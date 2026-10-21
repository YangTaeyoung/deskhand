import Foundation

/// State of one host capability (`ready`, `unavailable`, `unsupported`, `needs_user`).
public struct Capability: Codable, Sendable, Equatable {
    public var state: String
    public var reason: String?
    /// Shells the `pty` capability can start (`powershell`, `pwsh`, `cmd`), when the agent reports them.
    public var shells: [String]?

    public init(state: String, reason: String? = nil, shells: [String]? = nil) {
        self.state = state
        self.reason = reason
        self.shells = shells
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        state = try c.decode(String.self, forKey: AnyKey("state"))
        reason = try c.decodeIfPresent(String.self, forKey: AnyKey("reason"))
        shells = try? c.decodeIfPresent([String].self, forKey: AnyKey("shells"))
    }
}

/// One display of a remote session.
public struct DisplayInfo: Codable, Sendable, Equatable {
    public var index: Int
    public var name: String
    public var adapter: String
    public var x: Int
    public var y: Int
    public var width: Int
    public var height: Int
    public var dpi: Int
    public var scale: Double
    public var primary: Bool
    public var refreshHz: Int

    public init(index: Int, name: String, adapter: String, x: Int, y: Int, width: Int, height: Int, dpi: Int, scale: Double, primary: Bool, refreshHz: Int) {
        self.index = index
        self.name = name
        self.adapter = adapter
        self.x = x
        self.y = y
        self.width = width
        self.height = height
        self.dpi = dpi
        self.scale = scale
        self.primary = primary
        self.refreshHz = refreshHz
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        index = try c.decode(Int.self, forKey: AnyKey("index"))
        name = try c.decode(String.self, forKey: AnyKey("name"))
        adapter = try c.decode(String.self, forKey: AnyKey("adapter"))
        x = try c.decode(Int.self, forKey: AnyKey("x"))
        y = try c.decode(Int.self, forKey: AnyKey("y"))
        width = try c.decode(Int.self, forKey: AnyKey("width"))
        height = try c.decode(Int.self, forKey: AnyKey("height"))
        dpi = try c.decode(Int.self, forKey: AnyKey("dpi"))
        scale = try c.decode(Double.self, forKey: AnyKey("scale"))
        primary = try c.decode(Bool.self, forKey: AnyKey("primary"))
        refreshHz = try c.decode(Int.self, forKey: AnyKey("refreshHz"))
    }
}

/// Remote session description.
public struct SessionInfo: Codable, Sendable, Equatable {
    public var sessionID: Int
    public var user: String
    public var sid: String?
    public var connectState: String
    public var protocolKind: String
    /// Nil when the agent does not report the lock state.
    public var locked: Bool?
    public var inputDesktop: String
    public var integrity: String?

    public init(sessionID: Int, user: String, sid: String? = nil, connectState: String, protocolKind: String, locked: Bool?, inputDesktop: String, integrity: String? = nil) {
        self.sessionID = sessionID
        self.user = user
        self.sid = sid
        self.connectState = connectState
        self.protocolKind = protocolKind
        self.locked = locked
        self.inputDesktop = inputDesktop
        self.integrity = integrity
    }

    private enum CodingKeys: String, CodingKey {
        case sessionID = "sessionId"
        case user
        case sid
        case connectState
        case protocolKind = "protocol"
        case locked
        case inputDesktop
        case integrity
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = try c.decode(Int.self, forKey: .sessionID)
        user = try c.decode(String.self, forKey: .user)
        sid = try c.decodeIfPresent(String.self, forKey: .sid)
        connectState = try c.decode(String.self, forKey: .connectState)
        protocolKind = try c.decode(String.self, forKey: .protocolKind)
        locked = try c.decodeIfPresent(Bool.self, forKey: .locked)
        inputDesktop = try c.decode(String.self, forKey: .inputDesktop)
        integrity = try c.decodeIfPresent(String.self, forKey: .integrity)
    }
}

