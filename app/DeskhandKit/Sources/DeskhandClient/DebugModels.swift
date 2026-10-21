import Foundation

// Debugger models of local-api.md §5 and §9. Lines and columns count from 1.

/// `kind` of a DebugSession.
public enum DebugSessionKind: String, Codable, Sendable {
    case launch
    case attach
}

/// `state` of a DebugSession.
public enum DebugSessionState: String, Codable, Sendable {
    case running
    case paused
    case closed
}

/// `close_reason` of a DebugSession and `reason` of `debug.closed`.
public enum DebugCloseReason: String, Codable, Sendable {
    case requested
    case processExited = "process_exited"
    case connectionLost = "connection_lost"
}

/// `pause_on_exceptions` of a DebugSession and `mode` of `debug.exceptions`.
public enum ExceptionPauseMode: String, Codable, Sendable, CaseIterable {
    case none
    case uncaught
    case all
}

/// `role` of a DebugTarget.
public enum DebugTargetRole: String, Codable, Sendable {
    case main
    case renderer
}

/// `reason` of a PauseState.
public enum DebugPauseReason: String, Codable, Sendable {
    case breakpoint
    case exception
    case promiseRejection = "promise_rejection"
    case pauseRequest = "pause_request"
    case step
    /// The first-line stop of `--inspect-brk` kept by `pause_on_start`.
    case start
    case other
}

/// `main_unavailable_reason` of a renderer-only session.
public enum MainUnavailableReason: String, Codable, Sendable {
    /// The app ignores `--inspect` (its `EnableNodeCliInspectArguments` fuse is off).
    case inspectorDisabled = "inspector_disabled"
    case inspectorNotResponding = "inspector_not_responding"
}

/// `renderer_unavailable_reason` of a session with main and no renderer.
public enum RendererUnavailableReason: String, Codable, Sendable {
    /// The remote debugging port never opened.
    case remoteDebuggingDisabled = "remote_debugging_disabled"
    /// The port opened but `/json/list` did not answer.
    case remoteDebuggingNotResponding = "remote_debugging_not_responding"
}

/// `match` of a breakpoint: how its `pattern` names scripts.
public enum BreakpointMatch: String, Codable, Sendable, CaseIterable {
    /// The URL path ends with `pattern` at a path separator, ignoring `?query` and `#hash`.
    case fileName = "file_name"
    /// The exact URL.
    case url
    /// A JavaScript regular expression over URLs.
    case urlRegex = "url_regex"
}

/// The client behind a request: who made a session or asked for a pause, step or resume.
public struct DebugActor: Codable, Sendable, Equatable, Hashable {
    /// `human`, `ai` or `system`.
    public var actor: String
    public var clientName: String?

    public init(actor: String, clientName: String? = nil) {
        self.actor = actor
        self.clientName = clientName
    }

    public static let human = DebugActor(actor: "human")
}

/// `kind` of `debug.step`.
public enum DebugStepKind: String, Codable, Sendable, CaseIterable {
    case over
    case into
    case out
}

/// `level` of a ConsoleEntry, least severe first.
public enum ConsoleLevel: String, Codable, Sendable, CaseIterable, Comparable {
    case debug
    case info
    case log
    case warning
    case error

    private var rank: Int { Self.allCases.firstIndex(of: self) ?? 0 }

    public static func < (a: ConsoleLevel, b: ConsoleLevel) -> Bool { a.rank < b.rank }
}

/// `source` of a ConsoleEntry.
public enum ConsoleSource: String, Codable, Sendable {
    case console
    case exception
    case browser
}

/// `kind` of a Property.
public enum DebugPropertyKind: String, Codable, Sendable {
    case property
    case accessor
    case `internal`
}

/// Remote loopback ports of a session.
public struct DebugPorts: Codable, Sendable, Equatable {
    /// Node inspector port of the main process.
    public var inspect: Int?
    /// DevTools port of the renderers.
    public var remoteDebugging: Int?

    public init(inspect: Int? = nil, remoteDebugging: Int? = nil) {
        self.inspect = inspect
        self.remoteDebugging = remoteDebugging
    }
}

/// A debuggable target of a session: the main process or a renderer.
public struct DebugTarget: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var role: String
    public var type: String
    public var url: String
    public var title: String
    public var attached: Bool
    public var paused: Bool
    /// A pause was asked for and waits for the target to run code.
    public var pausePending: Bool
    /// Rises by one on every pause and resume of this target.
    public var gen: Int

    public var targetRole: DebugTargetRole? { DebugTargetRole(rawValue: role) }

    public init(id: String, role: String, type: String, url: String, title: String, attached: Bool = true, paused: Bool = false,
                pausePending: Bool = false, gen: Int = 0) {
        self.id = id
        self.role = role
        self.type = type
        self.url = url
        self.title = title
        self.attached = attached
        self.paused = paused
        self.pausePending = pausePending
        self.gen = gen
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        role = try c.decode(String.self, forKey: .role)
        type = try c.decodeIfPresent(String.self, forKey: .type) ?? "other"
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        attached = try c.decodeIfPresent(Bool.self, forKey: .attached) ?? true
        paused = try c.decodeIfPresent(Bool.self, forKey: .paused) ?? false
        pausePending = try c.decodeIfPresent(Bool.self, forKey: .pausePending) ?? false
        gen = try c.decodeIfPresent(Int.self, forKey: .gen) ?? 0
    }

    private enum CodingKeys: String, CodingKey {
        case id, role, type, url, title, attached, paused, pausePending, gen
    }
}

