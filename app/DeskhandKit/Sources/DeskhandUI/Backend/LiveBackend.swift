import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// `Backend` over the daemon's local API socket.
public final class LiveBackend: Backend {
    public let client: DeskhandClient
    private let launcher: DaemonLauncher
    private let clientVersion: String
    private let sshConfig: URL

    public init(
        client: DeskhandClient = DeskhandClient(),
        launcher: DaemonLauncher = DaemonLauncher(),
        clientVersion: String = LiveBackend.bundleVersion,
        sshConfig: URL = SSHConfigReader.defaultConfig
    ) {
        self.client = client
        self.launcher = launcher
        self.clientVersion = clientVersion
        self.sshConfig = sshConfig
    }

    /// `CFBundleShortVersionString` of the running app, or `0.0.0` outside a bundle.
    public static var bundleVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.0.0"
    }

    /// Call limits of methods that wait on long work in the daemon; other calls use the client's limit.
    enum CallLimit {
        /// `host.add` and `host.repair`.
        static let install: Duration = .seconds(600)
        /// `host.remove` and `host.doctor`.
        static let ssh: Duration = .seconds(120)
        /// `files.trash`, `files.delete` and `artifacts.export`.
        static let bulkFiles: Duration = .seconds(300)
        /// `diag.events`.
        static let eventLog: Duration = .seconds(90)
        /// `debug.electron.launch` and `debug.attach`.
        static let debugStart: Duration = .seconds(90)
        /// `debug.evaluate`.
        static let evaluate: Duration = .seconds(60)
    }

    /// Decodes a result value that `DeskhandClient.call` returned as `JSONValue`.
    static func decode<T: Decodable>(_ type: T.Type, from value: JSONValue) throws -> T {
        let data = try JSONEncoder().encode(value)
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(type, from: data)
    }
}

// MARK: Lifecycle

extension LiveBackend {
    public func start() async throws {
        if await client.clientID != nil { return }
        try await launcher.ensureRunning()
        try await client.connect(kind: "app", name: "Deskhand", version: clientVersion)
    }
}

// MARK: Daemon

extension LiveBackend {
    public func daemonInfo() async -> DaemonInfo {
        DaemonInfo(version: await client.daemonVersion, apiVersion: await client.apiVersion, socketPath: client.socketPath)
    }

    public func restartDaemon() async throws {
        try await launcher.restart()
    }
}

// MARK: Hosts

private struct HostIDParams: Encodable {
    let hostID: String
}

private struct HostAddParams: Encodable {
    let name: String
    let ssh: String
    let force: Bool
}

private struct HostRemoveParams: Encodable {
    let hostID: String
    let uninstall: Bool
}

private struct HostListResult: Decodable {
    let hosts: [Host]
}

private struct DoctorResult: Decodable {
    let checks: [DoctorCheck]
}

extension JobReceipt {
    init(_ envelope: Envelope<Empty>) {
        self.init(jobID: envelope.jobID, hostID: envelope.hostID, status: envelope.status,
                  error: envelope.error, nextAction: envelope.nextAction)
    }
}

extension LiveBackend {
    public func hostList() async throws -> [Host] {
        let result: HostListResult = try await client.call("host.list", NoParams())
        return result.hosts
    }

    public func hostAdd(name: String, ssh: String, force: Bool) async throws -> JobReceipt {
        let envelope: Envelope<Empty> = try await client.call("host.add", HostAddParams(name: name, ssh: ssh, force: force), timeout: CallLimit.install)
        return JobReceipt(envelope)
    }

    public func hostRemove(hostID: String, uninstall: Bool) async throws {
        let _: Empty = try await client.call("host.remove", HostRemoveParams(hostID: hostID, uninstall: uninstall), timeout: CallLimit.ssh)
    }

    public func hostDoctor(hostID: String) async throws -> [DoctorCheck] {
        let result: DoctorResult = try await client.call("host.doctor", HostIDParams(hostID: hostID), timeout: CallLimit.ssh)
        return result.checks
    }

    public func hostConnect(hostID: String) async throws {
        let _: Empty = try await client.call("host.connect", HostIDParams(hostID: hostID))
    }

