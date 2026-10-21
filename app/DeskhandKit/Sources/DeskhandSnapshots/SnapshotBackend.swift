import DeskhandClient
import DeskhandUI
import Foundation
import struct DeskhandClient.Host

/// `Backend` for snapshot scenes. Control and timeline reads return exactly what the scene
/// seeded; a timeline can stay pending or fail, and control changes can fail. File changes,
/// transfers, terminals and process kills do nothing; scenes seed those tabs. Everything else
/// goes to a `PreviewBackend`.
final class SnapshotBackend: Backend {
    enum TimelineReply: Sendable {
        case entries([TimelineEntry])
        case failure(OperationError)
        /// Never answers while the scene renders.
        case pending
    }

    private let base = PreviewBackend(scenario: .standard, stepDelay: .zero)
    private let controls: [String: ControlState]
    private let timelines: [String: TimelineReply]
    private let controlFailure: OperationError?
    private let daemonVersion: String?

    /// `daemonVersion` is the version `daemonInfo` reports; nil for a daemon that never answered.
    init(controls: [String: ControlState], timelines: [String: TimelineReply], controlFailure: OperationError? = nil,
         daemonVersion: String? = "0.1.0") {
        self.controls = controls
        self.timelines = timelines
        self.controlFailure = controlFailure
        self.daemonVersion = daemonVersion
    }

    func start() async throws {}
    func daemonInfo() async -> DaemonInfo { DaemonInfo(version: daemonVersion, apiVersion: daemonVersion == nil ? nil : 1) }
    func restartDaemon() async throws {}
    func hostList() async throws -> [Host] { try await base.hostList() }
    func hostAdd(name: String, ssh: String) async throws -> JobReceipt { try await base.hostAdd(name: name, ssh: ssh) }
    func hostRemove(hostID: String, uninstall: Bool) async throws {}
    func hostDoctor(hostID: String) async throws -> [DoctorCheck] { PreviewFixtures.doctor(hostID: hostID) }
    func hostConnect(hostID: String) async throws {}
    func hostDisconnect(hostID: String) async throws {}
    func hostRepair(hostID: String) async throws -> JobReceipt { JobReceipt(jobID: nil, status: .running) }
    func sessionInspect(hostID: String) async throws -> SessionInspect { try await base.sessionInspect(hostID: hostID) }
    func sshAliases() async -> [String] { PreviewFixtures.sshAliases }

    func controlGet(hostID: String) async throws -> ControlState {
        controls[hostID] ?? ControlState(owner: "none", gen: 0, aiPaused: false)
    }

    func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        try changeControl(hostID)
    }

    func controlRelease(hostID: String) async throws {
        _ = try changeControl(hostID)
    }

    func controlStop(hostID: String) async throws -> ControlState {
        try changeControl(hostID)
    }

    func controlResumeAI(hostID: String) async throws -> ControlState {
        try changeControl(hostID)
    }

    /// Fails with `controlFailure` when set; otherwise leaves the seeded control unchanged.
    private func changeControl(_ hostID: String) throws -> ControlState {
        if let controlFailure { throw controlFailure }
        return controls[hostID] ?? ControlState(owner: "none", gen: 0, aiPaused: false)
    }

    func timelineList(hostID: String, since: Int?, limit: Int, cursor: String?) async throws -> TimelinePage {
        switch timelines[hostID] ?? .entries([]) {
        case .entries(let entries):
            return TimelinePage(entries: entries)
        case .failure(let error):
            throw error
        case .pending:
            try await Task.sleep(for: .seconds(3600))
            return TimelinePage(entries: [])
        }
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        AsyncThrowingStream { _ in }
    }

    func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        try await base.filesList(hostID: hostID, path: path, cursor: cursor, limit: limit)
    }

    func filesStat(hostID: String, path: String) async throws -> FileEntry { try await base.filesStat(hostID: hostID, path: path) }
    func filesMkdir(hostID: String, path: String) async throws {}
    func filesRename(hostID: String, from: String, to: String) async throws {}
    func filesTrash(hostID: String, path: String) async throws {}
    func filesDelete(hostID: String, path: String) async throws {}
    func appLaunch(hostID: String, path: String, args: [String]?, cwd: String?) async throws -> Int? { nil }
    func transferStart(_ request: TransferRequest) async throws -> String { throw ClientError.notConnected }
    func transferList(hostID: String) async throws -> [Transfer] { [] }
    func transferGet(transferID: String) async throws -> Transfer { throw ClientError.notConnected }
    func transferPause(transferID: String) async throws -> Transfer { throw ClientError.notConnected }
    func transferResume(transferID: String) async throws -> Transfer { throw ClientError.notConnected }
    func transferCancel(transferID: String) async throws -> Transfer { throw ClientError.notConnected }

    /// Throws; scenes draw the screen with `ScreenStandIn`.
    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        throw ClientError.notConnected
    }

    /// Terminal calls throw or return nothing; scenes seed the terminal tab with stand-in sessions.
    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        throw ClientError.notConnected
    }

    func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream { throw ClientError.notConnected }
    func ptyJobs(hostID: String) async throws -> [Job] { [] }
    func ptyKill(jobID: String) async throws {}

    /// Process and diagnostics reads come from the preview daemon; scenes seed those tabs, and
    /// a kill changes nothing.
    func procList(hostID: String) async throws -> [RemoteProcess] { try await base.procList(hostID: hostID) }
    func procKill(hostID: String, pid: Int, started: Timestamp?, tree: Bool) async throws -> KillReceipt {
        KillReceipt(status: .succeeded, killed: 1)
    }
    func diagSystem(hostID: String) async throws -> DiagSystem { try await base.diagSystem(hostID: hostID) }
    func diagEvents(_ query: EventLogQuery) async throws -> EventLogPage { try await base.diagEvents(query) }
    func diagCrashes(hostID: String, since: Date?) async throws -> [CrashDump] { try await base.diagCrashes(hostID: hostID, since: since) }
}

