import AppKit
import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// Records debugger calls as short strings, answers from configurable replies, can hold launch,
/// step and variables replies, and lets the test push `debug.*` events, end the event stream or
/// report a reconnect.
final class FakeDebuggerBackend: DebuggerBackend, EventsBackend, @unchecked Sendable {
    struct State {
        var calls: [String] = []
        var sessions: [DebugSession] = []
        var launchResult: Result<DebugSession, OperationError>?
        var launches: [DebugLaunchRequest] = []
        var holdLaunch = false
        var heldLaunches: [CheckedContinuation<Void, Never>] = []
        var holdStep = false
        var heldSteps: [CheckedContinuation<Void, Never>] = []
        var stepReply: DebugTarget?
        var properties: [String: DebugPropertyPage] = [:]
        /// Objects whose `debug.variables` reply waits for `releaseVariables`.
        var holdVariables: Set<String> = []
        var heldVariables: [CheckedContinuation<Void, Never>] = []
        var states: [String: DebugPauseState] = [:]
        /// Failures `debug.state` answers first, per target.
        var stateFailures: [String: Int] = [:]
        var breakpointReply: DebugBreakpoint?
        var removeReply: DebugBreakpoint?
        var consolePages: [ConsolePage] = []
        var clearedThrough = 0
        var inspectorLeftOpen = false
        /// `debug.breakpoints.activate` and `debug.exceptions` wait for `releaseSettings`.
        var holdSettings = false
        var heldSettings: [CheckedContinuation<Void, Never>] = []
        var evaluation = DebugEvaluation(value: RemoteValue(type: "number", valuePreview: "2"))
        var events: AsyncThrowingStream<DaemonEvent, Error>.Continuation?
        var reconnects: AsyncStream<Void>.Continuation?
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
    var heldVariablesCount: Int { withLock { $0.heldVariables.count } }
    var heldSettingsCount: Int { withLock { $0.heldSettings.count } }
    var launches: [DebugLaunchRequest] { withLock { $0.launches } }

    func configure(_ body: (inout State) -> Void) { withLock(body) }

    private func record(_ call: String) { withLock { $0.calls.append(call) } }

    private func release(_ take: (inout State) -> [CheckedContinuation<Void, Never>]) {
        for continuation in withLock(take) { continuation.resume() }
    }

    func releaseLaunches() {
        release { s in
            s.holdLaunch = false
            defer { s.heldLaunches = [] }
            return s.heldLaunches
        }
    }

    func releaseSteps() {
        release { s in
            s.holdStep = false
            defer { s.heldSteps = [] }
            return s.heldSteps
        }
    }

    func releaseSettings() {
        release { s in
            s.holdSettings = false
            defer { s.heldSettings = [] }
            return s.heldSettings
        }
    }

    private func holdSettingsIfAsked() async {
        let hold = withLock { $0.holdSettings }
        if hold { await withCheckedContinuation { c in withLock { $0.heldSettings.append(c) } } }
    }

    func releaseVariables() {
        release { s in
            s.holdVariables = []
            defer { s.heldVariables = [] }
            return s.heldVariables
        }
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

    /// Reports that the daemon connection came back.
    func reconnect() {
        _ = withLock { $0.reconnects }?.yield()
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: DaemonEvent.self)
        withLock {
            $0.events = continuation
            $0.subscriptions += 1
        }
        return stream
    }

    func reconnects() -> AsyncStream<Void> {
        let (stream, continuation) = AsyncStream.makeStream(of: Void.self)
        withLock { $0.reconnects = continuation }
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
        withLock { $0.launches.append(request) }
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

    func debugBreakpointSet(debugID: String, spec: BreakpointSpec) async throws -> DebugBreakpoint {
        record("bp.set \(spec.match) \(spec.pattern):\(spec.line)")
        guard let reply = withLock({ $0.breakpointReply }) else { throw OperationError(code: "INVALID_ARGUMENT", message: "거부") }
        return reply
    }

    func debugBreakpointUpdate(debugID: String, breakpointID: String, enabled: Bool?, condition: String?) async throws -> DebugBreakpoint {
        record("bp.update \(breakpointID) enabled=\(enabled.map(String.init) ?? "-") condition=\(condition ?? "-")")
        guard var reply = withLock({ $0.breakpointReply }) else { throw OperationError(code: "NOT_FOUND", message: "없음") }
        if let enabled { reply.enabled = enabled }
        if let condition { reply.condition = condition.isEmpty ? nil : condition }
        reply.rev += 1
        withLock { $0.breakpointReply = reply }
        return reply
    }

    func debugBreakpointRemove(debugID: String, breakpointID: String) async throws -> DebugBreakpoint? {
        record("bp.remove \(breakpointID)")
        return withLock { $0.removeReply }
    }

    func debugBreakpointsActivate(debugID: String, active: Bool) async throws -> DebugSession? {
        record("bp.activate \(active)")
        await holdSettingsIfAsked()
        return nil
    }

    func debugExceptions(debugID: String, mode: ExceptionPauseMode) async throws -> DebugSession? {
        record("exceptions \(mode.rawValue)")
        await holdSettingsIfAsked()
        return nil
    }

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
        let failing = withLock { s -> Bool in
            guard let left = s.stateFailures[targetID], left > 0 else { return false }
            s.stateFailures[targetID] = left - 1
            return true
        }
        if failing { throw OperationError(code: "TIMEOUT", message: "응답 없음") }
        guard let state = withLock({ $0.states[targetID] }) else { throw OperationError(code: "NOT_FOUND", message: "없음") }
        return state
    }