/// A paired computer (`host.list`).
public struct Host: Codable, Sendable, Equatable {
    public var hostID: String
    public var name: String
    public var os: String
    public var arch: String
    public var address: String
    public var port: Int
    public var sshAlias: String
    public var state: String
    public var caps: HostCaps
    public var session: SessionInfo?
    public var displays: [DisplayInfo]
    public var owner: String
    public var ownerClient: String?
    public var agentVersion: String?
    public var connectionGeneration: Int
    public var lastSeen: Timestamp?

    public init(hostID: String, name: String, os: String, arch: String, address: String, port: Int, sshAlias: String, state: String, caps: HostCaps, session: SessionInfo? = nil, displays: [DisplayInfo] = [], owner: String, ownerClient: String? = nil, agentVersion: String? = nil, connectionGeneration: Int, lastSeen: Timestamp? = nil) {
        self.hostID = hostID
        self.name = name
        self.os = os
        self.arch = arch
        self.address = address
        self.port = port
        self.sshAlias = sshAlias
        self.state = state
        self.caps = caps
        self.session = session
        self.displays = displays
        self.owner = owner
        self.ownerClient = ownerClient
        self.agentVersion = agentVersion
        self.connectionGeneration = connectionGeneration
        self.lastSeen = lastSeen
    }

    private enum CodingKeys: String, CodingKey {
        case hostID = "hostId"
        case name
        case os
        case arch
        case address
        case port
        case sshAlias
        case state
        case caps
        case session
        case displays
        case owner
        case ownerClient
        case agentVersion
        case connectionGeneration
        case lastSeen
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        hostID = try c.decode(String.self, forKey: .hostID)
        name = try c.decode(String.self, forKey: .name)
        os = try c.decode(String.self, forKey: .os)
        arch = try c.decode(String.self, forKey: .arch)
        address = try c.decode(String.self, forKey: .address)
        port = try c.decode(Int.self, forKey: .port)
        sshAlias = try c.decode(String.self, forKey: .sshAlias)
        state = try c.decode(String.self, forKey: .state)
        caps = try c.decode(HostCaps.self, forKey: .caps)
        session = try c.decodeIfPresent(SessionInfo.self, forKey: .session)
        displays = try c.decodeIfPresent([DisplayInfo].self, forKey: .displays) ?? []
        owner = try c.decode(String.self, forKey: .owner)
        ownerClient = try c.decodeIfPresent(String.self, forKey: .ownerClient)
        agentVersion = try c.decodeIfPresent(String.self, forKey: .agentVersion)
        connectionGeneration = try c.decode(Int.self, forKey: .connectionGeneration)
        lastSeen = try c.decodeIfPresent(Timestamp.self, forKey: .lastSeen)
    }
}

/// Control ownership of a host (`control.*`).
public struct ControlState: Codable, Sendable, Equatable {
    public var owner: String
    public var ownerClient: String?
    public var gen: Int
    public var aiPaused: Bool

    public init(owner: String, ownerClient: String? = nil, gen: Int, aiPaused: Bool) {
        self.owner = owner
        self.ownerClient = ownerClient
        self.gen = gen
        self.aiPaused = aiPaused
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        owner = try c.decode(String.self, forKey: AnyKey("owner"))
        ownerClient = try c.decodeIfPresent(String.self, forKey: AnyKey("ownerClient"))
        gen = try c.decode(Int.self, forKey: AnyKey("gen"))
        aiPaused = try c.decode(Bool.self, forKey: AnyKey("aiPaused"))
    }
}

/// One check of `host.doctor`: `status` is `pass`, `fail`, `warn` or `skip`; `fix` is how to resolve a failure.
public struct DoctorCheck: Codable, Sendable, Equatable {
    public var id: String
    public var status: String
    public var detail: String
    public var fix: String?

