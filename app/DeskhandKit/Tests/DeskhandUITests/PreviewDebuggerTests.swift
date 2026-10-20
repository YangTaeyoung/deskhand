import DeskhandClient
import Foundation
import Synchronization
import XCTest
@testable import DeskhandUI

/// Collects the events of one subscription.
final class EventCollector: Sendable {
    private final class Box: Sendable {
        let events = Mutex<[DaemonEvent]>([])
    }

    private let box = Box()
    private let task: Task<Void, Never>

    init(_ backend: any EventsBackend, kinds: [String]) {
        let stream = backend.events(kinds: kinds, hostID: nil)
        let box = box
        task = Task {
            do { for try await event in stream { box.events.withLock { $0.append(event) } } } catch {}
        }
    }

    var all: [DaemonEvent] { box.events.withLock { $0 } }

    func of(_ kind: String) -> [DaemonEvent] { all.filter { $0.kind == kind } }

    func cancel() { task.cancel() }
}

/// The preview daemon's simulated Shop Admin session.
final class PreviewDebuggerTests: XCTestCase {
    typealias Program = PreviewDebugProgram
    private let dev = PreviewFixtures.devPCID

    private func launched(_ backend: PreviewBackend) async throws -> DebugSession {
        try await backend.debugLaunch(DebugLaunchRequest(hostID: dev, path: Program.appPath, args: ["--env=staging"]))
    }

    private func pausedStates(_ events: EventCollector) -> [DebugPauseState] {
        events.of(EventKind.debugPaused).compactMap { event in
            event.payload["state"].flatMap { try? LiveBackend.decode(DebugPauseState.self, from: $0) }
        }
    }

    private func waitForPause(_ events: EventCollector, count: Int) async throws -> DebugPauseState {
        let deadline = ContinuousClock.now + .seconds(3)
        while pausedStates(events).count < count {
            guard ContinuousClock.now < deadline else { throw OperationError(message: "no pause \(count)") }
            try await Task.sleep(for: .milliseconds(5))
        }
        return pausedStates(events)[count - 1]
    }