    func debugVariables(debugID: String, targetID: String, objectID: String, offset: Int, limit: Int) async throws -> DebugPropertyPage {
        record("variables \(objectID)" + (offset > 0 ? " @\(offset)" : "") + " in \(targetID)")
        let hold = withLock { $0.holdVariables.contains(objectID) }
        if hold { await withCheckedContinuation { c in withLock { $0.heldVariables.append(c) } } }
        return withLock { $0.properties[objectID] } ?? DebugPropertyPage(properties: [])
    }

    func debugEvaluate(debugID: String, targetID: String, expression: String, frameID: String?) async throws -> DebugEvaluation {
        record("evaluate \(expression) in \(frameID ?? "global")")
        return withLock { $0.evaluation }
    }

    func debugScripts(debugID: String, targetID: String?) async throws -> [DebugScript] {
        record("scripts")
        throw OperationError(code: "STALE_TARGET", message: "대상이 다시 로드되었습니다")
    }

    func debugSource(debugID: String, targetID: String, scriptID: String) async throws -> DebugSource {
        record("source \(scriptID)")
        return DebugSource(scriptID: scriptID, url: "app://shop/\(scriptID).js", text: "line 1\nline 2")
    }

    func debugConsole(_ query: ConsoleQuery) async throws -> ConsolePage {
        let cursor = query.cursor.map { "cursor \($0)" } ?? query.before.map { "before \($0)" } ?? "latest"
        record("console \(cursor)")
        return withLock { s in s.consolePages.isEmpty ? ConsolePage(entries: [], nextCursor: query.cursor ?? 0) : s.consolePages.removeFirst() }
    }

    func debugConsoleClear(debugID: String) async throws -> Int {
        record("console.clear")
        return withLock { $0.clearedThrough }
    }

    func debugClose(debugID: String, keepProcess: Bool) async throws -> DebugCloseResult {
        record("close \(debugID) keep=\(keepProcess)")
        return DebugCloseResult(inspectorLeftOpen: withLock { $0.inspectorLeftOpen })
    }
}

@MainActor
final class DebuggerModelTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private let main = "t-main"
    private let page = "t-page"

