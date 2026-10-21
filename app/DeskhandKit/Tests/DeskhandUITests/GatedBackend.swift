import DeskhandClient
import Foundation
import Synchronization
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// One `control.*` call as `GatedBackend` received it.
struct ControlCall: Equatable {
    let method: String
    let hostID: String
    var owner: ControlOwner?
    var force: Bool?
}

/// Backend that forwards to a `PreviewBackend`, counts `hostList` calls, can hold `hostList`
/// replies until the test resumes them in any order, can fail `timelineList`, records, holds or
/// fails `control.*` changes, counts or fails `procList`, records, answers or fails `procKill`,
/// records or holds `diagEvents`, and counts, holds or fails `diagSystem`.
final class GatedBackend: Backend, LocalOverlayProviding {
    let base: PreviewBackend

    private struct HeldControl: Hashable {
        let method: String
        let afterApplying: Bool
    }

    private struct State {
        var holdHostList = false
        var pendingHostLists: [CheckedContinuation<[Host], Error>] = []
        var hostListCalls = 0
        var timelineFailure: OperationError?
        var controlCalls: [ControlCall] = []
        var controlFailures: [String: OperationError] = [:]
        var heldControls: Set<HeldControl> = []
        var pendingControls: [(method: String, continuation: CheckedContinuation<Void, Never>)] = []
        var filesListCalls: [FilesListCall] = []
        var onePageListing = false
        var transferRequests: [TransferRequest] = []
        var transferGets: [String] = []
        var fileCalls: [FileCall] = []
        var procListCalls = 0
        var procListFailure: OperationError?
        var killCalls: [KillCall] = []
        var killReceipt: KillReceipt?
        var killFailure: (any Error & Sendable)?
        var systemCalls = 0
        var systemFailure: OperationError?
        var holdSystem = false
        var heldSystems: [CheckedContinuation<Void, Never>] = []
        var eventQueries: [EventLogQuery] = []
        var heldEventQueries: [CheckedContinuation<Void, Never>] = []
        var holdEvents = false
        var restartFailure: (any Error & Sendable)?
        var restartCalls = 0
        var hostRemoveFailure: OperationError?
    }

    private let state = Mutex(State())

    init(_ base: PreviewBackend) {
        self.base = base
    }

    var localOverlay: LocalOverlay { base.localOverlay }

    func holdHostList(_ hold: Bool) {
        state.withLock { $0.holdHostList = hold }
    }

    var pendingHostListCount: Int { state.withLock { $0.pendingHostLists.count } }
    var hostListCalls: Int { state.withLock { $0.hostListCalls } }

    /// Replies to the held `hostList` call at `index` (in call order among those still held).
    func resumeHostList(at index: Int, with result: Result<[Host], Error>) {
        let continuation = state.withLock { $0.pendingHostLists.remove(at: index) }
        continuation.resume(with: result)
    }

    var filesListCalls: [FilesListCall] { state.withLock { $0.filesListCalls } }
    var transferRequests: [TransferRequest] { state.withLock { $0.transferRequests } }
    /// Transfer IDs of the `transfer.get` calls, in order.
    var transferGets: [String] { state.withLock { $0.transferGets } }
    var fileCalls: [FileCall] { state.withLock { $0.fileCalls } }

    /// Answers `filesList` with the whole folder in one page, as `files.list` without paging does.
    func listInOnePage(_ onePage: Bool) {
        state.withLock { $0.onePageListing = onePage }
    }

    func failTimeline(with error: OperationError?) {
        state.withLock { $0.timelineFailure = error }
    }

    var controlCalls: [ControlCall] { state.withLock { $0.controlCalls } }

    /// Makes `method` (for example `control.stop`) throw `error`, or succeed again when nil.
    func failControl(_ method: String, with error: OperationError?) {
        state.withLock { $0.controlFailures[method] = error }
    }

    /// Holds calls of `method` until `releaseControl(_:)`: before they reach the preview
    /// daemon, or with `afterApplying`, after it applied them and before the reply returns.
    func holdControl(_ method: String, afterApplying: Bool = false) {
        state.withLock { _ = $0.heldControls.insert(HeldControl(method: method, afterApplying: afterApplying)) }
    }

    var pendingControlCount: Int { state.withLock { $0.pendingControls.count } }

