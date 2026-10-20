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
    case other
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
    /// Rises by one on every pause and resume of this target.
    public var gen: Int

    public var targetRole: DebugTargetRole? { DebugTargetRole(rawValue: role) }

    public init(id: String, role: String, type: String, url: String, title: String, attached: Bool = true, paused: Bool = false,
                gen: Int = 0) {
        self.id = id
        self.role = role
        self.type = type
        self.url = url
        self.title = title
        self.attached = attached
        self.paused = paused
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
        gen = try c.decodeIfPresent(Int.self, forKey: .gen) ?? 0
    }

    private enum CodingKeys: String, CodingKey {
        case id, role, type, url, title, attached, paused, gen
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

/// A breakpoint of a session, set by exact `url` or by `urlRegex`.
public struct DebugBreakpoint: Codable, Sendable, Equatable, Identifiable {
    public var breakpointID: String
    public var url: String?
    public var urlRegex: String?
    public var line: Int
    public var column: Int?
    public var condition: String?
    public var enabled: Bool
    public var resolved: Bool
    public var locations: [BreakpointLocation]

    public var id: String { breakpointID }

    public init(breakpointID: String, url: String? = nil, urlRegex: String? = nil, line: Int, column: Int? = nil,
                condition: String? = nil, enabled: Bool = true, locations: [BreakpointLocation] = []) {
        self.breakpointID = breakpointID
        self.url = url
        self.urlRegex = urlRegex
        self.line = line
        self.column = column
        self.condition = condition
        self.enabled = enabled
        self.resolved = !locations.isEmpty
        self.locations = locations
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        breakpointID = try c.decode(String.self, forKey: .breakpointID)
        url = try c.decodeIfPresent(String.self, forKey: .url)
        urlRegex = try c.decodeIfPresent(String.self, forKey: .urlRegex)
        line = try c.decode(Int.self, forKey: .line)
        column = try c.decodeIfPresent(Int.self, forKey: .column)
        condition = try c.decodeIfPresent(String.self, forKey: .condition)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
        locations = try c.decodeIfPresent([BreakpointLocation].self, forKey: .locations) ?? []
        resolved = try c.decodeIfPresent(Bool.self, forKey: .resolved) ?? !locations.isEmpty
    }

    private enum CodingKeys: String, CodingKey {
        case breakpointID = "breakpointId"
        case urlRegex
        case url, line, column, condition, enabled, resolved, locations
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
    public var pid: Int?
    public var ports: DebugPorts
    public var state: String
    /// Main first.
    public var targets: [DebugTarget]
    /// In creation order.
    public var breakpoints: [DebugBreakpoint]
    public var breakpointsActive: Bool
    public var pauseOnExceptions: String
    public var createdAt: Timestamp
    public var closedAt: Timestamp?
    public var closeReason: String?
    public var exitCode: Int?

    public var id: String { debugID }
    public var sessionKind: DebugSessionKind? { DebugSessionKind(rawValue: kind) }
    public var sessionState: DebugSessionState? { DebugSessionState(rawValue: state) }
    public var exceptionMode: ExceptionPauseMode { ExceptionPauseMode(rawValue: pauseOnExceptions) ?? .uncaught }
    public var closeKind: DebugCloseReason? { closeReason.flatMap(DebugCloseReason.init(rawValue:)) }

    public init(debugID: String, hostID: String, jobID: String? = nil, kind: DebugSessionKind, name: String, path: String? = nil,
                args: [String] = [], pid: Int? = nil, ports: DebugPorts = DebugPorts(), state: DebugSessionState = .running,
                targets: [DebugTarget] = [], breakpoints: [DebugBreakpoint] = [], breakpointsActive: Bool = true,
                pauseOnExceptions: ExceptionPauseMode = .uncaught, createdAt: Timestamp, closedAt: Timestamp? = nil,
                closeReason: DebugCloseReason? = nil, exitCode: Int? = nil) {
        self.debugID = debugID
        self.hostID = hostID
        self.jobID = jobID
        self.kind = kind.rawValue
        self.name = name
        self.path = path
        self.args = args
        self.pid = pid
        self.ports = ports
        self.state = state.rawValue
        self.targets = targets
        self.breakpoints = breakpoints
        self.breakpointsActive = breakpointsActive
        self.pauseOnExceptions = pauseOnExceptions.rawValue
        self.createdAt = createdAt
        self.closedAt = closedAt
        self.closeReason = closeReason?.rawValue
        self.exitCode = exitCode
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
        pid = try c.decodeIfPresent(Int.self, forKey: .pid)
        ports = try c.decodeIfPresent(DebugPorts.self, forKey: .ports) ?? DebugPorts()
        state = try c.decode(String.self, forKey: .state)
        targets = try c.decodeIfPresent([DebugTarget].self, forKey: .targets) ?? []
        breakpoints = try c.decodeIfPresent([DebugBreakpoint].self, forKey: .breakpoints) ?? []
        breakpointsActive = try c.decodeIfPresent(Bool.self, forKey: .breakpointsActive) ?? true
        pauseOnExceptions = try c.decodeIfPresent(String.self, forKey: .pauseOnExceptions) ?? ExceptionPauseMode.uncaught.rawValue
        createdAt = try c.decode(Timestamp.self, forKey: .createdAt)
        closedAt = try c.decodeIfPresent(Timestamp.self, forKey: .closedAt)
        closeReason = try c.decodeIfPresent(String.self, forKey: .closeReason)
        exitCode = try c.decodeIfPresent(Int.self, forKey: .exitCode)
    }

    private enum CodingKeys: String, CodingKey {
        case debugID = "debugId"
        case hostID = "hostId"
        case jobID = "jobId"
        case kind, name, path, args, pid, ports, state, targets, breakpoints
        case breakpointsActive, pauseOnExceptions, createdAt, closedAt, closeReason, exitCode
    }
}

/// A value of the debuggee as CDP `RemoteObject` describes it.
public struct RemoteValue: Codable, Sendable, Equatable, Hashable {
    public var type: String
    public var subtype: String?
    public var className: String?
    /// One readable line, at most 200 characters.
    public var valuePreview: String
    /// Present for values whose properties can be read with `debug.variables`.
    public var objectID: String?
    public var hasChildren: Bool

    public init(type: String, subtype: String? = nil, className: String? = nil, valuePreview: String, objectID: String? = nil,
                hasChildren: Bool? = nil) {
        self.type = type
        self.subtype = subtype
        self.className = className
        self.valuePreview = valuePreview
        self.objectID = objectID
        self.hasChildren = hasChildren ?? (objectID != nil)
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decode(String.self, forKey: .type)
        subtype = try c.decodeIfPresent(String.self, forKey: .subtype)
        className = try c.decodeIfPresent(String.self, forKey: .className)
        valuePreview = try c.decodeIfPresent(String.self, forKey: .valuePreview) ?? ""
        objectID = try c.decodeIfPresent(String.self, forKey: .objectID)
        hasChildren = try c.decodeIfPresent(Bool.self, forKey: .hasChildren) ?? (objectID != nil)
    }

    private enum CodingKeys: String, CodingKey {
        case objectID = "objectId"
        case type, subtype, className, valuePreview, hasChildren
    }
}

/// A scope of a call frame; its variables are the properties of `objectID`.
public struct DebugScope: Codable, Sendable, Equatable, Hashable {
    public var type: String
    public var objectID: String
    public var name: String?

    public init(type: String, objectID: String, name: String? = nil) {
        self.type = type
        self.objectID = objectID
        self.name = name
    }

    private enum CodingKeys: String, CodingKey {
        case objectID = "objectId"
        case type, name
    }
}

/// A frame of the paused call stack.
public struct DebugCallFrame: Codable, Sendable, Equatable, Identifiable {
    public var frameID: String
    /// Empty for anonymous functions.
    public var function: String
    public var scriptID: String
    public var url: String
    public var line: Int
    public var column: Int
    /// Innermost first.
    public var scopeChain: [DebugScope]
    public var thisValue: RemoteValue?

    public var id: String { frameID }

    public init(frameID: String, function: String, scriptID: String, url: String, line: Int, column: Int,
                scopeChain: [DebugScope] = [], thisValue: RemoteValue? = nil) {
        self.frameID = frameID
        self.function = function
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
        self.scopeChain = scopeChain
        self.thisValue = thisValue
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        frameID = try c.decode(String.self, forKey: .frameID)
        function = try c.decodeIfPresent(String.self, forKey: .function) ?? ""
        scriptID = try c.decodeIfPresent(String.self, forKey: .scriptID) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        line = try c.decode(Int.self, forKey: .line)
        column = try c.decodeIfPresent(Int.self, forKey: .column) ?? 1
        scopeChain = try c.decodeIfPresent([DebugScope].self, forKey: .scopeChain) ?? []
        thisValue = try c.decodeIfPresent(RemoteValue.self, forKey: .thisValue)
    }

    private enum CodingKeys: String, CodingKey {
        case frameID = "frameId"
        case scriptID = "scriptId"
        case thisValue = "this"
        case function, url, line, column, scopeChain
    }
}

/// Whether a target is paused, why, and its call stack (`debug.state`, `debug.paused`).
public struct DebugPauseState: Codable, Sendable, Equatable {
    public var targetID: String
    public var gen: Int
    public var paused: Bool
    public var reason: String?
    public var hitBreakpoints: [String]
    /// Innermost first.
    public var callFrames: [DebugCallFrame]
    /// The thrown value when the target paused on an exception or a rejected promise.
    public var exception: RemoteValue?

    public var pauseReason: DebugPauseReason? { reason.map { DebugPauseReason(rawValue: $0) ?? .other } }

    public init(targetID: String, gen: Int, paused: Bool, reason: DebugPauseReason? = nil, hitBreakpoints: [String] = [],
                callFrames: [DebugCallFrame] = [], exception: RemoteValue? = nil) {
        self.targetID = targetID
        self.gen = gen
        self.paused = paused
        self.reason = reason?.rawValue
        self.hitBreakpoints = hitBreakpoints
        self.callFrames = callFrames
        self.exception = exception
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        targetID = try c.decode(String.self, forKey: .targetID)
        gen = try c.decodeIfPresent(Int.self, forKey: .gen) ?? 0
        paused = try c.decode(Bool.self, forKey: .paused)
        reason = try c.decodeIfPresent(String.self, forKey: .reason)
        hitBreakpoints = try c.decodeIfPresent([String].self, forKey: .hitBreakpoints) ?? []
        callFrames = try c.decodeIfPresent([DebugCallFrame].self, forKey: .callFrames) ?? []
        exception = try c.decodeIfPresent(RemoteValue.self, forKey: .exception)
    }

    private enum CodingKeys: String, CodingKey {
        case targetID = "targetId"
        case gen, paused, reason, hitBreakpoints, callFrames, exception
    }
}

/// A property of an object or scope (`debug.variables`): a name and the value's RemoteValue fields.
public struct DebugProperty: Codable, Sendable, Equatable {
    public var name: String
    public var kind: String
    public var writable: Bool?
    public var value: RemoteValue

    public var propertyKind: DebugPropertyKind { DebugPropertyKind(rawValue: kind) ?? .property }

    public init(name: String, kind: String = DebugPropertyKind.property.rawValue, writable: Bool? = nil, value: RemoteValue) {
        self.name = name
        self.kind = kind
        self.writable = writable
        self.value = value
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? DebugPropertyKind.property.rawValue
        writable = try c.decodeIfPresent(Bool.self, forKey: .writable)
        value = try RemoteValue(from: decoder)
    }

    public func encode(to encoder: Encoder) throws {
        try value.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encode(kind, forKey: .kind)
        try c.encodeIfPresent(writable, forKey: .writable)
    }

    private enum CodingKeys: String, CodingKey {
        case name, kind, writable
    }
}

/// One page of `debug.variables`.
public struct DebugPropertyPage: Codable, Sendable, Equatable {
    public var properties: [DebugProperty]
    /// Properties of the object in all pages.
    public var total: Int

    public init(properties: [DebugProperty], total: Int? = nil) {
        self.properties = properties
        self.total = total ?? properties.count
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        properties = try c.decodeIfPresent([DebugProperty].self, forKey: .properties) ?? []
        total = try c.decodeIfPresent(Int.self, forKey: .total) ?? properties.count
    }

    private enum CodingKeys: String, CodingKey {
        case properties, total
    }
}

/// A frame of a console or exception stack.
public struct StackFrameInfo: Codable, Sendable, Equatable, Hashable {
    /// Empty for anonymous functions.
    public var function: String
    public var scriptID: String?
    public var url: String
    public var line: Int
    public var column: Int

    public init(function: String, scriptID: String? = nil, url: String, line: Int, column: Int) {
        self.function = function
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
    }

    private enum CodingKeys: String, CodingKey {
        case scriptID = "scriptId"
        case function, url, line, column
    }
}

/// A loaded script of a target (`debug.scripts`).
public struct DebugScript: Codable, Sendable, Equatable, Hashable, Identifiable {
    public var scriptID: String
    public var targetID: String
    public var url: String

    public var id: String { targetID + "|" + scriptID }

    public init(scriptID: String, targetID: String, url: String) {
        self.scriptID = scriptID
        self.targetID = targetID
        self.url = url
    }

    private enum CodingKeys: String, CodingKey {
        case scriptID = "scriptId"
        case targetID = "targetId"
        case url
    }
}

/// What an exception says: its one-line text, where it was thrown, and its stack.
public struct ExceptionInfo: Codable, Sendable, Equatable {
    public var text: String
    public var scriptID: String?
    public var url: String?
    public var line: Int?
    public var column: Int?
    public var stack: [StackFrameInfo]

    public init(text: String, scriptID: String? = nil, url: String? = nil, line: Int? = nil, column: Int? = nil,
                stack: [StackFrameInfo] = []) {
        self.text = text
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
        self.stack = stack
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = try c.decode(String.self, forKey: .text)
        scriptID = try c.decodeIfPresent(String.self, forKey: .scriptID)
        url = try c.decodeIfPresent(String.self, forKey: .url)
        line = try c.decodeIfPresent(Int.self, forKey: .line)
        column = try c.decodeIfPresent(Int.self, forKey: .column)
        stack = try c.decodeIfPresent([StackFrameInfo].self, forKey: .stack) ?? []
    }

    private enum CodingKeys: String, CodingKey {
        case scriptID = "scriptId"
        case text, url, line, column, stack
    }
}

/// Result of `debug.evaluate`: the value, or the thrown value with `exception`.
public struct DebugEvaluation: Codable, Sendable, Equatable {
    public var value: RemoteValue
    public var exception: ExceptionInfo?

    public init(value: RemoteValue, exception: ExceptionInfo? = nil) {
        self.value = value
        self.exception = exception
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        value = try RemoteValue(from: decoder)
        exception = try c.decodeIfPresent(ExceptionInfo.self, forKey: .exception)
    }

    public func encode(to encoder: Encoder) throws {
        try value.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(exception, forKey: .exception)
    }

    private enum CodingKeys: String, CodingKey {
        case exception
    }
}

/// A console message or uncaught exception of a session.
public struct ConsoleEntry: Codable, Sendable, Equatable, Identifiable {
    /// Rises by one per entry within the session, from 1.
    public var seq: Int
    public var time: Timestamp
    public var targetID: String
    public var source: String
    public var level: String
    public var text: String
    public var scriptID: String?
    public var url: String?
    public var line: Int?
    public var column: Int?
    public var stack: [StackFrameInfo]?

    public var id: Int { seq }
    public var consoleLevel: ConsoleLevel { ConsoleLevel(rawValue: level) ?? .log }
    public var consoleSource: ConsoleSource { ConsoleSource(rawValue: source) ?? .console }

    public init(seq: Int, time: Timestamp, targetID: String, source: ConsoleSource = .console, level: ConsoleLevel, text: String,
                scriptID: String? = nil, url: String? = nil, line: Int? = nil, column: Int? = nil, stack: [StackFrameInfo]? = nil) {
        self.seq = seq
        self.time = time
        self.targetID = targetID
        self.source = source.rawValue
        self.level = level.rawValue
        self.text = text
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
        self.stack = stack
    }

    private enum CodingKeys: String, CodingKey {
        case targetID = "targetId"
        case scriptID = "scriptId"
        case seq, time, source, level, text, url, line, column, stack
    }
}

/// One page of `debug.console`. Entries are oldest first.
public struct ConsolePage: Codable, Sendable, Equatable {
    public var entries: [ConsoleEntry]
    /// The `seq` to pass as `cursor` for the entries after these.
    public var nextCursor: Int
    public var hasMore: Bool
    /// Older entries than the first one are still kept by the daemon.
    public var hasEarlier: Bool
    /// Entries the daemon discarded from its buffer during the session.
    public var dropped: Int

    public init(entries: [ConsoleEntry], nextCursor: Int, hasMore: Bool = false, hasEarlier: Bool = false, dropped: Int = 0) {
        self.entries = entries
        self.nextCursor = nextCursor
        self.hasMore = hasMore
        self.hasEarlier = hasEarlier
        self.dropped = dropped
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        entries = try c.decodeIfPresent([ConsoleEntry].self, forKey: .entries) ?? []
        nextCursor = try c.decodeIfPresent(Int.self, forKey: .nextCursor) ?? (entries.last?.seq ?? 0)
        hasMore = try c.decodeIfPresent(Bool.self, forKey: .hasMore) ?? false
        hasEarlier = try c.decodeIfPresent(Bool.self, forKey: .hasEarlier) ?? false
        dropped = try c.decodeIfPresent(Int.self, forKey: .dropped) ?? 0
    }

    private enum CodingKeys: String, CodingKey {
        case entries, nextCursor, hasMore, hasEarlier, dropped
    }
}

/// Result of `debug.source`.
public struct DebugSource: Codable, Sendable, Equatable {
    public var scriptID: String
    public var url: String
    public var text: String
    public var truncated: Bool

    /// `text` split into lines; line N of the script is `lines[N - 1]`.
    public var lines: [String] {
        text.split(separator: "\n", omittingEmptySubsequences: false).map { line in
            line.hasSuffix("\r") ? String(line.dropLast()) : String(line)
        }
    }

    public init(scriptID: String, url: String, text: String, truncated: Bool = false) {
        self.scriptID = scriptID
        self.url = url
        self.text = text
        self.truncated = truncated
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        scriptID = try c.decodeIfPresent(String.self, forKey: .scriptID) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        text = try c.decode(String.self, forKey: .text)
        truncated = try c.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
    }

    private enum CodingKeys: String, CodingKey {
        case scriptID = "scriptId"
        case url, text, truncated
    }
}
