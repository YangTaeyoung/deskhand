import DeskhandClient
import Foundation
import struct DeskhandClient.Host
import struct DeskhandClient.ProcessInfo

/// Everything the screens ask of the daemon, grouped by feature. A feature adds its own
/// protocol below and joins it to this list; `LiveBackend` and `PreviewBackend` implement each
/// feature protocol in their own extension.
public protocol Backend: BackendLifecycle, HostsBackend, ControlBackend, TimelineBackend, EventsBackend, ScreenBackend, FilesBackend,
    TerminalBackend, ProcessesBackend, DiagnosticsBackend {}

/// A process of the remote computer (`ProcessInfo` of DeskhandClient).
public typealias RemoteProcess = ProcessInfo

public protocol BackendLifecycle: Sendable {
    /// Makes the daemon reachable and says hello. Throws `ClientError.daemonUnavailable` when the
    /// daemon cannot be started.
    func start() async throws
}

/// `host.*` and `session.inspect` (local-api.md §3).
public protocol HostsBackend: Sendable {
    func hostList() async throws -> [Host]
    /// Starts installation and pairing; progress arrives as `host.install.progress` events.
    func hostAdd(name: String, ssh: String) async throws -> JobReceipt
    func hostRemove(hostID: String, uninstall: Bool) async throws
    func hostDoctor(hostID: String) async throws -> [DoctorCheck]
    func hostConnect(hostID: String) async throws
    func hostDisconnect(hostID: String) async throws
    /// Re-pairs a host whose certificate no longer matches.
    func hostRepair(hostID: String) async throws -> JobReceipt
    func sessionInspect(hostID: String) async throws -> SessionInspect
    /// `Host` aliases from the user's SSH configuration, offered in the add-computer sheet.
    func sshAliases() async -> [String]
}

/// `control.*` (local-api.md §3).
public protocol ControlBackend: Sendable {
    func controlGet(hostID: String) async throws -> ControlState
    func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState
    func controlRelease(hostID: String) async throws
    /// Emergency stop: the human takes control and AI input is paused.
    func controlStop(hostID: String) async throws -> ControlState
    func controlResumeAI(hostID: String) async throws -> ControlState
}

/// `timeline.list` (local-api.md §3, §6).
public protocol TimelineBackend: Sendable {
    /// Entries newest first.
    func timelineList(hostID: String, since: Int?, limit: Int) async throws -> [TimelineEntry]
}

/// `stream.attach` with kind `screen` (local-api.md §4).
public protocol ScreenBackend: Sendable {
    /// Opens a stream connection for the screen of `display` at `preset`. Frames follow
    /// protocol.md §4–§5 and local-api.md §4; the stream finishes when the daemon closes it.
    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream
}

/// `files.*`, `app.launch` and `transfer.*` (local-api.md §3). Calls that change remote state
/// throw `OperationError` when their envelope reports a failure or an unknown outcome.
public protocol FilesBackend: Sendable {
    /// One page of the folder at `path`: at most `limit` entries after `cursor`.
    func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage
    func filesStat(hostID: String, path: String) async throws -> FileEntry
    func filesMkdir(hostID: String, path: String) async throws
    func filesRename(hostID: String, from: String, to: String) async throws
    /// Moves `path` to the remote computer's recycle bin.
    func filesTrash(hostID: String, path: String) async throws
    /// Deletes `path` permanently (`permanent: true`).
    func filesDelete(hostID: String, path: String) async throws
    /// Starts `path` on the remote computer in the signed-in user's session and returns its
    /// process ID when the agent reports one.
    func appLaunch(hostID: String, path: String, args: [String]?, cwd: String?) async throws -> Int?
    /// Starts a transfer and returns its `transfer_id`; progress arrives as `transfer.updated` events.
    func transferStart(_ request: TransferRequest) async throws -> String
    /// Transfers of `hostID`, newest first.
    func transferList(hostID: String) async throws -> [Transfer]
    func transferGet(transferID: String) async throws -> Transfer
    /// Stops a running transfer, keeping the finished files and verified chunks.
    func transferPause(transferID: String) async throws -> Transfer
    /// Continues a paused, failed or cancelled transfer from its verified chunks.
    func transferResume(transferID: String) async throws -> Transfer
    /// Stops a transfer; finished files stay at the destination.
    func transferCancel(transferID: String) async throws -> Transfer
}