/// Debugger calls change nothing and read nothing; scenes seed the debugger tab.
extension SnapshotBackend {
    func debugList(hostID: String) async throws -> [DebugSession] { [] }
    func debugGet(debugID: String) async throws -> DebugSession { throw ClientError.notConnected }
    func debugLaunch(_ request: DebugLaunchRequest) async throws -> DebugSession { throw ClientError.notConnected }
    func debugAttach(_ request: DebugAttachRequest) async throws -> DebugSession { throw ClientError.notConnected }
    func debugTargetAttach(debugID: String, targetID: String) async throws -> DebugTarget { throw ClientError.notConnected }
    func debugBreakpointSet(debugID: String, spec: BreakpointSpec) async throws -> DebugBreakpoint { throw ClientError.notConnected }
    func debugBreakpointUpdate(debugID: String, breakpointID: String, enabled: Bool?, condition: String?) async throws -> DebugBreakpoint {
        throw ClientError.notConnected
    }
    func debugBreakpointRemove(debugID: String, breakpointID: String) async throws -> DebugBreakpoint? { nil }
    func debugBreakpointsActivate(debugID: String, active: Bool) async throws -> DebugSession? { nil }
    func debugExceptions(debugID: String, mode: ExceptionPauseMode) async throws -> DebugSession? { nil }
    func debugPause(debugID: String, targetID: String) async throws -> DebugTarget? { nil }
    func debugResume(debugID: String, targetID: String) async throws -> DebugTarget? { nil }
    func debugStep(debugID: String, targetID: String, kind: DebugStepKind) async throws -> DebugTarget? { nil }
    func debugState(debugID: String, targetID: String) async throws -> DebugPauseState { throw ClientError.notConnected }
    func debugVariables(debugID: String, targetID: String, objectID: String, offset: Int, limit: Int) async throws -> DebugPropertyPage {
        throw ClientError.notConnected
    }
    func debugEvaluate(debugID: String, targetID: String, expression: String, frameID: String?) async throws -> DebugEvaluation {
        throw ClientError.notConnected
    }
    func debugScripts(debugID: String, targetID: String?) async throws -> [DebugScript] { throw ClientError.notConnected }
    func debugSource(debugID: String, targetID: String, scriptID: String) async throws -> DebugSource { throw ClientError.notConnected }
    func debugConsole(_ query: ConsoleQuery) async throws -> ConsolePage { ConsolePage(entries: [], nextCursor: query.cursor ?? 0) }
    func debugConsoleClear(debugID: String) async throws -> Int { 0 }
    func debugClose(debugID: String, keepProcess: Bool) async throws -> DebugCloseResult { DebugCloseResult() }
}

/// QA reads come from the preview daemon's sample runs and evidence; scenes seed the QA tab, and
/// starting, cancelling and exporting change nothing.
extension SnapshotBackend {
    func qaRun(_ request: QARunRequest) async throws -> QARun { throw ClientError.notConnected }
    func qaGet(runID: String) async throws -> QARun { try await base.qaGet(runID: runID) }
    func qaList(hostID: String?, limit: Int, cursor: String?) async throws -> QARunPage {
        try await base.qaList(hostID: hostID, limit: limit, cursor: cursor)
    }
    func qaCancel(runID: String) async throws -> QARun { try await base.qaGet(runID: runID) }
    func artifactsExport(jobID: String, dir: String) async throws -> ArtifactExport { throw ClientError.notConnected }
    func evidenceData(path: String, maxBytes: Int) async throws -> Data { try await base.evidenceData(path: path, maxBytes: maxBytes) }
}