    public init(id: String, status: String, detail: String, fix: String? = nil) {
        self.id = id
        self.status = status
        self.detail = detail
        self.fix = fix
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey("id"))
        status = try c.decode(String.self, forKey: AnyKey("status"))
        detail = try c.decodeIfPresent(String.self, forKey: AnyKey("detail")) ?? ""
        fix = try c.decodeIfPresent(String.self, forKey: AnyKey("fix"))
    }
}

struct AnyKey: CodingKey {
    var stringValue: String
    var intValue: Int? { nil }

    init(_ string: String) { stringValue = string }
    init?(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { nil }
}

/// A daemon timestamp: RFC 3339 UTC with milliseconds (`2026-10-20T12:34:56.789Z`).
public struct Timestamp: Codable, Sendable, Equatable, Hashable {
    public let date: Date

    nonisolated(unsafe) private static let formatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    private static let formatterLock = NSLock()

    public init(date: Date) { self.date = date }

    public init?(string: String) {
        Timestamp.formatterLock.lock()
        defer { Timestamp.formatterLock.unlock() }
        guard let d = Timestamp.formatter.date(from: string) else { return nil }
        date = d
    }

    public init(from decoder: Decoder) throws {
        let string = try decoder.singleValueContainer().decode(String.self)
        guard let value = Timestamp(string: string) else {
            throw DecodingError.dataCorrupted(.init(
                codingPath: decoder.codingPath, debugDescription: "not an RFC 3339 UTC timestamp with milliseconds: \(string)"))
        }
        self = value
    }

    public func encode(to encoder: Encoder) throws {
        Timestamp.formatterLock.lock()
        let string = Timestamp.formatter.string(from: date)
        Timestamp.formatterLock.unlock()
        var c = encoder.singleValueContainer()
        try c.encode(string)
    }
}

/// Capability states keyed by capability name (`screen`, `input`, `uia`, `exec`, `files`, `processes`, `pty`, `tunnel`, `diag`, `clipboard`); unknown names are kept.
public struct HostCaps: Codable, Sendable, Equatable {
    public var items: [String: Capability]

    public init(items: [String: Capability] = [:]) { self.items = items }

    public init(from decoder: Decoder) throws {
        items = try decoder.singleValueContainer().decode([String: Capability].self)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(items)
    }

    public subscript(name: String) -> Capability? { items[name] }
    public var screen: Capability? { items["screen"] }
    public var input: Capability? { items["input"] }
    public var uia: Capability? { items["uia"] }
    public var exec: Capability? { items["exec"] }
    public var files: Capability? { items["files"] }
    public var processes: Capability? { items["processes"] }
    public var pty: Capability? { items["pty"] }
    public var tunnel: Capability? { items["tunnel"] }
    public var diag: Capability? { items["diag"] }
    public var clipboard: Capability? { items["clipboard"] }
}

/// Result of `session.inspect`: the session, its displays and capabilities, the control state and the connection generation.
public struct SessionInspect: Decodable, Sendable, Equatable {
    public var session: SessionInfo
    public var displays: [DisplayInfo]
    public var caps: HostCaps
    public var control: ControlState?
    public var connectionGeneration: Int

    private enum CodingKeys: String, CodingKey {
        case displays, caps, connectionGeneration
    }

    public init(session: SessionInfo, displays: [DisplayInfo] = [], caps: HostCaps = HostCaps(), control: ControlState? = nil, connectionGeneration: Int) {
        self.session = session
        self.displays = displays
        self.caps = caps
        self.control = control
        self.connectionGeneration = connectionGeneration
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        session = try SessionInfo(from: decoder)
        displays = try c.decodeIfPresent([DisplayInfo].self, forKey: .displays) ?? []
        caps = try c.decodeIfPresent(HostCaps.self, forKey: .caps) ?? HostCaps()
        control = try? ControlState(from: decoder)
        connectionGeneration = try c.decode(Int.self, forKey: .connectionGeneration)
    }
}