    public func hostDisconnect(hostID: String) async throws {
        let _: Empty = try await client.call("host.disconnect", HostIDParams(hostID: hostID))
    }

    public func hostRepair(hostID: String) async throws -> JobReceipt {
        let envelope: Envelope<Empty> = try await client.call("host.repair", HostIDParams(hostID: hostID), timeout: CallLimit.install)
        return JobReceipt(envelope)
    }

    public func sessionInspect(hostID: String) async throws -> SessionInspect {
        try await client.call("session.inspect", HostIDParams(hostID: hostID))
    }

    public func sshAliases() async -> [String] {
        SSHConfigReader.hosts(from: sshConfig)
    }
}

// MARK: Control

private struct ControlAcquireParams: Encodable {
    let hostID: String
    let `as`: String
    let force: Bool
}

extension LiveBackend {
    public func controlGet(hostID: String) async throws -> ControlState {
        try await client.call("control.get", HostIDParams(hostID: hostID))
    }

    public func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        try await controlChange("control.acquire", hostID: hostID,
                                params: ControlAcquireParams(hostID: hostID, as: owner.rawValue, force: force))
    }

    public func controlRelease(hostID: String) async throws {
        let _: Empty = try await client.call("control.release", HostIDParams(hostID: hostID))
    }

    public func controlStop(hostID: String) async throws -> ControlState {
        try await controlChange("control.stop", hostID: hostID, params: HostIDParams(hostID: hostID))
    }

    public func controlResumeAI(hostID: String) async throws -> ControlState {
        try await controlChange("control.resume_ai", hostID: hostID, params: HostIDParams(hostID: hostID))
    }

    /// Calls a control method and returns the control state from its result, or from
    /// `control.get` when the result does not carry the full state.
    private func controlChange(_ method: String, hostID: String, params: some Encodable & Sendable) async throws -> ControlState {
        let value: JSONValue = try await client.call(method, params)
        if let state = try? Self.decode(ControlState.self, from: value) { return state }
        return try await controlGet(hostID: hostID)
    }
}

// MARK: Timeline

private struct TimelineParams: Encodable {
    let hostID: String
    /// RFC 3339 UTC with milliseconds.
    let since: Timestamp?
    let limit: Int
    let cursor: String?
}

/// `{entries, next_cursor?}`; entries are newest first.
private struct TimelineResult: Decodable {
    let entries: [TimelineEntry]
    let nextCursor: String?
}

extension LiveBackend {
    public func timelineList(hostID: String, since: Date?, limit: Int, cursor: String?) async throws -> TimelinePage {
        let result: TimelineResult = try await client.call(
            "timeline.list", TimelineParams(hostID: hostID, since: since.map(Timestamp.init(date:)), limit: limit, cursor: cursor))
        return TimelinePage(entries: result.entries, nextCursor: result.nextCursor)
    }
}

// MARK: Events

extension LiveBackend {
    public func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        client.events(kinds: kinds, hostID: hostID)
    }
}

// MARK: Screen

extension LiveBackend {
    public func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        try await StreamConnection.attach(
            kind: "screen", params: ["host_id": hostID, "display": display, "preset": preset.rawValue],
            socketPath: client.socketPath, clientName: "Deskhand", clientVersion: clientVersion)
    }
}

// MARK: Files

private struct FilePathParams: Encodable {
    let hostID: String
    let path: String
}

private struct FileListParams: Encodable {
    let hostID: String
    let path: String
    let cursor: String?
    let limit: Int
}

private struct AppLaunchParams: Encodable {
    let hostID: String
    let path: String
    let args: [String]?
    let cwd: String?
}

private struct FileRenameParams: Encodable {
    let hostID: String
    let from: String
    let to: String
}

private struct FileDeleteParams: Encodable {
    let hostID: String
    let path: String
    let permanent: Bool
}

private struct TransferStartParams: Encodable {
    let direction: String
    let hostID: String
    let local: String
    let remote: String
    let onConflict: String
}

private struct TransferIDParams: Encodable {
    let transferID: String
}