/// `pty.open`, `stream.attach` with kind `pty`, and the pty jobs of `jobs.*` (local-api.md §3–§5).
/// Calls that start or end remote work throw `OperationError` when their envelope reports a failure.
public protocol TerminalBackend: Sendable {
    /// Starts `shell` on the computer with a `cols`×`rows` console.
    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening
    /// Opens the stream of a pty. Frames follow protocol.md §7: the attached frame (`0x23`) comes
    /// first, then the scrollback and live output as data frames (`0x20`).
    func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream
    /// The computer's running pty jobs (`jobs.list{host_id, kind:"pty", status:"running"}`).
    func ptyJobs(hostID: String) async throws -> [Job]
    /// Ends the shell of a pty job (`jobs.cancel`).
    func ptyKill(jobID: String) async throws
}

/// `proc.list` and `proc.kill{host_id, pid, tree?}` (local-api.md §3 진단, protocol.md §3.1).
public protocol ProcessesBackend: Sendable {
    /// Every process of the computer.
    func procList(hostID: String) async throws -> [RemoteProcess]
    /// Ends `pid`, and with `tree` its descendants. A `failed` or `unknown` envelope comes back
    /// as the receipt's status; request problems throw.
    func procKill(hostID: String, pid: Int, tree: Bool) async throws -> KillReceipt
}

/// `diag.system`, `diag.events` and `diag.crashes` (local-api.md §3 진단, protocol.md §3.1).
/// A `failed` or `unknown` envelope throws `OperationError`.
public protocol DiagnosticsBackend: Sendable {
    func diagSystem(hostID: String) async throws -> DiagSystem
    /// Events of one log, newest first.
    func diagEvents(_ query: EventLogQuery) async throws -> EventLogPage
    /// Crash dumps and error reports since `since` (the agent's default, 30 days, when nil), newest first.
    func diagCrashes(hostID: String, since: Date?) async throws -> [CrashDump]
}

/// The envelope of `proc.kill`: its status, how many processes ended, and the error and next
/// action of a `failed` or `unknown` status.
public struct KillReceipt: Sendable, Equatable {
    public var status: EnvelopeStatus
    public var killed: Int
    public var error: OperationError?

    public init(status: EnvelopeStatus, killed: Int = 0, error: OperationError? = nil) {
        self.status = status
        self.killed = killed
        self.error = error
    }
}

/// `log` of `diag.events`.
public enum EventLogName: String, Sendable, CaseIterable, Identifiable {
    case system = "System"
    case application = "Application"

    public var id: String { rawValue }
}

/// Parameters of `diag.events`.
public struct EventLogQuery: Sendable, Equatable {
    /// Largest `max` the agent accepts.
    public static let maximumCount = 1000

    public var hostID: String
    public var log: EventLogName
    public var since: Date?
    public var until: Date?
    /// Levels to return; nil returns every level.
    public var levels: [EventLevel]?
    /// Exact provider name.
    public var provider: String?
    public var max: Int

    public init(hostID: String, log: EventLogName, since: Date? = nil, until: Date? = nil, levels: [EventLevel]? = nil,
                provider: String? = nil, max: Int = 200) {
        self.hostID = hostID
        self.log = log
        self.since = since
        self.until = until
        self.levels = levels
        self.provider = provider
        self.max = max
    }
}

/// Result of `diag.events`.
public struct EventLogPage: Sendable, Equatable {
    /// Newest first.
    public var events: [EventLogEntry]
    /// The agent stopped at `max`; older events in the range were not returned.
    public var truncated: Bool

    public init(events: [EventLogEntry], truncated: Bool) {
        self.events = events
        self.truncated = truncated
    }
}

/// What `stream.attach{kind:"pty"}` attaches to.
public enum PTYTarget: Equatable, Hashable, Sendable {
    /// A pty that `pty.open` just started (`pty_id`).
    case pty(String)
    /// A running pty job, attached again with its scrollback (`job_id`).
    case job(String)

    /// `stream.attach` parameters besides `kind`.
    public var attachParams: [String: String] {
        switch self {
        case .pty(let id): ["pty_id": id]
        case .job(let id): ["job_id": id]
        }
    }
}

/// Result of `pty.open`: the stream target and, when the envelope carries it, the pty's job.
public struct PTYOpening: Equatable, Sendable {
    public var ptyID: String
    public var jobID: String?

    public init(ptyID: String, jobID: String? = nil) {
        self.ptyID = ptyID
        self.jobID = jobID
    }
}

/// Any connection opened by `stream.attach`.
public typealias FrameStream = ScreenStream

