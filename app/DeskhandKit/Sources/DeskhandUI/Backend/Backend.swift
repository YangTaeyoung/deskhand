import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// Everything the screens ask of the daemon, grouped by feature. A feature adds its own
/// protocol below and joins it to this list; `LiveBackend` and `PreviewBackend` implement each
/// feature protocol in their own extension.
public protocol Backend: BackendLifecycle, HostsBackend, ControlBackend, TimelineBackend, EventsBackend {}

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
        case "IN_PROGRESS":
            .ui("fix.inProgress", "같은 작업이 이미 진행 중입니다. 끝난 뒤 다시 시도하세요.")
        default:
            nil
        }
    }
}
