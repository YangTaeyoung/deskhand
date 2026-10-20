import AppKit
import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// Records debugger calls as short strings, answers from configurable replies, can hold launch
/// and step replies, and lets the test push `debug.*` events or end the event stream.
final class FakeDebuggerBackend: DebuggerBackend, EventsBackend, @unchecked Sendable {
    struct State {
        var calls: [String] = []
        var sessions: [DebugSession] = []
        var launchResult: Result<DebugSession, OperationError>?
        var holdLaunch = false
        var heldLaunches: [CheckedContinuation<Void, Never>] = []
        var holdStep = false
        var heldSteps: [CheckedContinuation<Void, Never>] = []
        var stepReply: DebugTarget?
        var properties: [String: DebugPropertyPage] = [:]
        var states: [String: DebugPauseState] = [:]
        var breakpointReply: DebugBreakpoint?
        var consolePages: [ConsolePage] = []
        var evaluation = DebugEvaluation(value: RemoteValue(type: "number", valuePreview: "2"))
        var events: AsyncThrowingStream<DaemonEvent, Error>.Continuation?
        var subscriptions = 0
        var seq = 0
    }

    private let lock = NSLock()
    private var current = State()

    func withLock<T>(_ body: (inout State) throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try body(&current)
    }

    var calls: [String] { withLock { $0.calls } }
    var subscriptions: Int { withLock { $0.subscriptions } }
    var heldLaunchCount: Int { withLock { $0.heldLaunches.count } }
    var heldStepCount: Int { withLock { $0.heldSteps.count } }

    func configure(_ body: (inout State) -> Void) { withLock(body) }

    private func record(_ call: String) { withLock { $0.calls.append(call) } }

    func releaseLaunches() {
        let held = withLock { s -> [CheckedContinuation<Void, Never>] in
            s.holdLaunch = false
            defer { s.heldLaunches = [] }
            return s.heldLaunches
        }
        for continuation in held { continuation.resume() }
    }

    func releaseSteps() {
        let held = withLock { s -> [CheckedContinuation<Void, Never>] in
            s.holdStep = false
            defer { s.heldSteps = [] }
            return s.heldSteps
        }
        for continuation in held { continuation.resume() }
    }

    func send(_ kind: String, _ payload: [String: JSONValue], hostID: String = PreviewFixtures.devPCID) {
        let (event, continuation) = withLock { s -> (DaemonEvent, AsyncThrowingStream<DaemonEvent, Error>.Continuation?) in
            s.seq += 1
            var params = payload
            params["kind"] = .string(kind)
            params["host_id"] = .string(hostID)
            return (DaemonEvent(seq: s.seq, hostID: hostID, kind: kind, payload: params), s.events)
        }
        continuation?.yield(event)
    }