/// `{host_id, path, entries, total, next_cursor?, truncated, skipped?}`; `path` is the folder as the PC spells it.
private struct FileListResult: Decodable {
    let path: String?
    let entries: [FileEntry]
    let nextCursor: String?
    let total: Int?
    let truncated: Bool?
}

/// `pid` of `app.launch` alongside the envelope.
private struct AppLaunchResult: Decodable {
    let pid: Int?
}

/// `transfer_id` of `transfer.start` alongside the envelope; null when the transfer did not start.
private struct TransferStartResult: Decodable {
    let transferID: String?

    private enum CodingKeys: String, CodingKey {
        case transferID = "transferId"
    }
}

/// `trashed` of `files.trash` alongside the envelope.
private struct TrashResult: Decodable {
    let trashed: Bool?
}

private struct TransferListResult: Decodable {
    let transfers: [Transfer]
}

extension LiveBackend {
    /// `files.list{host_id, path, cursor?, limit}` with `limit` clamped to 1–5000 (local-api.md §8).
    public func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        let result: FileListResult = try await client.call(
            "files.list", FileListParams(hostID: hostID, path: path, cursor: cursor, limit: min(max(limit, 1), 5000)))
        return FileEntryPage(entries: result.entries, path: result.path, nextCursor: result.nextCursor, total: result.total,
                             truncated: result.truncated ?? false)
    }

    public func filesStat(hostID: String, path: String) async throws -> FileStat {
        try await client.call("files.stat", FilePathParams(hostID: hostID, path: path))
    }

    public func filesMkdir(hostID: String, path: String) async throws {
        try await envelopeCall("files.mkdir", FilePathParams(hostID: hostID, path: path))
    }

    public func filesRename(hostID: String, from: String, to: String) async throws {
        try await envelopeCall("files.rename", FileRenameParams(hostID: hostID, from: from, to: to))
    }

    /// `files.trash`; `trashed: false` (the path no longer exists) throws `NOT_FOUND` because nothing was moved.
    public func filesTrash(hostID: String, path: String) async throws {
        let envelope: Envelope<TrashResult> = try await client.call(
            "files.trash", FilePathParams(hostID: hostID, path: path), timeout: CallLimit.bulkFiles)
        try Self.check(envelope)
        if envelope.payload.trashed == false {
            throw OperationError(code: "NOT_FOUND", message: .ui("files.trash.missing", "원격 PC에 그 항목이 없어 휴지통으로 옮긴 것이 없습니다"),
                                 nextAction: "check_state")
        }
    }

    public func filesDelete(hostID: String, path: String) async throws {
        try await envelopeCall("files.delete", FileDeleteParams(hostID: hostID, path: path, permanent: true), timeout: CallLimit.bulkFiles)
    }

    public func appLaunch(hostID: String, path: String, args: [String]?, cwd: String?) async throws -> Int? {
        let envelope: Envelope<AppLaunchResult> = try await client.call(
            "app.launch", AppLaunchParams(hostID: hostID, path: path, args: args, cwd: cwd))
        try Self.check(envelope)
        return envelope.payload.pid
    }

    public func transferStart(_ request: TransferRequest) async throws -> TransferStarted {
        let envelope: Envelope<TransferStartResult> = try await client.call("transfer.start", TransferStartParams(
            direction: request.direction.rawValue, hostID: request.hostID, local: request.local,
            remote: request.remote, onConflict: request.onConflict.rawValue))
        try Self.check(envelope)
        guard let id = envelope.payload.transferID else {
            throw OperationError(message: .ui("error.unknown", "알 수 없는 오류가 발생했습니다"), nextAction: envelope.nextAction)
        }
        return TransferStarted(transferID: id, jobID: envelope.jobID)
    }

    public func transferList(hostID: String) async throws -> [Transfer] {
        let result: TransferListResult = try await client.call("transfer.list", HostIDParams(hostID: hostID))
        return result.transfers
    }

    public func transferGet(transferID: String) async throws -> TransferDetail {
        try await client.call("transfer.get", TransferIDParams(transferID: transferID))
    }

    public func transferPause(transferID: String) async throws -> TransferDetail {
        try await client.call("transfer.pause", TransferIDParams(transferID: transferID))
    }

    public func transferResume(transferID: String) async throws -> TransferDetail {
        try await client.call("transfer.resume", TransferIDParams(transferID: transferID))
    }

    public func transferCancel(transferID: String) async throws -> TransferDetail {
        try await client.call("transfer.cancel", TransferIDParams(transferID: transferID))
    }

    /// Calls a method whose result is an envelope and throws its error unless it succeeded.
    private func envelopeCall(_ method: String, _ params: some Encodable & Sendable, timeout: Duration? = nil) async throws {
        let envelope: Envelope<Empty> = try await client.call(method, params, timeout: timeout)
        try Self.check(envelope)
    }

    /// Throws the envelope's error when its status is not `succeeded` or `running`.
    static func check<T>(_ envelope: Envelope<T>) throws {
        switch envelope.status {
        case .succeeded, .running, .waitingForUser:
            return
        case .unknown:
            throw OperationError(code: envelope.error?.code ?? "EXECUTION_OUTCOME_UNKNOWN",
                                 message: envelope.error?.message ?? .ui("error.outcomeUnknown", "원격 PC에서 실행됐는지 확인하지 못했습니다"),
                                 nextAction: envelope.nextAction ?? envelope.error?.nextAction)
        case .failed, .cancelled:
            if let error = envelope.error { throw OperationError(error) }
            throw OperationError(message: .ui("error.unknown", "알 수 없는 오류가 발생했습니다"), nextAction: envelope.nextAction)
        }
    }
}