    private func session(_ id: String = "d-1", state: DebugSessionState = .running, targets: [DebugTarget]? = nil,
                         breakpoints: [DebugBreakpoint] = []) -> DebugSession {
        DebugSession(debugID: id, hostID: dev, kind: .launch, name: "Shop Admin", path: "C:\\Shop\\desktop\\Shop Admin.exe",
                     args: ["--env=staging"], cwd: "C:\\Shop\\desktop", pauseOnStart: true, pid: 10244,
                     state: state, targets: targets ?? [
                        DebugTarget(id: main, role: "main", type: "node", url: "file:///main.js", title: "main"),
                        DebugTarget(id: page, role: "renderer", type: "page", url: "app://shop/index.html", title: "결제"),
                     ], breakpoints: breakpoints, pauseOnExceptions: .all, createdAt: Timestamp(date: .now))
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

    private func breakpoint(rev: Int, line: Int = 31, enabled: Bool = true, resolved: Bool = false) -> DebugBreakpoint {
        DebugBreakpoint(breakpointID: "bp-1", match: .fileName, pattern: "checkout.js", line: line, enabled: enabled,
                        locations: resolved ? [BreakpointLocation(targetID: page, scriptID: "88", url: "app://shop/checkout.js", line: line, column: 3)] : [],
                        rev: rev)
    }

    private func breakpointEvent(_ breakpoint: DebugBreakpoint, removed: Bool = false) -> [String: JSONValue] {
        ["debug_id": .string("d-1"), "breakpoint": PreviewBackend.payload(breakpoint), "removed": .bool(removed)]
    }

    private static let fast = DebugTimings(consoleFrame: .milliseconds(5), stateRetries: [.milliseconds(5), .milliseconds(5)])

    /// A started model whose session list holds `sessions`, after the list loaded.
    private func started(_ backend: FakeDebuggerBackend, sessions: [DebugSession] = [],
                         timings: DebugTimings = DebuggerModelTests.fast) async throws -> DebuggerModel {
        backend.configure { $0.sessions = sessions }
        let model = DebuggerModel(hostID: dev, backend: backend, defaults: UserDefaults(suiteName: "debugger-tests-\(UUID())")!,
                                  backoff: .init(initial: .milliseconds(10), maximum: .milliseconds(20)), timings: timings)
        model.start()
        try await waitUntil { model.listPhase == .loaded && backend.subscriptions == 1 }
        return model
    }

    // MARK: Launching

    func testLaunchGoesFromIdleThroughStartingToRunningAndRemembersItsParameters() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend)
        XCTAssertEqual(model.phase, .idle)
        backend.configure {
            $0.launchResult = .success(self.session())
            $0.holdLaunch = true
        }
        let spec = BreakpointSpec(match: .fileName, pattern: "checkout.js", line: 31)
        let request = DebugLaunchRequest(hostID: dev, path: "C:\\Shop\\desktop\\Shop Admin.exe", args: ["--env=staging"], cwd: "C:\\Shop",
                                         pauseOnStart: true, breakpoints: [spec], pauseOnExceptions: .all)
        let launch = Task { await model.launch(request) }
        try await waitUntil { backend.heldLaunchCount == 1 }
        XCTAssertEqual(model.phase, .starting("Shop Admin"))
        backend.releaseLaunches()
        let error = await launch.value
        XCTAssertNil(error)
        XCTAssertEqual(model.phase, .running)
        XCTAssertEqual(model.selected?.id, "d-1")
        XCTAssertEqual(model.selected?.selectedTargetID, page)
        try await waitUntil { backend.calls.contains("console latest") }
        let recent = try XCTUnwrap(model.recentLaunches.first)
        XCTAssertEqual(recent.request(hostID: dev), request)
        XCTAssertEqual(LaunchDraft(recent).request(hostID: dev), request)
    }

    func testFailedLaunchReturnsTheErrorAndStaysIdle() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend)
        backend.configure { $0.launchResult = .failure(OperationError(code: "EXITED_EARLY", message: "앱이 종료되었습니다")) }
        let error = await model.launch(DebugLaunchRequest(hostID: dev, path: "C:\\Shop\\bin\\Shop.exe"))
        XCTAssertEqual(error?.code, "EXITED_EARLY")
        XCTAssertEqual(model.phase, .idle)
        XCTAssertNil(model.starting)
        XCTAssertNotNil(LaunchSheet.fix(error!))
    }

    func testRelaunchCarriesArgumentsFolderStartPauseBreakpointsAndExceptionSetting() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session(state: .closed, breakpoints: [breakpoint(rev: 2, resolved: true)])])
        let request = try XCTUnwrap(model.selected?.relaunchRequest)
        XCTAssertEqual(request.args, ["--env=staging"])
        XCTAssertEqual(request.cwd, "C:\\Shop\\desktop")
        XCTAssertTrue(request.pauseOnStart)
        XCTAssertEqual(request.breakpoints, [BreakpointSpec(match: .fileName, pattern: "checkout.js", line: 31, enabled: true)])
        XCTAssertEqual(request.pauseOnExceptions, .all)
    }

    // MARK: Pauses

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
        guard case .loaded(let source) = debug.sourceState(targetID: page, scriptID: "89") else { return XCTFail("source not loaded") }
        XCTAssertEqual(source.text.lines, ["line 1", "line 2"])

        debug.selectFrame(1)
        try await waitUntil { backend.calls.contains("variables L1-1 in \(page)") && backend.calls.contains("source 88") }
        XCTAssertEqual(debug.selectedFrame?.function, "submitOrder")
        debug.moveFrameSelection(by: -1)
        XCTAssertEqual(debug.selectedFrame?.function, "postOrder")
        debug.moveFrameSelection(by: -1)
        XCTAssertEqual(debug.selectedFrameIndex, 0)
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

    func testTargetFieldsApplyWhilePauseFieldsWaitForANewerGen() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(5))
        try await waitUntil { model.phase == .paused(.breakpoint) }
        let older = DebugTarget(id: page, role: "renderer", type: "page", url: "app://shop/index.html", title: "결제 · 새 제목", paused: false, gen: 4)
        backend.send(EventKind.debugTargetChanged, ["debug_id": .string("d-1"), "target": PreviewBackend.payload(older), "removed": .bool(false)])
        try await waitUntil { debug.selectedTarget?.title == "결제 · 새 제목" }
        XCTAssertEqual(model.phase, .paused(.breakpoint))
        XCTAssertTrue(debug.selectedTarget?.paused ?? false)
    }

    func testFailedStateReadsRetryWithoutAdvancingTheGenThenApply() async throws {
        let backend = FakeDebuggerBackend()
        let state = DebugPauseState(targetID: page, gen: 3, paused: true, reason: .breakpoint, callFrames: frames("3"))
        backend.configure {
            $0.states[self.page] = state
            $0.stateFailures[self.page] = 2
        }
        var listed = session(state: .paused)
        listed.targets[1].paused = true
        listed.targets[1].gen = 3
        let model = try await started(backend, sessions: [listed])
        try await waitUntil { model.phase == .paused(.breakpoint) }
        XCTAssertEqual(backend.calls.filter { $0 == "state \(page)" }.count, 3)
        XCTAssertNil(model.selected?.stateFailures[page])
    }

    func testStateReadThatKeepsFailingShowsTheErrorUntilARetrySucceeds() async throws {
        let backend = FakeDebuggerBackend()
        let state = DebugPauseState(targetID: page, gen: 3, paused: true, reason: .breakpoint, callFrames: frames("3"))
        backend.configure {
            $0.states[self.page] = state
            $0.stateFailures[self.page] = 3
        }
        var listed = session(state: .paused)
        listed.targets[1].paused = true
        listed.targets[1].gen = 3
        let model = try await started(backend, sessions: [listed])
        let debug = try XCTUnwrap(model.selected)
        try await waitUntil { debug.stateFailures[self.page] != nil }
        XCTAssertEqual(debug.stateFailures[page]?.code, "TIMEOUT")
        XCTAssertTrue(debug.awaitingState)
        XCTAssertEqual(debug.pauses.gen(of: page), -1)
        XCTAssertEqual(model.phase, .running)
        XCTAssertTrue(debug.isPaused(page))
        XCTAssertEqual(model.pausedElsewhere?.gen, 3)
        XCTAssertNil(model.pausedElsewhere?.location)
        debug.togglePause()
        try await waitUntil { backend.calls.contains("resume \(self.page)") && debug.command == nil }
        debug.retryState()
        try await waitUntil { model.phase == .paused(.breakpoint) }
        XCTAssertNil(debug.stateFailures[page])
        XCTAssertFalse(debug.awaitingState)
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

    func testPauseOfAnotherTabShowsUntilDismissedOrResumed() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        XCTAssertNil(model.pausedElsewhere)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { model.pausedElsewhere != nil }
        let pause = try XCTUnwrap(model.pausedElsewhere)
        XCTAssertEqual(pause.appName, "Shop Admin")
        XCTAssertEqual(pause.location, "api.js:25")
        model.dismiss(pause)
        XCTAssertNil(model.pausedElsewhere)
        backend.send(EventKind.debugResumed, resumed(2))
        backend.send(EventKind.debugPaused, paused(3))
        try await waitUntil { model.pausedElsewhere?.gen == 3 }
        backend.send(EventKind.debugResumed, resumed(4))
        try await waitUntil { model.pausedElsewhere == nil }
    }

    func testClosedEventEndsTheSession() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        backend.send(EventKind.debugClosed, ["debug_id": .string("d-1"), "reason": .string("process_exited"), "exit_code": .int(1)])
        try await waitUntil { model.phase == .closed(.processExited) }
        XCTAssertEqual(model.selected?.info.exitCode, 1)
        XCTAssertFalse(model.selected?.canControl ?? true)
    }

    // MARK: Breakpoints

    func testBreakpointsFollowRepliesAndEventsByRev() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.configure { $0.breakpointReply = self.breakpoint(rev: 1) }
        let error = await debug.setBreakpoint(.fileName("checkout.js"), line: 31, condition: nil)
        XCTAssertNil(error)
        XCTAssertTrue(backend.calls.contains("bp.set file_name checkout.js:31"))
        XCTAssertEqual(debug.info.breakpoints.map(\.resolved), [false])

        backend.send(EventKind.debugBreakpointChanged, breakpointEvent(breakpoint(rev: 2, resolved: true)))
        try await waitUntil { debug.info.breakpoints.first?.resolved == true }
        backend.send(EventKind.debugBreakpointChanged, breakpointEvent(breakpoint(rev: 1)))
        backend.send(EventKind.debugBreakpointChanged, breakpointEvent(breakpoint(rev: 3, line: 32, resolved: true)))
        try await waitUntil { debug.info.breakpoints.first?.line == 32 }
        XCTAssertTrue(debug.info.breakpoints.first?.resolved ?? false)

        backend.configure { $0.breakpointReply = self.breakpoint(rev: 3, line: 32, resolved: true) }
        debug.setBreakpointEnabled("bp-1", false)
        try await waitUntil { debug.info.breakpoints.first?.enabled == false }
        XCTAssertTrue(backend.calls.contains("bp.update bp-1 enabled=false condition=-"))
        let refused = await debug.setBreakpointCondition("bp-1", " order.total > 0 ")
        XCTAssertNil(refused)
        XCTAssertEqual(debug.info.breakpoints.first?.condition, "order.total > 0")

        backend.configure { $0.removeReply = self.breakpoint(rev: 9, line: 32) }
        debug.toggleBreakpoint(url: "app://shop/checkout.js", line: 32)
        try await waitUntil { backend.calls.contains("bp.remove bp-1") }
        try await waitUntil { debug.info.breakpoints.isEmpty }
        backend.send(EventKind.debugBreakpointChanged, breakpointEvent(breakpoint(rev: 8, line: 32, resolved: true)))
        backend.send(EventKind.debugBreakpointChanged, breakpointEvent(breakpoint(rev: 10, line: 40)))
        try await waitUntil { debug.info.breakpoints.first?.line == 40 }
        XCTAssertEqual(debug.info.breakpoints.count, 1)
    }

    func testGutterClicksWhileTheChangeIsInFlightDoNothing() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.configure { $0.breakpointReply = self.breakpoint(rev: 1) }
        debug.toggleBreakpoint(url: "app://shop/api.js", line: 25)
        debug.toggleBreakpoint(url: "app://shop/api.js", line: 25)
        try await waitUntil { debug.pendingLines.isEmpty }
        XCTAssertEqual(backend.calls.filter { $0.hasPrefix("bp.set") }, ["bp.set url app://shop/api.js:25"])
    }

    func testRemovingTheSelectedBreakpointClearsTheSelection() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session(breakpoints: [breakpoint(rev: 1)])])
        let debug = try XCTUnwrap(model.selected)
        debug.selectedBreakpointID = "bp-1"
        backend.configure { $0.removeReply = self.breakpoint(rev: 2) }
        debug.removeSelectedBreakpoint()
        try await waitUntil { debug.info.breakpoints.isEmpty }
        XCTAssertNil(debug.selectedBreakpointID)
    }

    // MARK: Variables

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
        XCTAssertEqual(order.path, ["0:local", "order"])
        debug.toggleVariable(order)
        try await waitUntil { debug.variables.rows.contains { $0.title == "total" } }
        XCTAssertEqual(backend.calls.filter { $0 == "variables O1 in \(page)" }.count, 1)
        debug.toggleVariable(order)
        debug.toggleVariable(order)
        XCTAssertEqual(backend.calls.filter { $0 == "variables O1 in \(page)" }.count, 1)

        backend.send(EventKind.debugResumed, resumed(2))
        try await waitUntil { model.phase == .running }
        XCTAssertEqual(debug.variables.rows, [])
        backend.send(EventKind.debugPaused, paused(3))
        try await waitUntil { backend.calls.contains("variables O3 in \(page)") }
    }

    func testAVariablesReplyThatArrivesAfterResumeIsDropped() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.properties["L0-1"] = DebugPropertyPage(properties: [self.object("order", "O1")])
            $0.holdVariables = ["L0-1"]
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { backend.heldVariablesCount == 1 }
        backend.send(EventKind.debugResumed, resumed(2))
        try await waitUntil { model.phase == .running }
        backend.releaseVariables()
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(debug.variables.rows, [])
    }

    func testAVariablesReplyForAnEarlierPauseDoesNotFillTheNewOne() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.properties["L0-1"] = DebugPropertyPage(properties: [self.object("stale", "S1")])
            $0.properties["L0-3"] = DebugPropertyPage(properties: [self.object("fresh", "F3")])
            $0.holdVariables = ["L0-1"]
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { backend.heldVariablesCount == 1 }
        backend.send(EventKind.debugResumed, resumed(2))
        backend.send(EventKind.debugPaused, paused(3))
        try await waitUntil { debug.variables.rows.contains { $0.title == "fresh" } }
        backend.releaseVariables()
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertFalse(debug.variables.rows.contains { $0.title == "stale" })
        XCTAssertEqual(debug.variables.gen, 3)
    }

    func testMoreRowLoadsTheNextPageOnce() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.properties["L0-1"] = DebugPropertyPage(properties: [self.object("a", "A1")], total: 3)
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { debug.variables.rows.contains { $0.kind == .more } }
        backend.configure { $0.holdVariables = ["L0-1"] }
        let more = try XCTUnwrap(debug.variables.rows.first { $0.kind == .more })
        debug.toggleVariable(more)
        debug.toggleVariable(more)
        try await waitUntil { backend.heldVariablesCount == 1 }
        XCTAssertTrue(debug.variables.rows.first { $0.kind == .more }?.busy ?? false)
        backend.releaseVariables()
        XCTAssertEqual(backend.calls.filter { $0 == "variables L0-1 @1 in \(page)" }.count, 1)
    }

    func testKeyboardMovesTheVariableSelectionAndExpandsIt() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure { $0.properties["L0-1"] = DebugPropertyPage(properties: [self.object("order", "O1")]) }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.send(EventKind.debugPaused, paused(1))
        try await waitUntil { debug.variables.rows.contains { $0.title == "order" } }
        debug.moveVariableSelection(by: 1)
        debug.moveVariableSelection(by: 1)
        XCTAssertEqual(debug.selectedVariable?.path, ["0:local", "order"])
        debug.setSelectedVariableExpanded(true)
        try await waitUntil { backend.calls.contains("variables O1 in \(page)") }
        XCTAssertTrue(debug.variables.isExpanded(["0:local", "order"]))
    }

    // MARK: Console

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

    func testAReconnectReloadsTheSessionsAndCatchesUpTheConsole() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure { $0.consolePages = [ConsolePage(entries: [self.entry(1), self.entry(2)], nextCursor: 2)] }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        try await waitUntil { debug.console.entries.count == 2 }
        var moved = session()
        moved.rev = 5
        moved.targets[1].title = "결제 · 다시 연결"
        backend.configure {
            $0.sessions = [moved]
            $0.consolePages = [ConsolePage(entries: [self.entry(3)], nextCursor: 3, gap: true)]
        }
        let lists = backend.calls.filter { $0 == "list" }.count
        backend.reconnect()
        try await waitUntil { backend.calls.filter { $0 == "list" }.count == lists + 1 }
        try await waitUntil { debug.console.entries.map(\.seq) == [1, 2, 3] }
        XCTAssertTrue(backend.calls.contains("console cursor 2"))
        XCTAssertEqual(debug.selectedTarget?.title, "결제 · 다시 연결")
        XCTAssertEqual(debug.console.gaps, [3])
        XCTAssertEqual(backend.subscriptions, 1)
    }

    func testConsoleEventsGatherIntoOneFlushPerFrame() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()],
                                      timings: DebugTimings(consoleFrame: .milliseconds(40), stateRetries: []))
        let debug = try XCTUnwrap(model.selected)
        try await waitUntil { debug.consolePhase == .loaded }
        let before = debug.consoleFlushes
        for seq in 1...50 {
            backend.send(EventKind.debugConsole, ["debug_id": .string("d-1"), "entry": PreviewBackend.payload(entry(seq))])
        }
        try await waitUntil { debug.console.entries.count == 50 }
        XCTAssertLessThanOrEqual(debug.consoleFlushes - before, 2)
    }

    func testClearingTheConsoleEmptiesItEverywhereAndKeepsLaterEntries() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.consolePages = [ConsolePage(entries: [self.entry(10), self.entry(11)], nextCursor: 11, hasEarlier: true, dropped: 4)]
            $0.clearedThrough = 11
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        try await waitUntil { debug.console.entries.count == 2 }
        XCTAssertTrue(debug.console.hasEarlier)
        XCTAssertEqual(debug.console.dropped, 4)
        debug.clearConsole()
        XCTAssertEqual(debug.console.entries, [])
        XCTAssertFalse(debug.console.hasEarlier)
        XCTAssertEqual(debug.console.dropped, 0)
        try await waitUntil { backend.calls.contains("console.clear") }
        backend.send(EventKind.debugConsole, ["debug_id": .string("d-1"), "entry": PreviewBackend.payload(entry(11))])
        backend.send(EventKind.debugConsole, ["debug_id": .string("d-1"), "entry": PreviewBackend.payload(entry(12))])
        try await waitUntil { debug.console.entries.map(\.seq) == [12] }
        backend.send(EventKind.debugConsoleCleared, ["debug_id": .string("d-1"), "through_seq": .int(12)])
        try await waitUntil { debug.console.entries.isEmpty }
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

    func testConsoleResultsExpandAgainstTheTargetTheyRanInAndSurviveSteps() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure {
            $0.evaluation = DebugEvaluation(value: RemoteValue(type: "object", className: "Object", valuePreview: "{total: 1}",
                                                               objectID: "console-1", hasChildren: true))
            $0.properties["console-1"] = DebugPropertyPage(properties: [DebugProperty(name: "total", value: RemoteValue(type: "number", valuePreview: "1"))])
        }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        debug.evaluate("order")
        try await waitUntil { debug.consoleObjects.targetID(of: ["x1"]) == self.page }
        debug.select(target: main)
        backend.send(EventKind.debugPaused, paused(3, target: page))
        backend.send(EventKind.debugResumed, resumed(4, target: page))
        try await waitUntil { model.phase == .running }
        let record = try XCTUnwrap(debug.console.evaluations.first)
        XCTAssertEqual(record.targetID, page)
        let root = VariableTree.Row(kind: .scope, path: ["x1"], depth: 0, title: "order", isExpandable: true, objectID: "console-1",
                                    targetID: record.targetID)
        debug.toggleConsoleObject(root)
        try await waitUntil { debug.consoleObjects.rows(under: ["x1"]).contains { $0.title == "total" } }
        XCTAssertTrue(backend.calls.contains("variables console-1 in \(page)"))
    }

    func testSelectedConsoleRowsCopyInOrder() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure { $0.consolePages = [ConsolePage(entries: [self.entry(1, "하나"), self.entry(2, "둘"), self.entry(3, "셋")], nextCursor: 3)] }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        try await waitUntil { debug.console.entries.count == 3 }
        debug.selectConsoleItem("e3", extending: false, toggling: false)
        debug.selectConsoleItem("e1", extending: false, toggling: true)
        XCTAssertEqual(debug.selectedConsoleText, "하나\n셋")
        debug.selectConsoleItem("e1", extending: false, toggling: false)
        debug.selectConsoleItem("e3", extending: true, toggling: false)
        XCTAssertEqual(debug.selectedConsoleText, "하나\n둘\n셋")
    }

    func testScriptListFailureIsKept() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        await debug.loadScripts()
        XCTAssertEqual(debug.scriptsError?.code, "STALE_TARGET")
    }

    // MARK: Execution

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

    func testCommandAlternatesActOnTheSelectedSessionUnlessTheConsoleHasTheKeyboard() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let window = NSWindow(contentRect: CGRect(x: -32_000, y: -32_000, width: 10, height: 10), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        let other = NSWindow(contentRect: CGRect(x: -32_000, y: -32_000, width: 10, height: 10), styleMask: [.borderless],
                             backing: .buffered, defer: false)
        other.isReleasedWhenClosed = false
        XCTAssertGreaterThan(window.windowNumber, 0)
        func key(_ characters: String, _ flags: NSEvent.ModifierFlags, code: UInt16, in target: NSWindow) throws -> NSEvent {
            try XCTUnwrap(NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: target.windowNumber,
                                           context: nil, characters: characters, charactersIgnoringModifiers: characters, isARepeat: false,
                                           keyCode: code))
        }
        let event = try key("\\", [.command], code: 42, in: window)
        model.consoleFocused = true
        XCTAssertFalse(DebuggerKeyHandler.handle(event, model: model, enabled: true, window: window, keyWindow: window))
        model.consoleFocused = false
        XCTAssertTrue(DebuggerKeyHandler.handle(event, model: model, enabled: true, window: window, keyWindow: window))
        try await waitUntil { backend.calls.contains("pause \(self.page)") }
        XCTAssertTrue(DebuggerKeyHandler.handle(event, model: model, enabled: false, window: window, keyWindow: window))
        XCTAssertFalse(DebuggerKeyHandler.handle(event, model: model, enabled: true, window: nil, keyWindow: window), "no debugger window")
        XCTAssertFalse(DebuggerKeyHandler.handle(event, model: model, enabled: true, window: window, keyWindow: nil), "no key window")
        XCTAssertFalse(DebuggerKeyHandler.handle(event, model: model, enabled: true, window: window, keyWindow: other), "another window is key")
        XCTAssertFalse(DebuggerKeyHandler.handle(try key("\\", [.command], code: 42, in: other), model: model, enabled: true, window: window,
                                                 keyWindow: window), "keys of another window pass through")
        model.showsScriptNavigator = true
        XCTAssertFalse(DebuggerKeyHandler.handle(event, model: model, enabled: true, window: window, keyWindow: window))
        model.showsScriptNavigator = false
        let escape = try key("\u{1B}", [], code: 53, in: window)
        XCTAssertFalse(DebuggerKeyHandler.handle(escape, model: model, enabled: true, window: window, keyWindow: window))
        model.navigatorShown = true
        model.navigatorOverlay = true
        XCTAssertTrue(DebuggerKeyHandler.handle(escape, model: model, enabled: true, window: window, keyWindow: window))
        XCTAssertEqual(model.navigatorShown, false)
    }

    func testSettingsRepliesWithoutASessionDoNotOverwriteANewerSession() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        backend.configure { $0.holdSettings = true }
        debug.setBreakpointsActive(false)
        debug.setExceptionMode(.none)
        try await waitUntil { backend.heldSettingsCount == 2 }
        var newer = session()
        newer.rev = 7
        newer.breakpointsActive = true
        newer.pauseOnExceptions = ExceptionPauseMode.uncaught.rawValue
        backend.send(EventKind.debugSessionChanged, ["debug_id": .string("d-1"), "session": PreviewBackend.payload(newer)])
        try await waitUntil { debug.info.rev == 7 }
        backend.releaseSettings()
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertTrue(debug.info.breakpointsActive)
        XCTAssertEqual(debug.info.exceptionMode, .uncaught)

        debug.setBreakpointsActive(false)
        try await waitUntil { !debug.info.breakpointsActive }
    }

    func testPauseWaitingForCodeIsCancelledByTheToggle() async throws {
        let backend = FakeDebuggerBackend()
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        let pending = DebugTarget(id: page, role: "renderer", type: "page", url: "app://shop/index.html", title: "결제", pausePending: true, gen: 2)
        backend.send(EventKind.debugTargetChanged, ["debug_id": .string("d-1"), "target": PreviewBackend.payload(pending), "removed": .bool(false)])
        try await waitUntil { debug.pausePending }
        debug.togglePause()
        try await waitUntil { backend.calls.contains("resume \(self.page)") }
        XCTAssertFalse(backend.calls.contains("pause \(page)"))
    }

    func testDetachingWithTheInspectorLeftOpenSaysSo() async throws {
        let backend = FakeDebuggerBackend()
        backend.configure { $0.inspectorLeftOpen = true }
        let model = try await started(backend, sessions: [session()])
        let debug = try XCTUnwrap(model.selected)
        await debug.close(keepProcess: true)
        XCTAssertEqual(debug.notice?.tone, .info)
        XCTAssertEqual(debug.notice?.message, "PC의 검사기 포트가 열린 채 남았습니다 · 앱을 다시 시작하면 닫힙니다")
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

    func testDebugMenuShortcutsApplyOnlyWhileTheDebuggerTabShowsAndTheConsoleIsNotTyping() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let defaults = UserDefaults(suiteName: "debugger-menu-\(UUID())")!
        let store = AppStore.seeded(backend: backend, defaults: defaults, hosts: PreviewFixtures.hosts(), selectedHostID: dev)
        store.layout.tab = .files
        XCTAssertFalse(DebuggerCommandContext(store: store).isShown)
        XCTAssertNil(DebuggerCommandContext(store: store).session)
        store.layout.tab = .debugger
        XCTAssertTrue(DebuggerCommandContext(store: store).isShown)
        XCTAssertTrue(DebuggerCommandContext(store: store).canStart)
        let host = try XCTUnwrap(store.host(dev))
        let model = store.debugger(for: host)
        let error = await model.launch(DebugLaunchRequest(hostID: dev, path: PreviewDebugProgram.appPath))
        XCTAssertNil(error)
        XCTAssertTrue(DebuggerCommandContext(store: store).canControl)
        model.consoleFocused = true
        XCTAssertFalse(DebuggerCommandContext(store: store).canControl)
        model.consoleFocused = false
        store.selectedHostID = PreviewFixtures.qaLaptopID
        XCTAssertFalse(DebuggerCommandContext(store: store).canStart, "the QA laptop's agent cannot open tunnels")
        XCTAssertEqual(FunctionKey.f8.character, Character(UnicodeScalar(UInt32(NSF8FunctionKey))!))
        XCTAssertEqual(FunctionKey.f11.character, Character(UnicodeScalar(UInt32(NSF11FunctionKey))!))
    }
}