    func endEvents() {
        let continuation = withLock { s -> AsyncThrowingStream<DaemonEvent, Error>.Continuation? in
            defer { s.events = nil }
            return s.events
        }
        continuation?.finish()
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: DaemonEvent.self)
        withLock {
            $0.events = continuation
            $0.subscriptions += 1
        }
        return stream
    }

    func debugList(hostID: String) async throws -> [DebugSession] {
        record("list")
        return withLock { $0.sessions }
    }

    func debugGet(debugID: String) async throws -> DebugSession {
        record("get \(debugID)")
        guard let session = withLock({ $0.sessions.first { $0.debugID == debugID } }) else {
            throw OperationError(code: "NOT_FOUND", message: "없음")
        }
        return session
    }

    func debugLaunch(_ request: DebugLaunchRequest) async throws -> DebugSession {
        record("launch \(request.path)")
        let hold = withLock { $0.holdLaunch }
        if hold { await withCheckedContinuation { c in withLock { $0.heldLaunches.append(c) } } }
        guard let result = withLock({ $0.launchResult }) else { throw OperationError(code: "INTERNAL", message: "설정 안 됨") }
        let session = try result.get()
        withLock { $0.sessions.append(session) }
        return session
    }

    func debugAttach(_ request: DebugAttachRequest) async throws -> DebugSession {
        record("attach")
        guard let result = withLock({ $0.launchResult }) else { throw OperationError(code: "INTERNAL", message: "설정 안 됨") }
        return try result.get()
    }

    func debugTargetAttach(debugID: String, targetID: String) async throws -> DebugTarget {
        record("target.attach \(targetID)")
        throw OperationError(code: "INTERNAL", message: "없음")
    }

    func debugBreakpointSet(_ request: BreakpointRequest) async throws -> DebugBreakpoint {
        switch request.match {
        case .url(let url): record("bp.set url \(url):\(request.line)")
        case .regex(let regex): record("bp.set regex \(regex):\(request.line)")
        }
        guard let reply = withLock({ $0.breakpointReply }) else { throw OperationError(code: "INVALID_ARGUMENT", message: "거부") }
        return reply
    }

    func debugBreakpointEnable(debugID: String, breakpointID: String, enabled: Bool) async throws -> DebugBreakpoint {
        record("bp.enable \(breakpointID) \(enabled)")
        guard var reply = withLock({ $0.breakpointReply }) else { throw OperationError(code: "NOT_FOUND", message: "없음") }
        reply.enabled = enabled
        return reply
    }

    func debugBreakpointRemove(debugID: String, breakpointID: String) async throws { record("bp.remove \(breakpointID)") }
    func debugBreakpointsActivate(debugID: String, active: Bool) async throws { record("bp.activate \(active)") }
    func debugExceptions(debugID: String, mode: ExceptionPauseMode) async throws { record("exceptions \(mode.rawValue)") }

    func debugPause(debugID: String, targetID: String) async throws -> DebugTarget? {
        record("pause \(targetID)")
        return nil
    }

    func debugResume(debugID: String, targetID: String) async throws -> DebugTarget? {
        record("resume \(targetID)")
        return nil
    }

    func debugStep(debugID: String, targetID: String, kind: DebugStepKind) async throws -> DebugTarget? {
        record("step \(kind.rawValue) \(targetID)")
        let hold = withLock { $0.holdStep }
        if hold { await withCheckedContinuation { c in withLock { $0.heldSteps.append(c) } } }
        return withLock { $0.stepReply }
    }

    func debugState(debugID: String, targetID: String) async throws -> DebugPauseState {
        record("state \(targetID)")
        guard let state = withLock({ $0.states[targetID] }) else { throw OperationError(code: "NOT_FOUND", message: "없음") }
        return state
    }

    func debugVariables(debugID: String, targetID: String, objectID: String, offset: Int, limit: Int) async throws -> DebugPropertyPage {
        record("variables \(objectID)" + (offset > 0 ? " @\(offset)" : ""))
        return withLock { $0.properties[objectID] } ?? DebugPropertyPage(properties: [])
    }

    func debugEvaluate(debugID: String, targetID: String, expression: String, frameID: String?) async throws -> DebugEvaluation {
        record("evaluate \(expression) in \(frameID ?? "global")")
        return withLock { $0.evaluation }
    }

    func debugScripts(debugID: String, targetID: String?) async throws -> [DebugScript] { [] }

    func debugSource(debugID: String, targetID: String, scriptID: String) async throws -> DebugSource {
        record("source \(scriptID)")
        return DebugSource(scriptID: scriptID, url: "app://shop/\(scriptID).js", text: "line 1\nline 2")
    }

    func debugConsole(_ query: ConsoleQuery) async throws -> ConsolePage {
        let cursor = query.cursor.map { "cursor \($0)" } ?? query.before.map { "before \($0)" } ?? "latest"
        record("console \(cursor)")
        return withLock { s in s.consolePages.isEmpty ? ConsolePage(entries: [], nextCursor: query.cursor ?? 0) : s.consolePages.removeFirst() }
    }

    func debugClose(debugID: String, keepProcess: Bool) async throws { record("close \(debugID) keep=\(keepProcess)") }
}