/// A binary frame connection opened by `stream.attach`.
public protocol ScreenStream: AnyObject, Sendable {
    /// Frames from the daemon in arrival order.
    var frames: AsyncThrowingStream<StreamConnection.Frame, Error> { get }
    /// Sends one frame of `kind` with `payload`.
    func send(kind: UInt8, payload: Data) async throws
    func close()
}

extension StreamConnection: ScreenStream {}

/// Daemon event notifications (`events.subscribe`).
public protocol EventsBackend: Sendable {
    /// Events matching the filters; `nil` matches everything. The stream outlives reconnects.
    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error>
}

/// Event kinds the app reacts to, with their payload fields (local-api.md §6).
public enum EventKind {
    /// `state`, `connection_generation`, `reason?`.
    public static let connectionState = "connection.state"
    /// ControlState fields: `owner`, `owner_client?`, `gen`, `ai_paused`.
    public static let controlChanged = "control.changed"
    /// `entry` (TimelineEntry).
    public static let timelineAppended = "timeline.appended"
    /// `host` (Host).
    public static let hostAdded = "host.added"
    /// No fields besides `host_id`.
    public static let hostRemoved = "host.removed"
    /// `host` (Host).
    public static let hostUpdated = "host.updated"
    /// `caps`.
    public static let capsChanged = "caps.changed"
    /// `session` (SessionInfo).
    public static let sessionChanged = "session.changed"
    /// `displays` ([DisplayInfo]).
    public static let displayChanged = "display.changed"
    /// `name`, `step`, `status`, `detail?`, `error?{code, message, next_action?}`.
    public static let installProgress = "host.install.progress"
    /// `transfer` (Transfer).
    public static let transferUpdated = "transfer.updated"
    /// `job` (Job).
    public static let jobUpdated = "job.updated"

    /// Every kind `AppStore` subscribes to.
    public static let storeKinds = [
        connectionState, controlChanged, timelineAppended, hostAdded, hostRemoved, hostUpdated,
        capsChanged, sessionChanged, displayChanged, installProgress,
    ]
}

/// The envelope of a method that starts a daemon job (`host.add`, `host.repair`).
public struct JobReceipt: Sendable, Equatable {
    public var jobID: String?
    public var hostID: String?
    public var status: EnvelopeStatus
    public var error: EnvelopeError?
    public var nextAction: String?

    public init(jobID: String?, hostID: String? = nil, status: EnvelopeStatus, error: EnvelopeError? = nil, nextAction: String? = nil) {
        self.jobID = jobID
        self.hostID = hostID
        self.status = status
        self.error = error
        self.nextAction = nextAction
    }
}

/// Installation steps of `host.add`, in order.
public enum InstallStep: String, CaseIterable, Sendable {
    case probe
    case copy
    case initialize = "init"
    case install
    case verify
}

/// One `host.install.progress` event.
public struct InstallProgress: Sendable, Equatable {
    public enum Status: String, Sendable {
        case active
        case done
        case failed
    }

    public var hostID: String?
    public var name: String
    public var step: InstallStep
    public var status: Status
    public var detail: String?
    public var error: OperationError?

    public init(hostID: String? = nil, name: String, step: InstallStep, status: Status, detail: String? = nil, error: OperationError? = nil) {
        self.hostID = hostID
        self.name = name
        self.step = step
        self.status = status
        self.detail = detail
        self.error = error
    }

    public init?(event: DaemonEvent) {
        let p = event.payload
        guard event.kind == EventKind.installProgress,
              let name = p["name"]?.stringValue,
              let step = p["step"]?.stringValue.flatMap(InstallStep.init(rawValue:)),
              let status = p["status"]?.stringValue.flatMap(Status.init(rawValue:))
        else { return nil }
        var error: OperationError?
        if let e = p["error"], case .object = e {
            error = OperationError(
                code: e["code"]?.stringValue,
                message: e["message"]?.stringValue ?? .ui("error.installFailed", "설치하지 못했습니다"),
                nextAction: e["next_action"]?.stringValue)
        }
        self.init(hostID: p["host_id"]?.stringValue ?? event.hostID, name: name, step: step, status: status,
                  detail: p["detail"]?.stringValue, error: error)
    }

    /// The event payload in the daemon's spelling.
    public var payload: [String: JSONValue] {
        var p: [String: JSONValue] = ["name": .string(name), "step": .string(step.rawValue), "status": .string(status.rawValue)]
        if let hostID { p["host_id"] = .string(hostID) }
        if let detail { p["detail"] = .string(detail) }
        if let error {
            var e: [String: JSONValue] = ["message": .string(error.message)]
            if let code = error.code { e["code"] = .string(code) }
            if let next = error.nextAction { e["next_action"] = .string(next) }
            p["error"] = .object(e)
        }
        return p
    }
}