// MARK: Terminal

private struct PTYOpenParams: Encodable {
    let hostID: String
    let shell: String
    let cols: Int
    let rows: Int
}

/// `pty_id` of `pty.open` alongside the envelope.
private struct PTYOpenResult: Decodable {
    let ptyID: String

    private enum CodingKeys: String, CodingKey {
        case ptyID = "ptyId"
    }
}

private struct JobsListParams: Encodable {
    let hostID: String
    let kind: String
    let status: String
}

private struct JobsListResult: Decodable {
    let jobs: [Job]
}

private struct JobIDParams: Encodable {
    let jobID: String
}

extension LiveBackend {
    public func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        let value: JSONValue = try await client.call(
            "pty.open", PTYOpenParams(hostID: hostID, shell: shell.rawValue, cols: cols, rows: rows))
        var jobID: String?
        if value["status"] != nil {
            let envelope = try Self.decode(Envelope<Empty>.self, from: value)
            try Self.check(envelope)
            jobID = envelope.jobID
        }
        return PTYOpening(ptyID: try Self.decode(PTYOpenResult.self, from: value).ptyID, jobID: jobID)
    }

    public func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream {
        try await StreamConnection.attach(
            kind: "pty", params: target.attachParams, socketPath: client.socketPath, clientName: "Deskhand",
            clientVersion: clientVersion)
    }

    public func ptyJobs(hostID: String) async throws -> [Job] {
        let result: JobsListResult = try await client.call(
            "jobs.list", JobsListParams(hostID: hostID, kind: "pty", status: EnvelopeStatus.running.rawValue))
        return result.jobs.filter { $0.kind == "pty" && $0.status == EnvelopeStatus.running.rawValue }
    }

    /// `jobs.cancel`, whose envelope reports `cancelled` when the shell ended.
    public func ptyKill(jobID: String) async throws {
        let envelope: Envelope<Empty> = try await client.call("jobs.cancel", JobIDParams(jobID: jobID))
        if envelope.status == .cancelled || envelope.status == .succeeded { return }
        try Self.check(envelope)
    }
}

// MARK: Processes and diagnostics

private struct ProcKillParams: Encodable {
    let hostID: String
    let pid: Int
    let started: Timestamp?
    let tree: Bool
}

private struct DiagEventsParams: Encodable {
    let hostID: String
    let log: String
    let since: Timestamp?
    let until: Timestamp?
    let levels: [String]?
    let provider: String?
    let max: Int
}

private struct DiagCrashesParams: Encodable {
    let hostID: String
    let since: Timestamp?
}