    /// Stops holding `method` and lets its held calls continue.
    func releaseControl(_ method: String) {
        let released = state.withLock { s -> [CheckedContinuation<Void, Never>] in
            s.heldControls = s.heldControls.filter { $0.method != method }
            let matching = s.pendingControls.filter { $0.method == method }.map(\.continuation)
            s.pendingControls.removeAll { $0.method == method }
            return matching
        }
        for continuation in released { continuation.resume() }
    }

    private func gate(_ method: String, afterApplying: Bool) async {
        let hold = state.withLock { $0.heldControls.contains(HeldControl(method: method, afterApplying: afterApplying)) }
        guard hold else { return }
        await withCheckedContinuation { continuation in
            state.withLock { $0.pendingControls.append((method, continuation)) }
        }
    }

    /// Records `call`, applies the hold and failure configured for its method, and runs `apply`.
    private func control<T>(_ call: ControlCall, _ apply: () async throws -> T) async throws -> T {
        state.withLock { $0.controlCalls.append(call) }
        await gate(call.method, afterApplying: false)
        if let failure = state.withLock({ $0.controlFailures[call.method] }) { throw failure }
        let result = try await apply()
        await gate(call.method, afterApplying: true)
        return result
    }

    func start() async throws { try await base.start() }
    func daemonInfo() async -> DaemonInfo { await base.daemonInfo() }
    func restartDaemon() async throws {
        let failure = state.withLock { s -> (any Error & Sendable)? in
            s.restartCalls += 1
            return s.restartFailure
        }
        if let failure { throw failure }
        try await base.restartDaemon()
    }

    var restartCalls: Int { state.withLock { $0.restartCalls } }

    /// Makes `restartDaemon` throw `error`, or restart the preview daemon again when nil.
    func failRestart(with error: (any Error & Sendable)?) {
        state.withLock { $0.restartFailure = error }
    }

    /// Makes `hostRemove` throw `error`, or remove again when nil.
    func failHostRemove(with error: OperationError?) {
        state.withLock { $0.hostRemoveFailure = error }
    }

    func hostList() async throws -> [Host] {
        let hold = state.withLock { s -> Bool in
            s.hostListCalls += 1
            return s.holdHostList
        }
        guard hold else { return try await base.hostList() }
        return try await withCheckedThrowingContinuation { continuation in
            state.withLock { $0.pendingHostLists.append(continuation) }
        }
    }

