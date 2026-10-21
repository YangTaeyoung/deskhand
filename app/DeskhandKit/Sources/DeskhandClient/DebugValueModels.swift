import Foundation

// Debugger models of local-api.md §5 and §9. Lines and columns count from 1.

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
    /// The client that asked for the pause or step.
    public var requestedBy: DebugActor?
    public var hitBreakpoints: [String]
    /// Innermost first.
    public var callFrames: [DebugCallFrame]
    /// The thrown value when the target paused on an exception or a rejected promise.
    public var exception: RemoteValue?

    public var pauseReason: DebugPauseReason? { reason.map { DebugPauseReason(rawValue: $0) ?? .other } }

    public init(targetID: String, gen: Int, paused: Bool, reason: DebugPauseReason? = nil, requestedBy: DebugActor? = nil,
                hitBreakpoints: [String] = [], callFrames: [DebugCallFrame] = [], exception: RemoteValue? = nil) {
        self.targetID = targetID
        self.gen = gen
        self.paused = paused
        self.reason = reason?.rawValue
        self.requestedBy = requestedBy
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
        requestedBy = try c.decodeIfPresent(DebugActor.self, forKey: .requestedBy)
        hitBreakpoints = try c.decodeIfPresent([String].self, forKey: .hitBreakpoints) ?? []
        callFrames = try c.decodeIfPresent([DebugCallFrame].self, forKey: .callFrames) ?? []
        exception = try c.decodeIfPresent(RemoteValue.self, forKey: .exception)
    }

    private enum CodingKeys: String, CodingKey {
        case targetID = "targetId"
        case gen, paused, reason, requestedBy, hitBreakpoints, callFrames, exception
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