@MainActor
final class DebuggerModelTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private let main = "t-main"
    private let page = "t-page"

    private func session(_ id: String = "d-1", state: DebugSessionState = .running, targets: [DebugTarget]? = nil) -> DebugSession {
        DebugSession(debugID: id, hostID: dev, kind: .launch, name: "Shop Admin", path: "C:\\Shop\\desktop\\Shop Admin.exe", pid: 10244,
                     state: state, targets: targets ?? [
                        DebugTarget(id: main, role: "main", type: "node", url: "file:///main.js", title: "main"),
                        DebugTarget(id: page, role: "renderer", type: "page", url: "app://shop/index.html", title: "결제"),
                     ], createdAt: Timestamp(date: .now))
    }

    private func frames(_ tag: String) -> [DebugCallFrame] {
        [
            DebugCallFrame(frameID: "cf0-\(tag)", function: "postOrder", scriptID: "89", url: "app://shop/api.js", line: 25, column: 3,
                           scopeChain: [DebugScope(type: "local", objectID: "L0-\(tag)"), DebugScope(type: "global", objectID: "G-\(tag)")]),
            DebugCallFrame(frameID: "cf1-\(tag)", function: "submitOrder", scriptID: "88", url: "app://shop/checkout.js", line: 31, column: 3,
                           scopeChain: [DebugScope(type: "local", objectID: "L1-\(tag)"), DebugScope(type: "global", objectID: "G-\(tag)")]),
        ]
    }

    private func paused(_ gen: Int, reason: DebugPauseReason = .breakpoint, target: String? = nil, tag: String? = nil) -> [String: JSONValue] {
        let state = DebugPauseState(targetID: target ?? page, gen: gen, paused: true, reason: reason, callFrames: frames(tag ?? String(gen)))
        return ["debug_id": .string("d-1"), "state": PreviewBackend.payload(state)]
    }

    private func resumed(_ gen: Int, target: String? = nil) -> [String: JSONValue] {
        ["debug_id": .string("d-1"), "target_id": .string(target ?? page), "gen": .int(gen)]
    }

    private func entry(_ seq: Int, _ text: String = "줄") -> ConsoleEntry {
        ConsoleEntry(seq: seq, time: Timestamp(date: .now), targetID: page, level: .log, text: text)
    }

    private func object(_ name: String, _ id: String) -> DebugProperty {
        DebugProperty(name: name, value: RemoteValue(type: "object", className: "Object", valuePreview: "{…}", objectID: id))
    }

    /// A started model whose session list holds `sessions`, after the list loaded.
    private func started(_ backend: FakeDebuggerBackend, sessions: [DebugSession] = []) async throws -> DebuggerModel {
        backend.configure { $0.sessions = sessions }
        let model = DebuggerModel(hostID: dev, backend: backend, defaults: UserDefaults(suiteName: "debugger-tests-\(UUID())")!,
                                  backoff: .init(initial: .milliseconds(10), maximum: .milliseconds(20)))
        model.start()
        try await waitUntil { model.listPhase == .loaded && backend.subscriptions == 1 }
        return model
    }

    func testLaunchGoesFromIdleThroughStartingToRunning() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend)
        XCTAssertEqual(model.phase, .idle)
        backend.configure {
            $0.launchResult = .success(self.session())
            $0.holdLaunch = true
        }
        let launch = Task { await model.launch(DebugLaunchRequest(hostID: dev, path: "C:\\Shop\\desktop\\Shop Admin.exe")) }
        try await waitUntil { backend.heldLaunchCount == 1 }
        XCTAssertEqual(model.phase, .starting("Shop Admin"))
        backend.releaseLaunches()
        let error = await launch.value
        XCTAssertNil(error)
        XCTAssertEqual(model.phase, .running)
        XCTAssertEqual(model.selected?.id, "d-1")
        XCTAssertEqual(model.selected?.selectedTargetID, page)
        try await waitUntil { backend.calls.contains("console latest") }
        XCTAssertEqual(model.recentLaunches.first?.path, "C:\\Shop\\desktop\\Shop Admin.exe")
    }

    func testFailedLaunchReturnsTheErrorAndStaysIdle() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend)
        backend.configure { $0.launchResult = .failure(OperationError(code: "TIMEOUT", message: "디버그 포트가 열리지 않았습니다")) }
        let error = await model.launch(DebugLaunchRequest(hostID: dev, path: "C:\\Shop\\bin\\Shop.exe"))
        XCTAssertEqual(error?.code, "TIMEOUT")
        XCTAssertEqual(model.phase, .idle)
        XCTAssertNil(model.starting)
    }

    func testPausedEventSelectsTheTargetAndLoadsScopesAndSource() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure { $0.properties["L0-1"] = DebugPropertyPage(properties: [self.object("order", "O1")]) }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        debug.select(target: main)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { model.phase == .paused(.breakpoint) }
        XCTAssertEqual(debug.selectedTargetID, page)
        XCTAssertEqual(debug.selectedFrame?.function, "postOrder")
        try await waitUntil { debug.variables.rows.map(\.title) == ["지역", "order", "전역"] }
        try await waitUntil { backend.calls.contains("source 89") }
        XCTAssertEqual(debug.source(for: debug.selectedFrame)?.lines.count, 2)

        debug.selectFrame(1)
        try await waitUntil { backend.calls.contains("variables L1-1") && backend.calls.contains("source 88") }
        XCTAssertEqual(debug.selectedFrame?.function, "submitOrder")
    }

    func testPauseAndResumeEventsApplyOnlyNewerGens() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        backend.send(EventKind.debugPaused, paused(5))
        try await waitUntil { model.phase == .paused(.breakpoint) }
        backend.send(EventKind.debugResumed, resumed(4))
        backend.send(EventKind.debugResumed, resumed(6))
        try await waitUntil { model.phase == .running }
        backend.send(EventKind.debugPaused, paused(6, reason: .step))
        backend.send(EventKind.debugPaused, paused(7, reason: .exception))
        try await waitUntil { model.phase == .paused(.exception) }
        XCTAssertEqual(model.selected?.pauseState?.gen, 7)
    }

    func testStepReplyOlderThanTheNextPauseIsIgnored() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(5))
        try await waitUntil { model.phase == .paused(.breakpoint) }
        backend.configure {
            $0.holdStep = true
            $0.stepReply = DebugTarget(id: self.page, role: "renderer", type: "page", url: "u", title: "결제", paused: false, gen: 6)
        }
        debug.step(.over)
        try await waitUntil { backend.heldStepCount == 1 }
        XCTAssertEqual(debug.command, .step(.over))
        backend.send(EventKind.debugResumed, resumed(6))
        backend.send(EventKind.debugPaused, paused(7, reason: .step))
        try await waitUntil { model.phase == .paused(.step) }
        backend.releaseSteps()
        try await waitUntil { debug.command == nil }
        XCTAssertEqual(model.phase, .paused(.step))
        XCTAssertEqual(debug.pauseState?.gen, 7)
    }

    func testTargetAheadOfTheKnownGenReadsItsState() async throws {
        let backend = FakeDebuggerBackend()
        let state = DebugPauseState(targetID: page, gen: 9, paused: true, reason: .pauseRequest, callFrames: frames("9"))
        backend.configure { $0.states[self.page] = state }
        let model = try await started(backend, sessions: [session()])
        let target = DebugTarget(id: page, role: "renderer", type: "page", url: "app://shop/index.html", title: "결제", paused: true, gen: 9)
        backend.send(EventKind.debugTargetChanged, ["debug_id": .string("d-1"), "target": PreviewBackend.payload(target), "removed": .bool(false)])
        try await waitUntil { model.phase == .paused(.pauseRequest) }
        XCTAssertTrue(backend.calls.contains("state \(page)"))
    }

    func testSessionListReadsTheStateOfPausedTargets() async throws {
        let backend = FakeDebuggerBackend()
        let state = DebugPauseState(targetID: page, gen: 3, paused: true, reason: .breakpoint, callFrames: frames("3"))
        backend.configure { $0.states[self.page] = state }
        var listed = session(state: .paused)
        listed.targets[1].paused = true
        listed.targets[1].gen = 3
        let model = try await started(backend, sessions: [listed])
        try await waitUntil { model.phase == .paused(.breakpoint) }
        XCTAssertEqual(model.selected?.selectedTargetID, page)
    }

    func testClosedEventEndsTheSession() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        backend.send(EventKind.debugClosed, ["debug_id": .string("d-1"), "reason": .string("process_exited"), "exit_code": .int(1)])
        try await waitUntil { model.phase == .closed(.processExited) }
        XCTAssertEqual(model.selected?.info.exitCode, 1)
        XCTAssertFalse(model.selected?.canControl ?? true)
    }

    func testBreakpointsFollowRepliesAndEvents() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        let unresolved = DebugBreakpoint(breakpointID: "bp-1", urlRegex: #"(^|[/\\])checkout\.js$"#, line: 31)
        backend.configure { $0.breakpointReply = unresolved }
        let error = await debug.setBreakpoint(.fileName("checkout.js"), line: 31, condition: nil)
        XCTAssertNil(error)
        XCTAssertEqual(debug.info.breakpoints.map(\.resolved), [false])

        var resolved = unresolved
        resolved.locations = [BreakpointLocation(targetID: page, scriptID: "88", url: "app://shop/checkout.js", line: 31, column: 3)]
        resolved.resolved = true
        backend.send(EventKind.debugBreakpointChanged, ["debug_id": .string("d-1"), "breakpoint": PreviewBackend.payload(resolved),
                                                        "removed": .bool(false)])
        try await waitUntil { debug.info.breakpoints.first?.resolved == true }

        backend.configure { $0.breakpointReply = resolved }
        debug.setBreakpointEnabled("bp-1", false)
        try await waitUntil { debug.info.breakpoints.first?.enabled == false }
        XCTAssertTrue(backend.calls.contains("bp.enable bp-1 false"))

        debug.toggleBreakpoint(url: "app://shop/checkout.js", line: 31)
        try await waitUntil { backend.calls.contains("bp.remove bp-1") }
        XCTAssertEqual(debug.info.breakpoints, [])
        debug.toggleBreakpoint(url: "app://shop/api.js", line: 25)
        try await waitUntil { backend.calls.contains("bp.set url app://shop/api.js:25") }

        backend.send(EventKind.debugBreakpointChanged, ["debug_id": .string("d-1"), "breakpoint": PreviewBackend.payload(resolved),
                                                        "removed": .bool(true)])
        try await waitUntil { !debug.info.breakpoints.contains { $0.breakpointID == "bp-1" } }
    }

    func testExpandedVariablesStayOpenAcrossAStep() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.properties["L0-1"] = DebugPropertyPage(properties: [self.object("order", "O1")])
            $0.properties["O1"] = DebugPropertyPage(properties: [DebugProperty(name: "total", value: RemoteValue(type: "number", valuePreview: "46800"))])
            $0.properties["L0-3"] = DebugPropertyPage(properties: [self.object("order", "O3")])
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { debug.variables.rows.contains { $0.title == "order" } }
        let order = try XCTUnwrap(debug.variables.rows.first { $0.title == "order" })
        debug.toggleVariable(order)
        try await waitUntil { debug.variables.rows.contains { $0.title == "total" } }
        XCTAssertEqual(backend.calls.filter { $0 == "variables O1" }.count, 1)
        debug.toggleVariable(order)
        debug.toggleVariable(order)
        XCTAssertEqual(backend.calls.filter { $0 == "variables O1" }.count, 1)

        backend.send(EventKind.debugResumed, resumed(2))
        try await waitUntil { model.phase == .running }
        XCTAssertEqual(debug.variables.rows, [])
        backend.send(EventKind.debugPaused, paused(3))
        try await waitUntil { backend.calls.contains("variables O3") }
    }

    func testConsoleLoadsTheLatestPageFollowsEventsAndCatchesUpAfterResubscribing() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.consolePages = [
                ConsolePage(entries: [self.entry(41), self.entry(42)], nextCursor: 42, hasEarlier: true),
                ConsolePage(entries: [self.entry(39), self.entry(40)], nextCursor: 40, hasEarlier: false),
            ]
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        try await waitUntil { debug.console.entries.map(\.seq) == [41, 42] }
        XCTAssertTrue(debug.console.hasEarlier)
        backend.send(EventKind.debugConsole, ["debug_id": .string("d-1"), "entry": PreviewBackend.payload(entry(44))])
        backend.send(EventKind.debugConsole, ["debug_id": .string("d-1"), "entry": PreviewBackend.payload(entry(43))])
        backend.send(EventKind.debugConsole, ["debug_id": .string("d-1"), "entry": PreviewBackend.payload(entry(44, "중복"))])
        try await waitUntil { debug.console.entries.map(\.seq) == [41, 42, 43, 44] }
        XCTAssertEqual(debug.console.entries.last?.text, "줄")

        await debug.loadEarlierConsole()
        XCTAssertTrue(backend.calls.contains("console before 41"))
        XCTAssertEqual(debug.console.entries.map(\.seq), [39, 40, 41, 42, 43, 44])
        XCTAssertFalse(debug.console.hasEarlier)

        backend.endEvents()
        try await waitUntil { backend.subscriptions == 2 && backend.calls.contains("console cursor 44") }
    }

    func testEvaluateRunsInTheSelectedFrameWhilePausedAndRecordsHistory() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        debug.evaluate("1+1")
        try await waitUntil { backend.calls.contains("evaluate 1+1 in global") }
        try await waitUntil {
            debug.console.evaluations.first?.result == .value(DebugEvaluation(value: RemoteValue(type: "number", valuePreview: "2")))
        }
        XCTAssertEqual(debug.history.items, ["1+1"])

        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { model.phase == .paused(.breakpoint) }
        debug.selectFrame(1)
        debug.evaluate("order.total")
        try await waitUntil { backend.calls.contains("evaluate order.total in cf1-1") }
        XCTAssertEqual(debug.console.evaluations.last?.frame, "submitOrder")
    }

    func testTogglePauseSendsPauseOrResumeForTheSelectedTarget() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        debug.togglePause()
        try await waitUntil { backend.calls.contains("pause \(page)") }
        backend.send(EventKind.debugPaused, paused(1, reason: .pauseRequest))
        try await waitUntil { model.phase == .paused(.pauseRequest) }
        debug.togglePause()
        try await waitUntil { backend.calls.contains("resume \(page)") }
    }

    func testCloseAsksTheDaemonAndKeepsTheSessionListed() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        await debug.close(keepProcess: true)
        XCTAssertTrue(backend.calls.contains("close d-1 keep=true"))
        XCTAssertEqual(model.phase, .closed(.requested))
        XCTAssertEqual(model.sessions.count, 1)
    }

    func testDebugMenuShortcutsApplyOnlyWhileTheDebuggerTabShows() {
        let backend = PreviewBackend(stepDelay: .zero)
        let defaults = UserDefaults(suiteName: "debugger-menu-\(UUID())")!
        let store = AppStore.seeded(backend: backend, defaults: defaults, hosts: PreviewFixtures.hosts(), selectedHostID: dev)
        store.layout.tab = .files
        XCTAssertFalse(DebuggerCommandContext(store: store).isShown)
        XCTAssertNil(DebuggerCommandContext(store: store).session)
        store.layout.tab = .debugger
        XCTAssertTrue(DebuggerCommandContext(store: store).isShown)
        XCTAssertTrue(DebuggerCommandContext(store: store).canStart)
        store.selectedHostID = PreviewFixtures.qaLaptopID
        XCTAssertFalse(DebuggerCommandContext(store: store).canStart, "the QA laptop's agent cannot open tunnels")
        XCTAssertEqual(FunctionKey.f8.character, Character(UnicodeScalar(UInt32(NSF8FunctionKey))!))
        XCTAssertEqual(FunctionKey.f11.character, Character(UnicodeScalar(UInt32(NSF11FunctionKey))!))
    }
}