/// A failure as the screens present it: what failed, the daemon error code, and the suggested next action.
public struct OperationError: Error, Sendable, Equatable {
    public var code: String?
    public var message: String
    public var nextAction: String?

    public init(code: String? = nil, message: String, nextAction: String? = nil) {
        self.code = code
        self.message = message
        self.nextAction = nextAction
    }

    public init(_ error: any Error) {
        switch error {
        case let error as OperationError:
            self = error
        case let error as RPCErrorInfo:
            self.init(code: error.code, message: error.message, nextAction: error.nextAction)
        case let error as ClientError:
            switch error {
            case .daemonUnavailable:
                self.init(message: .ui("error.daemonUnavailable", "데몬이 응답하지 않습니다"))
            case .notConnected, .disconnected, .closed:
                self.init(message: .ui("error.disconnected", "데몬과 연결이 끊겼습니다"))
            case .socketPathTooLong(let path):
                self.init(message: .ui("error.socketPathTooLong", "데몬 소켓 경로가 너무 깁니다: \(path)"))
            case .connectFailed(let errno):
                self.init(message: .ui("error.connectFailed", "데몬 소켓에 연결할 수 없습니다 (errno \(Int(errno)))"))
            case .protocolViolation:
                self.init(message: .ui("error.protocol", "데몬 응답을 이해할 수 없습니다"))
            }
        case is DecodingError:
            self.init(message: .ui("error.decoding", "데몬 응답 형식이 예상과 다릅니다"))
        default:
            self.init(message: error.localizedDescription)
        }
    }

    public init(_ error: EnvelopeError) {
        self.init(code: error.code, message: error.message ?? .ui("error.unknown", "알 수 없는 오류가 발생했습니다"),
                  nextAction: error.nextAction)
    }

    /// The daemon's message followed by the remedy on its own line when one is known.
    public var detail: String {
        [message, fix].compactMap { $0 }.joined(separator: "\n")
    }

    /// How to resolve the failure, for error codes with a known remedy.
    public var fix: String? {
        switch code {
        case "AUTH_REQUIRED":
            .ui("fix.authRequired", "이 별칭으로 비밀번호 없이 ssh 접속되는지 확인하세요.")
        case "OFFLINE", "TIMEOUT":
            .ui("fix.offline", "컴퓨터가 켜져 있고 같은 네트워크나 VPN에 있는지 확인하세요.")
        case "ELEVATION_REQUIRED", "PERMISSION_DENIED":
            .ui("fix.elevation", "관리자 계정의 SSH 별칭으로 다시 시도하세요.")
        case "SESSION_LOCKED":
            .ui("fix.sessionLocked", "원격 컴퓨터에서 로그인하거나 잠금을 해제하세요.")
        case "PORT_CONFLICT":
            .ui("fix.portConflict", "다른 프로그램이 UDP 47600 포트를 쓰고 있습니다. 그 프로그램을 종료한 뒤 다시 시도하세요.")
        case "CAPABILITY_UNSUPPORTED":
            .ui("fix.capabilityUnsupported", "원격 PC의 에이전트를 최신 버전으로 업데이트한 뒤 다시 시도하세요.")
        case "IN_PROGRESS":
            .ui("fix.inProgress", "같은 작업이 이미 진행 중입니다. 끝난 뒤 다시 시도하세요.")
        case "DISK_FULL":
            .ui("fix.diskFull", "대상 디스크의 공간을 확보한 뒤 이어서 보내세요.")
        case "HASH_MISMATCH":
            .ui("fix.hashMismatch", "보내는 동안 원본이 바뀌었습니다. 원본을 확인하고 다시 보내세요.")
        case "TRANSFER_CONFLICT":
            .ui("fix.transferConflict", "같은 이름의 항목이 있습니다. 바꾸기·건너뛰기·둘 다 유지 중 하나를 골라 다시 보내세요.")
        case "NOT_FOUND":
            .ui("fix.notFound", "목록을 새로 고친 뒤 다시 시도하세요.")
        case "EXECUTION_OUTCOME_UNKNOWN":
            .ui("fix.outcomeUnknown", "실행됐는지 확인하지 못했습니다. 목록을 새로 고쳐 실제 상태를 확인하세요.")
        default:
            nil
        }
    }
}