/// Where a breakpoint landed in a loaded script.
public struct BreakpointLocation: Codable, Sendable, Equatable, Hashable {
    public var targetID: String
    public var scriptID: String
    public var url: String
    public var line: Int
    public var column: Int

    public init(targetID: String, scriptID: String, url: String, line: Int, column: Int) {
        self.targetID = targetID
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
    }

    private enum CodingKeys: String, CodingKey {
        case targetID = "targetId"
        case scriptID = "scriptId"
        case url, line, column
    }
}

/// What a new breakpoint is (`debug.breakpoint.set`, the `breakpoints` of launch and attach).
public struct BreakpointSpec: Codable, Sendable, Equatable, Hashable {
    public var match: String
    public var pattern: String
    public var line: Int
    public var column: Int?
    public var condition: String?
    public var enabled: Bool?

    public var matchKind: BreakpointMatch { BreakpointMatch(rawValue: match) ?? .urlRegex }

    public init(match: BreakpointMatch, pattern: String, line: Int, column: Int? = nil, condition: String? = nil, enabled: Bool? = nil) {
        self.match = match.rawValue
        self.pattern = pattern
        self.line = line
        self.column = column
        self.condition = condition
        self.enabled = enabled
    }
}

/// A breakpoint of a session: its spec, where it bound, and its `rev`.
public struct DebugBreakpoint: Codable, Sendable, Equatable, Identifiable {
    public var breakpointID: String
    public var match: String
    public var pattern: String
    public var line: Int
    public var column: Int?
    public var condition: String?
    public var enabled: Bool
    public var resolved: Bool
    public var locations: [BreakpointLocation]
    /// Rises by one on every change of this breakpoint, removal included.
    public var rev: Int

    public var id: String { breakpointID }
    public var matchKind: BreakpointMatch { BreakpointMatch(rawValue: match) ?? .urlRegex }

    /// The breakpoint as a spec to set again elsewhere.
    public var spec: BreakpointSpec {
        BreakpointSpec(match: matchKind, pattern: pattern, line: line, column: column, condition: condition, enabled: enabled)
    }

    public init(breakpointID: String, match: BreakpointMatch, pattern: String, line: Int, column: Int? = nil,
                condition: String? = nil, enabled: Bool = true, locations: [BreakpointLocation] = [], rev: Int = 1) {
        self.breakpointID = breakpointID
        self.match = match.rawValue
        self.pattern = pattern
        self.line = line
        self.column = column
        self.condition = condition
        self.enabled = enabled
        self.resolved = !locations.isEmpty
        self.locations = locations
        self.rev = rev
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        breakpointID = try c.decode(String.self, forKey: .breakpointID)
        match = try c.decodeIfPresent(String.self, forKey: .match) ?? BreakpointMatch.urlRegex.rawValue
        pattern = try c.decodeIfPresent(String.self, forKey: .pattern) ?? ""
        line = try c.decode(Int.self, forKey: .line)
        column = try c.decodeIfPresent(Int.self, forKey: .column)
        condition = try c.decodeIfPresent(String.self, forKey: .condition)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
        locations = try c.decodeIfPresent([BreakpointLocation].self, forKey: .locations) ?? []
        resolved = try c.decodeIfPresent(Bool.self, forKey: .resolved) ?? !locations.isEmpty
        rev = try c.decodeIfPresent(Int.self, forKey: .rev) ?? 0
    }

    private enum CodingKeys: String, CodingKey {
        case breakpointID = "breakpointId"
        case match, pattern, line, column, condition, enabled, resolved, locations, rev
    }
}

/// A debugging session the daemon owns: a launched or attached Electron or Node app.
public struct DebugSession: Codable, Sendable, Equatable, Identifiable {
    public var debugID: String
    public var hostID: String
    public var jobID: String?
    public var kind: String
    public var name: String
    public var path: String?
    public var args: [String]
    public var cwd: String?
    public var pauseOnStart: Bool
    public var pid: Int?
    public var ports: DebugPorts
    public var state: String
    /// Main first.
    public var targets: [DebugTarget]
    /// Why only renderers are attached.
    public var mainUnavailableReason: String?
    public var rendererUnavailableReason: String?
    /// In creation order.
    public var breakpoints: [DebugBreakpoint]
    public var breakpointsActive: Bool
    public var pauseOnExceptions: String
    public var createdBy: DebugActor
    public var createdAt: Timestamp
    public var closedAt: Timestamp?
    public var closeReason: String?
    public var exitCode: Int?
    /// Rises by one on every change of the session's own fields.
    public var rev: Int