/// `procs` of `proc.list`.
private struct ProcListResult: Decodable {
    let procs: [RemoteProcess]
}

/// `killed` of `proc.kill` alongside the envelope.
private struct ProcKillResult: Decodable {
    let killed: Int?
}

/// `events` and `truncated` of `diag.events`.
private struct DiagEventsResult: Decodable {
    let events: [EventLogEntry]
    let truncated: Bool?
}

/// `dumps` of `diag.crashes`.
private struct DiagCrashesResult: Decodable {
    let dumps: [CrashDump]
}

extension LiveBackend {
    public func procList(hostID: String) async throws -> [RemoteProcess] {
        try await enveloped("proc.list", HostIDParams(hostID: hostID), as: ProcListResult.self).procs
    }

    public func procKill(hostID: String, pid: Int, started: Timestamp?, tree: Bool) async throws -> KillReceipt {
        let value: JSONValue = try await client.call("proc.kill", ProcKillParams(hostID: hostID, pid: pid, started: started, tree: tree))
        let envelope = try Self.decode(Envelope<ProcKillResult>.self, from: value)
        guard value["status"] != nil else {
            return KillReceipt(status: .succeeded, killed: envelope.payload.killed ?? 1)
        }
        return Self.killReceipt(envelope)
    }

    /// The receipt of a `proc.kill` envelope: `succeeded` with its count, `failed` with the
    /// agent's error, anything else as an unknown outcome whose next action is to check the list.
    private static func killReceipt(_ envelope: Envelope<ProcKillResult>) -> KillReceipt {
        switch envelope.status {
        case .succeeded:
            return KillReceipt(status: .succeeded, killed: envelope.payload.killed ?? 1)
        case .failed, .cancelled:
            let error = envelope.error.map(OperationError.init)
                ?? OperationError(message: .ui("error.unknown", "알 수 없는 오류가 발생했습니다"))
            return KillReceipt(status: .failed, error: OperationError(
                code: error.code, message: error.message, nextAction: envelope.nextAction ?? error.nextAction))
        case .unknown, .running, .waitingForUser:
            return KillReceipt(status: .unknown, error: OperationError(
                code: envelope.error?.code ?? "EXECUTION_OUTCOME_UNKNOWN",
                message: envelope.error?.message ?? .ui("error.outcomeUnknown", "원격 PC에서 실행됐는지 확인하지 못했습니다"),
                nextAction: envelope.nextAction ?? envelope.error?.nextAction ?? "check_state"))
        }
    }

    public func diagSystem(hostID: String) async throws -> DiagSystem {
        try await enveloped("diag.system", HostIDParams(hostID: hostID), as: DiagSystem.self)
    }

    public func diagEvents(_ query: EventLogQuery) async throws -> EventLogPage {
        let params = DiagEventsParams(
            hostID: query.hostID, log: query.log.rawValue, since: query.since.map(Timestamp.init(date:)),
            until: query.until.map(Timestamp.init(date:)), levels: query.levels?.map(\.rawValue), provider: query.provider,
            max: min(max(query.max, 1), EventLogQuery.maximumCount))
        let result = try await enveloped("diag.events", params, as: DiagEventsResult.self, timeout: CallLimit.eventLog)
        return EventLogPage(events: result.events, truncated: result.truncated ?? false)
    }

    public func diagCrashes(hostID: String, since: Date?) async throws -> [CrashDump] {
        try await enveloped("diag.crashes", DiagCrashesParams(hostID: hostID, since: since.map(Timestamp.init(date:))),
                            as: DiagCrashesResult.self).dumps
    }

    /// Calls a method whose result may carry the envelope of local-api.md §2, throws the
    /// envelope's error unless it succeeded, then decodes `T` from the same object.
    private func enveloped<T: Decodable>(_ method: String, _ params: some Encodable & Sendable, as type: T.Type,
                                         timeout: Duration? = nil) async throws -> T {
        let value: JSONValue = try await client.call(method, params, timeout: timeout)
        if value["status"] != nil {
            try Self.check(try Self.decode(Envelope<Empty>.self, from: value))
        }
        return try Self.decode(T.self, from: value)
    }
}