    func testLaunchListsMainAndRendererAndPrintsLaunchMessages() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let session = try await launched(backend)
        XCTAssertEqual(session.name, "Shop Admin")
        XCTAssertEqual(session.targets.map(\.targetRole), [.main, .renderer])
        XCTAssertEqual(session.args, ["--env=staging"])
        let page = try await backend.debugConsole(ConsoleQuery(debugID: session.debugID))
        XCTAssertEqual(page.entries.map(\.seq), Array(1...Program.launchConsole().count))
        XCTAssertTrue(page.entries.contains { $0.consoleLevel == .warning && $0.text.contains("쿠폰 서버") })
        let listed = try await backend.debugList(hostID: dev)
        XCTAssertEqual(listed.map(\.debugID), [session.debugID])
        let scripts = try await backend.debugScripts(debugID: session.debugID, targetID: Program.pageTargetID)
        XCTAssertEqual(Set(scripts.map(\.url)), Set([Program.Script.index.url, Program.Script.checkout.url, Program.Script.api.url]))
    }

    func testLaunchFailuresMatchTheContract() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let cases: [(String, String, String)] = [
            (dev, Program.notElectronPath, "TIMEOUT"),
            (dev, "C:\\Shop\\notes.txt", "NOT_FOUND"),
            (PreviewFixtures.qaLaptopID, Program.appPath, "CAPABILITY_UNSUPPORTED"),
            (PreviewFixtures.designID, Program.appPath, "OFFLINE"),
        ]
        for (host, path, code) in cases {
            do {
                _ = try await backend.debugLaunch(DebugLaunchRequest(hostID: host, path: path))
                XCTFail("\(path) on \(host) launches")
            } catch {
                XCTAssertEqual(OperationError(error).code, code, path)
            }
        }
    }

    func testBreakpointsResolveInLoadedScriptsAndMoveToTheNextCodeLine() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let session = try await launched(backend)
        let checkout = Program.Script.checkout
        let blank = checkout.line("function subtotalOf") - 1
        let moved = try await backend.debugBreakpointSet(BreakpointRequest(
            debugID: session.debugID, match: try BreakpointPattern.fileName("checkout.js").match(), line: blank))
        XCTAssertTrue(moved.resolved)
        XCTAssertEqual(moved.locations.map(\.line), [blank + 1])
        XCTAssertEqual(moved.locations.first?.scriptID, checkout.scriptID)

        let pending = try await backend.debugBreakpointSet(BreakpointRequest(
            debugID: session.debugID, match: try BreakpointPattern.fileName("settings.js").match(), line: 3))
        XCTAssertFalse(pending.resolved)

        do {
            _ = try await backend.debugBreakpointSet(BreakpointRequest(debugID: session.debugID, match: .regex("api("), line: 3))
            XCTFail("invalid regex is refused")
        } catch {
            XCTAssertEqual(OperationError(error).code, "INVALID_ARGUMENT")
        }
    }

    func testSubmitFlowStopsAtABreakpointAndStepsOverIntoAndOut() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let events = EventCollector(backend, kinds: EventKind.debuggerKinds)
        defer { events.cancel() }
        let session = try await launched(backend)
        let api = Program.Script.api
        _ = try await backend.debugBreakpointSet(BreakpointRequest(debugID: session.debugID, match: .url(api.url),
                                                                   line: api.line("const payload = {")))
        let run = Task { await backend.debugRun(debugID: session.debugID, flow: .submit) }

        let first = try await waitForPause(events, count: 1)
        XCTAssertEqual(first.pauseReason, .breakpoint)
        XCTAssertEqual(first.callFrames.map(\.function), ["postOrder", "submitOrder", ""])
        XCTAssertEqual(first.hitBreakpoints.count, 1)
        let state = try await backend.debugState(debugID: session.debugID, targetID: Program.pageTargetID)
        XCTAssertEqual(state.gen, first.gen)

        let over = try await backend.debugStep(debugID: session.debugID, targetID: Program.pageTargetID, kind: .over)
        XCTAssertEqual(over?.gen, first.gen + 1)
        let second = try await waitForPause(events, count: 2)
        XCTAssertEqual(second.callFrames.first?.line, api.line("return request('/orders'"))
        XCTAssertEqual(second.pauseReason, .step)

        _ = try await backend.debugStep(debugID: session.debugID, targetID: Program.pageTargetID, kind: .into)
        let third = try await waitForPause(events, count: 3)
        XCTAssertEqual(third.callFrames.first?.function, "request")

        _ = try await backend.debugStep(debugID: session.debugID, targetID: Program.pageTargetID, kind: .out)
        let fourth = try await waitForPause(events, count: 4)
        XCTAssertEqual(fourth.callFrames.map(\.function), ["submitOrder", ""])

        _ = try await backend.debugResume(debugID: session.debugID, targetID: Program.pageTargetID)
        await run.value
        XCTAssertEqual(events.of(EventKind.debugResumed).count, 4)
        let resumedGens = events.of(EventKind.debugResumed).compactMap { $0.payload["gen"]?.intValue }
        XCTAssertEqual(resumedGens, resumedGens.sorted())
        let console = try await backend.debugConsole(ConsoleQuery(debugID: session.debugID))
        XCTAssertTrue(console.entries.contains { $0.text.hasPrefix("주문 제출") })

        do {
            _ = try await backend.debugStep(debugID: session.debugID, targetID: Program.pageTargetID, kind: .over)
            XCTFail("stepping a running target fails")
        } catch {
            XCTAssertEqual(OperationError(error).code, "STALE_TARGET")
        }
    }

    func testVariablesFollowObjectPathsAndGoStaleAfterResume() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let events = EventCollector(backend, kinds: EventKind.debuggerKinds)
        defer { events.cancel() }
        let session = try await launched(backend)
        let api = Program.Script.api
        _ = try await backend.debugBreakpointSet(BreakpointRequest(debugID: session.debugID, match: .url(api.url),
                                                                   line: api.line("return request('/orders'")))
        let run = Task { await backend.debugRun(debugID: session.debugID, flow: .submit) }
        let pause = try await waitForPause(events, count: 1)
        let local = try XCTUnwrap(pause.callFrames.first?.scopeChain.first)
        XCTAssertEqual(local.type, "local")
        let scope = try await backend.debugVariables(debugID: session.debugID, targetID: Program.pageTargetID, objectID: local.objectID,
                                                    offset: 0, limit: 100)
        XCTAssertEqual(scope.properties.map(\.name), ["order", "payload"])
        let order = try XCTUnwrap(scope.properties.first?.value)
        XCTAssertTrue(order.valuePreview.hasPrefix("{id: \"ORD-2026-1021-0042\""))
        let orderID = try XCTUnwrap(order.objectID)
        let fields = try await backend.debugVariables(debugID: session.debugID, targetID: Program.pageTargetID, objectID: orderID,
                                                     offset: 0, limit: 100)
        XCTAssertEqual(fields.properties.map(\.name), ["id", "customer", "lines", "subtotal", "discount", "total", "[[Prototype]]"])
        XCTAssertEqual(fields.properties.first { $0.name == "lines" }?.value.valuePreview.hasPrefix("Array(3)"), true)
        let paged = try await backend.debugVariables(debugID: session.debugID, targetID: Program.pageTargetID, objectID: orderID,
                                                    offset: 5, limit: 1)
        XCTAssertEqual(paged.properties.map(\.name), ["total"])
        XCTAssertEqual(paged.total, 7)

        _ = try await backend.debugResume(debugID: session.debugID, targetID: Program.pageTargetID)
        await run.value
        do {
            _ = try await backend.debugVariables(debugID: session.debugID, targetID: Program.pageTargetID, objectID: orderID, offset: 0, limit: 10)
            XCTFail("object IDs of a resumed pause are stale")
        } catch {
            XCTAssertEqual(OperationError(error).code, "STALE_TARGET")
        }
    }

    func testCouponFlowStopsOnTheUncaughtExceptionThenLogsIt() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let events = EventCollector(backend, kinds: EventKind.debuggerKinds)
        defer { events.cancel() }
        let session = try await launched(backend)
        let run = Task { await backend.debugRun(debugID: session.debugID, flow: .coupon) }
        let pause = try await waitForPause(events, count: 1)
        XCTAssertEqual(pause.pauseReason, .exception)
        XCTAssertEqual(pause.exception?.className, "TypeError")
        XCTAssertEqual(pause.callFrames.map(\.function), ["applyDiscount", "applyCoupon", ""])
        let frame = try XCTUnwrap(pause.callFrames.first)
        let failing = try await backend.debugEvaluate(debugID: session.debugID, targetID: Program.pageTargetID,
                                                     expression: "order.summary.total", frameID: frame.frameID)
        XCTAssertEqual(failing.exception?.text, "Uncaught TypeError: Cannot read properties of undefined (reading 'total')")
        let rate = try await backend.debugEvaluate(debugID: session.debugID, targetID: Program.pageTargetID,
                                                  expression: "coupon.rate", frameID: frame.frameID)
        XCTAssertEqual(rate.value.valuePreview, "0.1")
        let missing = try await backend.debugEvaluate(debugID: session.debugID, targetID: Program.pageTargetID,
                                                     expression: "totl", frameID: frame.frameID)
        XCTAssertEqual(missing.exception?.text, "Uncaught ReferenceError: totl is not defined")

        _ = try await backend.debugResume(debugID: session.debugID, targetID: Program.pageTargetID)
        await run.value
        let console = try await backend.debugConsole(ConsoleQuery(debugID: session.debugID))
        let error = try XCTUnwrap(console.entries.last)
        XCTAssertEqual(error.consoleSource, .exception)
        XCTAssertEqual(error.stack?.map(\.function), ["applyDiscount", "applyCoupon", "HTMLButtonElement.<anonymous>"])

        try await backend.debugExceptions(debugID: session.debugID, mode: .none)
        await backend.debugRun(debugID: session.debugID, flow: .coupon)
        XCTAssertEqual(pausedStates(events).count, 1)
        let after = try await backend.debugConsole(ConsoleQuery(debugID: session.debugID, cursor: error.seq))
        XCTAssertEqual(after.entries.map(\.consoleSource), [.exception])
    }

    func testGlobalEvaluationReadsTheWindowScope() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let session = try await launched(backend)
        let store = try await backend.debugEvaluate(debugID: session.debugID, targetID: Program.pageTargetID,
                                                   expression: "shopConfig.store", frameID: nil)
        XCTAssertEqual(store.value.valuePreview, "\"강남점\"")
        let config = try await backend.debugEvaluate(debugID: session.debugID, targetID: Program.pageTargetID,
                                                    expression: "shopConfig", frameID: nil)
        let id = try XCTUnwrap(config.value.objectID)
        let fields = try await backend.debugVariables(debugID: session.debugID, targetID: Program.pageTargetID, objectID: id, offset: 0, limit: 50)
        XCTAssertEqual(fields.properties.first?.name, "apiBase")
    }

    func testConsolePagesFollowTheCursorRules() {
        let entries = (1...10).map { ConsoleEntry(seq: $0, time: Timestamp(date: .now), targetID: "t", level: .log, text: "\($0)") }
        let latest = PreviewBackend.page(entries, ConsoleQuery(debugID: "d", limit: 3))
        XCTAssertEqual(latest.entries.map(\.seq), [8, 9, 10])
        XCTAssertTrue(latest.hasEarlier)
        XCTAssertEqual(latest.nextCursor, 10)
        let after = PreviewBackend.page(entries, ConsoleQuery(debugID: "d", cursor: 6, limit: 3))
        XCTAssertEqual(after.entries.map(\.seq), [7, 8, 9])
        XCTAssertTrue(after.hasMore)
        let before = PreviewBackend.page(entries, ConsoleQuery(debugID: "d", before: 8, limit: 3))
        XCTAssertEqual(before.entries.map(\.seq), [5, 6, 7])
        XCTAssertTrue(before.hasEarlier)
        let empty = PreviewBackend.page(entries, ConsoleQuery(debugID: "d", cursor: 10))
        XCTAssertEqual(empty.entries, [])
        XCTAssertEqual(empty.nextCursor, 10)
    }

    func testCloseEndsTheSessionAndLaterCallsAreNotFound() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let events = EventCollector(backend, kinds: EventKind.debuggerKinds)
        defer { events.cancel() }
        let session = try await launched(backend)
        try await backend.debugClose(debugID: session.debugID, keepProcess: false)
        try await waitUntil { !events.of(EventKind.debugClosed).isEmpty }
        XCTAssertEqual(events.of(EventKind.debugClosed).first?.payload["reason"]?.stringValue, "requested")
        let listed = try await backend.debugList(hostID: dev)
        XCTAssertEqual(listed.first?.sessionState, .closed)
        do {
            _ = try await backend.debugPause(debugID: session.debugID, targetID: Program.pageTargetID)
            XCTFail("closed sessions refuse calls")
        } catch {
            XCTAssertEqual(OperationError(error).code, "NOT_FOUND")
        }
    }

    func testTimedSimulationStopsAtABreakpointOnItsOwnAndResolvesTheLateScript() async throws {
        let backend = PreviewBackend(stepDelay: .milliseconds(10))
        let events = EventCollector(backend, kinds: EventKind.debuggerKinds)
        defer { events.cancel() }
        let session = try await launched(backend)
        let receipt = try await backend.debugBreakpointSet(BreakpointRequest(
            debugID: session.debugID, match: try BreakpointPattern.fileName("receipt.js").match(), line: 2))
        XCTAssertFalse(receipt.resolved)
        _ = try await backend.debugBreakpointSet(BreakpointRequest(
            debugID: session.debugID, match: .url(checkout.url), line: checkout.line("const discount =")))
        let pause = try await waitForPause(events, count: 1)
        XCTAssertEqual(pause.callFrames.first?.function, "submitOrder")
        _ = try await backend.debugResume(debugID: session.debugID, targetID: Program.pageTargetID)
        try await waitUntil {
            events.of(EventKind.debugBreakpointChanged).contains { event in
                event.payload["breakpoint"].flatMap { try? LiveBackend.decode(DebugBreakpoint.self, from: $0) }
                    .map { $0.breakpointID == receipt.breakpointID && $0.resolved } ?? false
            }
        }
        try await backend.debugClose(debugID: session.debugID, keepProcess: false)
    }

    private var checkout: Program.Script { .checkout }
}