    public var id: String { debugID }
    public var sessionKind: DebugSessionKind? { DebugSessionKind(rawValue: kind) }
    public var sessionState: DebugSessionState? { DebugSessionState(rawValue: state) }
    public var exceptionMode: ExceptionPauseMode { ExceptionPauseMode(rawValue: pauseOnExceptions) ?? .uncaught }
    public var closeKind: DebugCloseReason? { closeReason.flatMap(DebugCloseReason.init(rawValue:)) }
    public var mainUnavailable: MainUnavailableReason? {
        mainUnavailableReason.map { MainUnavailableReason(rawValue: $0) ?? .inspectorNotResponding }
    }
    public var rendererUnavailable: RendererUnavailableReason? {
        rendererUnavailableReason.map { RendererUnavailableReason(rawValue: $0) ?? .remoteDebuggingNotResponding }
    }

    public init(debugID: String, hostID: String, jobID: String? = nil, kind: DebugSessionKind, name: String, path: String? = nil,
                args: [String] = [], cwd: String? = nil, pauseOnStart: Bool = false, pid: Int? = nil, ports: DebugPorts = DebugPorts(),
                state: DebugSessionState = .running, targets: [DebugTarget] = [], mainUnavailable: MainUnavailableReason? = nil,
                rendererUnavailable: RendererUnavailableReason? = nil,
                breakpoints: [DebugBreakpoint] = [], breakpointsActive: Bool = true, pauseOnExceptions: ExceptionPauseMode = .uncaught,
                createdBy: DebugActor = .human, createdAt: Timestamp, closedAt: Timestamp? = nil, closeReason: DebugCloseReason? = nil,
                exitCode: Int? = nil, rev: Int = 1) {
        self.debugID = debugID
        self.hostID = hostID
        self.jobID = jobID
        self.kind = kind.rawValue
        self.name = name
        self.path = path
        self.args = args
        self.cwd = cwd
        self.pauseOnStart = pauseOnStart
        self.pid = pid
        self.ports = ports
        self.state = state.rawValue
        self.targets = targets
        self.mainUnavailableReason = mainUnavailable?.rawValue
        self.rendererUnavailableReason = rendererUnavailable?.rawValue
        self.breakpoints = breakpoints
        self.breakpointsActive = breakpointsActive
        self.pauseOnExceptions = pauseOnExceptions.rawValue
        self.createdBy = createdBy
        self.createdAt = createdAt
        self.closedAt = closedAt
        self.closeReason = closeReason?.rawValue
        self.exitCode = exitCode
        self.rev = rev
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        debugID = try c.decode(String.self, forKey: .debugID)
        hostID = try c.decode(String.self, forKey: .hostID)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        kind = try c.decode(String.self, forKey: .kind)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        path = try c.decodeIfPresent(String.self, forKey: .path)
        args = try c.decodeIfPresent([String].self, forKey: .args) ?? []
        cwd = try c.decodeIfPresent(String.self, forKey: .cwd)
        pauseOnStart = try c.decodeIfPresent(Bool.self, forKey: .pauseOnStart) ?? false
        pid = try c.decodeIfPresent(Int.self, forKey: .pid)
        ports = try c.decodeIfPresent(DebugPorts.self, forKey: .ports) ?? DebugPorts()
        state = try c.decode(String.self, forKey: .state)
        targets = try c.decodeIfPresent([DebugTarget].self, forKey: .targets) ?? []
        mainUnavailableReason = try c.decodeIfPresent(String.self, forKey: .mainUnavailableReason)
        rendererUnavailableReason = try c.decodeIfPresent(String.self, forKey: .rendererUnavailableReason)
        breakpoints = try c.decodeIfPresent([DebugBreakpoint].self, forKey: .breakpoints) ?? []
        breakpointsActive = try c.decodeIfPresent(Bool.self, forKey: .breakpointsActive) ?? true
        pauseOnExceptions = try c.decodeIfPresent(String.self, forKey: .pauseOnExceptions) ?? ExceptionPauseMode.uncaught.rawValue
        createdBy = try c.decodeIfPresent(DebugActor.self, forKey: .createdBy) ?? .human
        createdAt = try c.decode(Timestamp.self, forKey: .createdAt)
        closedAt = try c.decodeIfPresent(Timestamp.self, forKey: .closedAt)
        closeReason = try c.decodeIfPresent(String.self, forKey: .closeReason)
        exitCode = try c.decodeIfPresent(Int.self, forKey: .exitCode)
        rev = try c.decodeIfPresent(Int.self, forKey: .rev) ?? 0
    }

    private enum CodingKeys: String, CodingKey {
        case debugID = "debugId"
        case hostID = "hostId"
        case jobID = "jobId"
        case kind, name, path, args, cwd, pauseOnStart, pid, ports, state, targets, mainUnavailableReason, rendererUnavailableReason, breakpoints
        case breakpointsActive, pauseOnExceptions, createdBy, createdAt, closedAt, closeReason, exitCode, rev
    }
}