    func hostAdd(name: String, ssh: String) async throws -> JobReceipt { try await base.hostAdd(name: name, ssh: ssh) }
    func hostRemove(hostID: String, uninstall: Bool) async throws {
        if let failure = state.withLock({ $0.hostRemoveFailure }) { throw failure }
        try await base.hostRemove(hostID: hostID, uninstall: uninstall)
    }
    func hostDoctor(hostID: String) async throws -> [DoctorCheck] { try await base.hostDoctor(hostID: hostID) }
    func hostConnect(hostID: String) async throws { try await base.hostConnect(hostID: hostID) }
    func hostDisconnect(hostID: String) async throws { try await base.hostDisconnect(hostID: hostID) }
    func hostRepair(hostID: String) async throws -> JobReceipt { try await base.hostRepair(hostID: hostID) }
    func sessionInspect(hostID: String) async throws -> SessionInspect { try await base.sessionInspect(hostID: hostID) }
    func sshAliases() async -> [String] { await base.sshAliases() }
    /// Reads the preview daemon's state, then waits while `control.get` is held with `afterApplying`.
    func controlGet(hostID: String) async throws -> ControlState {
        if let failure = state.withLock({ $0.controlFailures["control.get"] }) { throw failure }
        let control = try await base.controlGet(hostID: hostID)
        await gate("control.get", afterApplying: true)
        return control
    }
    func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        try await control(ControlCall(method: "control.acquire", hostID: hostID, owner: owner, force: force)) {
            try await base.controlAcquire(hostID: hostID, as: owner, force: force)
        }
    }
    func controlRelease(hostID: String) async throws {
        try await control(ControlCall(method: "control.release", hostID: hostID)) {
            try await base.controlRelease(hostID: hostID)
        }
    }
    func controlStop(hostID: String) async throws -> ControlState {
        try await control(ControlCall(method: "control.stop", hostID: hostID)) {
            try await base.controlStop(hostID: hostID)
        }
    }
    func controlResumeAI(hostID: String) async throws -> ControlState {
        try await control(ControlCall(method: "control.resume_ai", hostID: hostID)) {
            try await base.controlResumeAI(hostID: hostID)
        }
    }

    func timelineList(hostID: String, since: Int?, limit: Int, cursor: String?) async throws -> TimelinePage {
        if let failure = state.withLock({ $0.timelineFailure }) { throw failure }
        return try await base.timelineList(hostID: hostID, since: since, limit: limit, cursor: cursor)
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        base.events(kinds: kinds, hostID: hostID)
    }

    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        try await base.screenAttach(hostID: hostID, display: display, preset: preset)
    }

    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        try await base.ptyOpen(hostID: hostID, shell: shell, cols: cols, rows: rows)
    }

    func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream { try await base.ptyAttach(target) }
    func ptyJobs(hostID: String) async throws -> [Job] { try await base.ptyJobs(hostID: hostID) }
    func ptyKill(jobID: String) async throws { try await base.ptyKill(jobID: jobID) }

    func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        let onePage = state.withLock { s -> Bool in
            s.filesListCalls.append(FilesListCall(path: path, cursor: cursor, limit: limit))
            return s.onePageListing
        }
        guard onePage else { return try await base.filesList(hostID: hostID, path: path, cursor: cursor, limit: limit) }
        let all = try await base.filesList(hostID: hostID, path: path, cursor: nil, limit: Int.max)
        return FileEntryPage(entries: all.entries, total: all.entries.count)
    }

    func filesStat(hostID: String, path: String) async throws -> FileStat { try await base.filesStat(hostID: hostID, path: path) }

    func filesMkdir(hostID: String, path: String) async throws {
        record(FileCall(method: "files.mkdir", path: path))
        try await base.filesMkdir(hostID: hostID, path: path)
    }

    func filesRename(hostID: String, from: String, to: String) async throws {
        record(FileCall(method: "files.rename", path: from, to: to))
        try await base.filesRename(hostID: hostID, from: from, to: to)
    }

    func filesTrash(hostID: String, path: String) async throws {
        record(FileCall(method: "files.trash", path: path))
        try await base.filesTrash(hostID: hostID, path: path)
    }

    func filesDelete(hostID: String, path: String) async throws {
        record(FileCall(method: "files.delete", path: path))
        try await base.filesDelete(hostID: hostID, path: path)
    }

    func appLaunch(hostID: String, path: String, args: [String]?, cwd: String?) async throws -> Int? {
        record(FileCall(method: "app.launch", path: path))
        return try await base.appLaunch(hostID: hostID, path: path, args: args, cwd: cwd)
    }

    func transferStart(_ request: TransferRequest) async throws -> TransferStarted {
        state.withLock { $0.transferRequests.append(request) }
        return try await base.transferStart(request)
    }

    func transferList(hostID: String) async throws -> [Transfer] { try await base.transferList(hostID: hostID) }
    func transferGet(transferID: String) async throws -> TransferDetail {
        state.withLock { $0.transferGets.append(transferID) }
        return try await base.transferGet(transferID: transferID)
    }
    func transferPause(transferID: String) async throws -> TransferDetail { try await base.transferPause(transferID: transferID) }
    func transferResume(transferID: String) async throws -> TransferDetail { try await base.transferResume(transferID: transferID) }
    func transferCancel(transferID: String) async throws -> TransferDetail { try await base.transferCancel(transferID: transferID) }

    var procListCalls: Int { state.withLock { $0.procListCalls } }
    var killCalls: [KillCall] { state.withLock { $0.killCalls } }
    var eventQueries: [EventLogQuery] { state.withLock { $0.eventQueries } }
    var pendingEventQueryCount: Int { state.withLock { $0.heldEventQueries.count } }

    /// Makes `procList` throw `error`, or answer again when nil.
    func failProcList(with error: OperationError?) {
        state.withLock { $0.procListFailure = error }
    }

    /// Answers `procKill` with `receipt` without asking the preview daemon, or forwards again when nil.
    func answerKill(with receipt: KillReceipt?) {
        state.withLock { $0.killReceipt = receipt }
    }

    /// Makes `procKill` throw `error`, or answer again when nil.
    func failKill(with error: (any Error & Sendable)?) {
        state.withLock { $0.killFailure = error }
    }

    var systemCalls: Int { state.withLock { $0.systemCalls } }
    var pendingSystemCount: Int { state.withLock { $0.heldSystems.count } }

    /// Makes `diagSystem` throw `error`, or answer again when nil.
    func failSystem(with error: OperationError?) {
        state.withLock { $0.systemFailure = error }
    }

    /// Holds `diagSystem` calls until `releaseSystem()`.
    func holdSystem(_ hold: Bool) {
        state.withLock { $0.holdSystem = hold }
    }

    /// Lets every held `diagSystem` call continue.
    func releaseSystem() {
        let held = state.withLock { s -> [CheckedContinuation<Void, Never>] in
            defer { s.heldSystems = [] }
            return s.heldSystems
        }
        for continuation in held { continuation.resume() }
    }

    /// Holds `diagEvents` calls until `releaseEventQuery()`.
    func holdEventQueries(_ hold: Bool) {
        state.withLock { $0.holdEvents = hold }
    }

    /// Lets the oldest held `diagEvents` call reach the preview daemon.
    func releaseEventQuery() {
        let continuation = state.withLock { $0.heldEventQueries.isEmpty ? nil : $0.heldEventQueries.removeFirst() }
        continuation?.resume()
    }

    func procList(hostID: String) async throws -> [RemoteProcess] {
        let failure = state.withLock { s -> (any Error & Sendable)? in
            s.procListCalls += 1
            return s.procListFailure
        }
        if let failure { throw failure }
        return try await base.procList(hostID: hostID)
    }

    func procKill(hostID: String, pid: Int, started: Timestamp?, tree: Bool) async throws -> KillReceipt {
        let (receipt, failure) = state.withLock { s -> (KillReceipt?, (any Error & Sendable)?) in
            s.killCalls.append(KillCall(hostID: hostID, pid: pid, started: started, tree: tree))
            return (s.killReceipt, s.killFailure)
        }
        if let failure { throw failure }
        if let receipt { return receipt }
        return try await base.procKill(hostID: hostID, pid: pid, started: started, tree: tree)
    }

    func diagSystem(hostID: String) async throws -> DiagSystem {
        let (hold, failure) = state.withLock { s -> (Bool, OperationError?) in
            s.systemCalls += 1
            return (s.holdSystem, s.systemFailure)
        }
        if hold {
            await withCheckedContinuation { continuation in
                state.withLock { $0.heldSystems.append(continuation) }
            }
        }
        if let failure { throw failure }
        return try await base.diagSystem(hostID: hostID)
    }

    func diagEvents(_ query: EventLogQuery) async throws -> EventLogPage {
        let hold = state.withLock { s -> Bool in
            s.eventQueries.append(query)
            return s.holdEvents
        }
        if hold {
            await withCheckedContinuation { continuation in
                state.withLock { $0.heldEventQueries.append(continuation) }
            }
        }
        return try await base.diagEvents(query)
    }

    func diagCrashes(hostID: String, since: Date?) async throws -> [CrashDump] { try await base.diagCrashes(hostID: hostID, since: since) }

    private func record(_ call: FileCall) {
        state.withLock { $0.fileCalls.append(call) }
    }
}

/// One `filesList` call as `GatedBackend` received it.
/// One `proc.kill` call as `GatedBackend` received it.
struct KillCall: Equatable {
    let hostID: String
    let pid: Int
    let started: Timestamp?
    let tree: Bool
}

struct FilesListCall: Equatable {
    let path: String
    let cursor: String?
    let limit: Int
}

/// One state-changing `files.*` or `app.launch` call.
struct FileCall: Equatable {
    let method: String
    let path: String
    var to: String?
}

/// Polls `condition` on the main actor until it holds or `timeout` passes.
@MainActor
func waitUntil(timeout: Duration = .seconds(3), _ condition: @MainActor () -> Bool,
               file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = ContinuousClock.now + timeout
    while !condition() {
        if ContinuousClock.now > deadline {
            return XCTFail("condition not met within \(timeout)", file: file, line: line)
        }
        try await Task.sleep(for: .milliseconds(5))
    }
}

/// Debugger calls go to the preview daemon.
extension GatedBackend {
    func debugList(hostID: String) async throws -> [DebugSession] { try await base.debugList(hostID: hostID) }
    func debugGet(debugID: String) async throws -> DebugSession { try await base.debugGet(debugID: debugID) }
    func debugLaunch(_ request: DebugLaunchRequest) async throws -> DebugSession { try await base.debugLaunch(request) }
    func debugAttach(_ request: DebugAttachRequest) async throws -> DebugSession { try await base.debugAttach(request) }
    func debugTargetAttach(debugID: String, targetID: String) async throws -> DebugTarget {
        try await base.debugTargetAttach(debugID: debugID, targetID: targetID)
    }
    func debugBreakpointSet(debugID: String, spec: BreakpointSpec) async throws -> DebugBreakpoint {
        try await base.debugBreakpointSet(debugID: debugID, spec: spec)
    }
    func debugBreakpointUpdate(debugID: String, breakpointID: String, enabled: Bool?, condition: String?) async throws -> DebugBreakpoint {
        try await base.debugBreakpointUpdate(debugID: debugID, breakpointID: breakpointID, enabled: enabled, condition: condition)
    }
    func debugBreakpointRemove(debugID: String, breakpointID: String) async throws -> DebugBreakpoint? {
        try await base.debugBreakpointRemove(debugID: debugID, breakpointID: breakpointID)
    }
    func debugBreakpointsActivate(debugID: String, active: Bool) async throws -> DebugSession? {
        try await base.debugBreakpointsActivate(debugID: debugID, active: active)
    }
    func debugExceptions(debugID: String, mode: ExceptionPauseMode) async throws -> DebugSession? {
        try await base.debugExceptions(debugID: debugID, mode: mode)
    }
    func debugPause(debugID: String, targetID: String) async throws -> DebugTarget? { try await base.debugPause(debugID: debugID, targetID: targetID) }
    func debugResume(debugID: String, targetID: String) async throws -> DebugTarget? { try await base.debugResume(debugID: debugID, targetID: targetID) }
    func debugStep(debugID: String, targetID: String, kind: DebugStepKind) async throws -> DebugTarget? {
        try await base.debugStep(debugID: debugID, targetID: targetID, kind: kind)
    }
    func debugState(debugID: String, targetID: String) async throws -> DebugPauseState { try await base.debugState(debugID: debugID, targetID: targetID) }
    func debugVariables(debugID: String, targetID: String, objectID: String, offset: Int, limit: Int) async throws -> DebugPropertyPage {
        try await base.debugVariables(debugID: debugID, targetID: targetID, objectID: objectID, offset: offset, limit: limit)
    }
    func debugEvaluate(debugID: String, targetID: String, expression: String, frameID: String?) async throws -> DebugEvaluation {
        try await base.debugEvaluate(debugID: debugID, targetID: targetID, expression: expression, frameID: frameID)
    }
    func debugScripts(debugID: String, targetID: String?) async throws -> [DebugScript] {
        try await base.debugScripts(debugID: debugID, targetID: targetID)
    }
    func debugSource(debugID: String, targetID: String, scriptID: String) async throws -> DebugSource {
        try await base.debugSource(debugID: debugID, targetID: targetID, scriptID: scriptID)
    }
    func debugConsole(_ query: ConsoleQuery) async throws -> ConsolePage { try await base.debugConsole(query) }
    func debugConsoleClear(debugID: String) async throws -> Int { try await base.debugConsoleClear(debugID: debugID) }
    func debugClose(debugID: String, keepProcess: Bool) async throws -> DebugCloseResult {
        try await base.debugClose(debugID: debugID, keepProcess: keepProcess)
    }
}

extension GatedBackend {
    func qaRun(_ request: QARunRequest) async throws -> QARun { try await base.qaRun(request) }
    func qaGet(runID: String) async throws -> QARun { try await base.qaGet(runID: runID) }
    func qaList(hostID: String?, limit: Int, cursor: String?) async throws -> QARunPage {
        try await base.qaList(hostID: hostID, limit: limit, cursor: cursor)
    }
    func qaCancel(runID: String) async throws -> QARun { try await base.qaCancel(runID: runID) }
    func artifactsExport(jobID: String, dir: String) async throws -> ArtifactExport { try await base.artifactsExport(jobID: jobID, dir: dir) }
    func evidenceData(path: String, maxBytes: Int) async throws -> Data { try await base.evidenceData(path: path, maxBytes: maxBytes) }
}
