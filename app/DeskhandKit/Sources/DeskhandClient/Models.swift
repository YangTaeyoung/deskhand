import Foundation

/// State of one host capability.
public struct Capability: Codable, Sendable, Equatable {
    public var state: String
    public var reason: String?

    public init(state: String = "unknown", reason: String? = nil) {
        self.state = state
        self.reason = reason
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        state = try c.decodeIfPresent(String.self, forKey: AnyKey("state")) ?? "unknown"
        reason = try c.decodeIfPresent(String.self, forKey: AnyKey("reason"))
    }
}

/// Capability states reported for a host.
public struct HostCaps: Codable, Sendable, Equatable {
    public var screen: Capability?
    public var input: Capability?
    public var uia: Capability?
    public var exec: Capability?
    public var files: Capability?
    public var processes: Capability?
    public var pty: Capability?
    public var tunnel: Capability?
    public var diag: Capability?
    public var clipboard: Capability?

    public init(screen: Capability? = nil, input: Capability? = nil, uia: Capability? = nil, exec: Capability? = nil, files: Capability? = nil, processes: Capability? = nil, pty: Capability? = nil, tunnel: Capability? = nil, diag: Capability? = nil, clipboard: Capability? = nil) {
        self.screen = screen
        self.input = input
        self.uia = uia
        self.exec = exec
        self.files = files
        self.processes = processes
        self.pty = pty
        self.tunnel = tunnel
        self.diag = diag
        self.clipboard = clipboard
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        screen = try c.decodeIfPresent(Capability.self, forKey: AnyKey("screen"))
        input = try c.decodeIfPresent(Capability.self, forKey: AnyKey("input"))
        uia = try c.decodeIfPresent(Capability.self, forKey: AnyKey("uia"))
        exec = try c.decodeIfPresent(Capability.self, forKey: AnyKey("exec"))
        files = try c.decodeIfPresent(Capability.self, forKey: AnyKey("files"))
        processes = try c.decodeIfPresent(Capability.self, forKey: AnyKey("processes"))
        pty = try c.decodeIfPresent(Capability.self, forKey: AnyKey("pty"))
        tunnel = try c.decodeIfPresent(Capability.self, forKey: AnyKey("tunnel"))
        diag = try c.decodeIfPresent(Capability.self, forKey: AnyKey("diag"))
        clipboard = try c.decodeIfPresent(Capability.self, forKey: AnyKey("clipboard"))
    }
}

/// One display of a remote session.
public struct DisplayInfo: Codable, Sendable, Equatable {
    public var index: Int?
    public var name: String?
    public var adapter: String?
    public var x: Int?
    public var y: Int?
    public var width: Int?
    public var height: Int?
    public var dpi: Double?
    public var scale: Double?
    public var primary: Bool?
    public var refreshHz: Double?

    public init(index: Int? = nil, name: String? = nil, adapter: String? = nil, x: Int? = nil, y: Int? = nil, width: Int? = nil, height: Int? = nil, dpi: Double? = nil, scale: Double? = nil, primary: Bool? = nil, refreshHz: Double? = nil) {
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
        index = try c.decodeIfPresent(Int.self, forKey: AnyKey("index"))
        name = try c.decodeIfPresent(String.self, forKey: AnyKey("name"))
        adapter = try c.decodeIfPresent(String.self, forKey: AnyKey("adapter"))
        x = try c.decodeIfPresent(Int.self, forKey: AnyKey("x"))
        y = try c.decodeIfPresent(Int.self, forKey: AnyKey("y"))
        width = try c.decodeIfPresent(Int.self, forKey: AnyKey("width"))
        height = try c.decodeIfPresent(Int.self, forKey: AnyKey("height"))
        dpi = try c.decodeIfPresent(Double.self, forKey: AnyKey("dpi"))
        scale = try c.decodeIfPresent(Double.self, forKey: AnyKey("scale"))
        primary = try c.decodeIfPresent(Bool.self, forKey: AnyKey("primary"))
        refreshHz = try c.decodeIfPresent(Double.self, forKey: AnyKey("refreshHz"))
    }
}

/// Remote session description (`session.inspect`, `host.list`).
public struct SessionInfo: Codable, Sendable, Equatable {
    public var sessionID: String?
    public var user: String?
    public var sid: String?
    public var connectState: String?
    public var protocolKind: String?
    public var locked: Bool?
    public var inputDesktop: String?
    public var integrity: String?
    public var displays: [DisplayInfo]
    public var caps: HostCaps?
    public var owner: String?
    public var connectionGeneration: Int?

    public init(sessionID: String? = nil, user: String? = nil, sid: String? = nil, connectState: String? = nil, protocolKind: String? = nil, locked: Bool? = nil, inputDesktop: String? = nil, integrity: String? = nil, displays: [DisplayInfo] = [], caps: HostCaps? = nil, owner: String? = nil, connectionGeneration: Int? = nil) {
        self.sessionID = sessionID
        self.user = user
        self.sid = sid
        self.connectState = connectState
        self.protocolKind = protocolKind
        self.locked = locked
        self.inputDesktop = inputDesktop
        self.integrity = integrity
        self.displays = displays
        self.caps = caps
        self.owner = owner
        self.connectionGeneration = connectionGeneration
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
        case displays
        case caps
        case owner
        case connectionGeneration
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = try c.decodeIfPresent(String.self, forKey: .sessionID)
        user = try c.decodeIfPresent(String.self, forKey: .user)
        sid = try c.decodeIfPresent(String.self, forKey: .sid)
        connectState = try c.decodeIfPresent(String.self, forKey: .connectState)
        protocolKind = try c.decodeIfPresent(String.self, forKey: .protocolKind)
        locked = try c.decodeIfPresent(Bool.self, forKey: .locked)
        inputDesktop = try c.decodeIfPresent(String.self, forKey: .inputDesktop)
        integrity = try c.decodeIfPresent(String.self, forKey: .integrity)
        displays = try c.decodeIfPresent([DisplayInfo].self, forKey: .displays) ?? []
        caps = try c.decodeIfPresent(HostCaps.self, forKey: .caps)
        owner = try c.decodeIfPresent(String.self, forKey: .owner)
        connectionGeneration = try c.decodeIfPresent(Int.self, forKey: .connectionGeneration)
    }
}

/// A paired computer (`host.list`).
public struct Host: Codable, Sendable, Equatable {
    public var hostID: String
    public var name: String
    public var os: String?
    public var arch: String?
    public var address: String?
    public var state: String?
    public var caps: HostCaps?
    public var session: SessionInfo?
    public var owner: String?
    public var lastSeen: Timestamp?

    public init(hostID: String, name: String, os: String? = nil, arch: String? = nil, address: String? = nil, state: String? = nil, caps: HostCaps? = nil, session: SessionInfo? = nil, owner: String? = nil, lastSeen: Timestamp? = nil) {
        self.hostID = hostID
        self.name = name
        self.os = os
        self.arch = arch
        self.address = address
        self.state = state
        self.caps = caps
        self.session = session
        self.owner = owner
        self.lastSeen = lastSeen
    }

    private enum CodingKeys: String, CodingKey {
        case hostID = "hostId"
        case name
        case os
        case arch
        case address
        case state
        case caps
        case session
        case owner
        case lastSeen
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        hostID = try c.decode(String.self, forKey: .hostID)
        name = try c.decode(String.self, forKey: .name)
        os = try c.decodeIfPresent(String.self, forKey: .os)
        arch = try c.decodeIfPresent(String.self, forKey: .arch)
        address = try c.decodeIfPresent(String.self, forKey: .address)
        state = try c.decodeIfPresent(String.self, forKey: .state)
        caps = try c.decodeIfPresent(HostCaps.self, forKey: .caps)
        session = try c.decodeIfPresent(SessionInfo.self, forKey: .session)
        owner = try c.decodeIfPresent(String.self, forKey: .owner)
        lastSeen = try c.decodeIfPresent(Timestamp.self, forKey: .lastSeen)
    }
}

/// Control ownership of a host (`control.*`).
public struct ControlState: Codable, Sendable, Equatable {
    public var owner: String
    public var ownerClient: String?
    public var gen: Int
    public var aiPaused: Bool

    public init(owner: String, ownerClient: String? = nil, gen: Int = 0, aiPaused: Bool = false) {
        self.owner = owner
        self.ownerClient = ownerClient
        self.gen = gen
        self.aiPaused = aiPaused
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        owner = try c.decode(String.self, forKey: AnyKey("owner"))
        ownerClient = try c.decodeIfPresent(String.self, forKey: AnyKey("ownerClient"))
        gen = try c.decodeIfPresent(Int.self, forKey: AnyKey("gen")) ?? 0
        aiPaused = try c.decodeIfPresent(Bool.self, forKey: AnyKey("aiPaused")) ?? false
    }
}

/// Whether the requested action ran.
public struct ActionResult: Codable, Sendable, Equatable {
    public var executed: Bool?
    public var outcome: String?

    public init(executed: Bool? = nil, outcome: String? = nil) {
        self.executed = executed
        self.outcome = outcome
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        executed = try c.decodeIfPresent(Bool.self, forKey: AnyKey("executed"))
        outcome = try c.decodeIfPresent(String.self, forKey: AnyKey("outcome"))
    }
}

/// A file produced by an operation.
public struct Artifact: Codable, Sendable, Equatable {
    public var id: String?
    public var kind: String?
    public var name: String?
    public var path: String?
    public var size: Int?
    public var mime: String?
    public var sha256: String?

    public init(id: String? = nil, kind: String? = nil, name: String? = nil, path: String? = nil, size: Int? = nil, mime: String? = nil, sha256: String? = nil) {
        self.id = id
        self.kind = kind
        self.name = name
        self.path = path
        self.size = size
        self.mime = mime
        self.sha256 = sha256
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decodeIfPresent(String.self, forKey: AnyKey("id"))
        kind = try c.decodeIfPresent(String.self, forKey: AnyKey("kind"))
        name = try c.decodeIfPresent(String.self, forKey: AnyKey("name"))
        path = try c.decodeIfPresent(String.self, forKey: AnyKey("path"))
        size = try c.decodeIfPresent(Int.self, forKey: AnyKey("size"))
        mime = try c.decodeIfPresent(String.self, forKey: AnyKey("mime"))
        sha256 = try c.decodeIfPresent(String.self, forKey: AnyKey("sha256"))
    }
}

/// A background job (`jobs.*`).
public struct Job: Codable, Sendable, Equatable {
    public var jobID: String
    public var kind: String?
    public var status: String?
    public var hostID: String?
    public var startedAt: Timestamp?
    public var finishedAt: Timestamp?
    public var progress: Double?
    public var summary: String?

    public init(jobID: String, kind: String? = nil, status: String? = nil, hostID: String? = nil, startedAt: Timestamp? = nil, finishedAt: Timestamp? = nil, progress: Double? = nil, summary: String? = nil) {
        self.jobID = jobID
        self.kind = kind
        self.status = status
        self.hostID = hostID
        self.startedAt = startedAt
        self.finishedAt = finishedAt
        self.progress = progress
        self.summary = summary
    }

    private enum CodingKeys: String, CodingKey {
        case jobID = "jobId"
        case kind
        case status
        case hostID = "hostId"
        case startedAt
        case finishedAt
        case progress
        case summary
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = try c.decode(String.self, forKey: .jobID)
        kind = try c.decodeIfPresent(String.self, forKey: .kind)
        status = try c.decodeIfPresent(String.self, forKey: .status)
        hostID = try c.decodeIfPresent(String.self, forKey: .hostID)
        startedAt = try c.decodeIfPresent(Timestamp.self, forKey: .startedAt)
        finishedAt = try c.decodeIfPresent(Timestamp.self, forKey: .finishedAt)
        progress = try c.decodeIfPresent(Double.self, forKey: .progress)
        summary = try c.decodeIfPresent(String.self, forKey: .summary)
    }
}

/// A remote file or directory (`files.list`).
public struct FileEntry: Codable, Sendable, Equatable {
    public var name: String
    public var path: String?
    public var isDir: Bool
    public var size: Int?
    public var mtime: Timestamp?
    public var mode: String?

    public init(name: String, path: String? = nil, isDir: Bool = false, size: Int? = nil, mtime: Timestamp? = nil, mode: String? = nil) {
        self.name = name
        self.path = path
        self.isDir = isDir
        self.size = size
        self.mtime = mtime
        self.mode = mode
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        path = try c.decodeIfPresent(String.self, forKey: AnyKey("path"))
        isDir = try c.decodeIfPresent(Bool.self, forKey: AnyKey("isDir")) ?? false
        size = try c.decodeIfPresent(Int.self, forKey: AnyKey("size"))
        mtime = try c.decodeIfPresent(Timestamp.self, forKey: AnyKey("mtime"))
        mode = try c.decodeIfPresent(String.self, forKey: AnyKey("mode"))
    }
}

/// Per-file state of a transfer.
public struct TransferFile: Codable, Sendable, Equatable {
    public var path: String?
    public var state: String?
    public var bytesDone: Int?
    public var bytesTotal: Int?
    public var outcome: String?

    public init(path: String? = nil, state: String? = nil, bytesDone: Int? = nil, bytesTotal: Int? = nil, outcome: String? = nil) {
        self.path = path
        self.state = state
        self.bytesDone = bytesDone
        self.bytesTotal = bytesTotal
        self.outcome = outcome
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        path = try c.decodeIfPresent(String.self, forKey: AnyKey("path"))
        state = try c.decodeIfPresent(String.self, forKey: AnyKey("state"))
        bytesDone = try c.decodeIfPresent(Int.self, forKey: AnyKey("bytesDone"))
        bytesTotal = try c.decodeIfPresent(Int.self, forKey: AnyKey("bytesTotal"))
        outcome = try c.decodeIfPresent(String.self, forKey: AnyKey("outcome"))
    }
}

/// A file transfer (`transfer.*`).
public struct Transfer: Codable, Sendable, Equatable {
    public var transferID: String
    public var direction: String?
    public var hostID: String?
    public var local: String?
    public var remote: String?
    public var state: String?
    public var bytesDone: Int?
    public var bytesTotal: Int?
    public var progress: Double?
    public var outcome: String?
    public var files: [TransferFile]

    public init(transferID: String, direction: String? = nil, hostID: String? = nil, local: String? = nil, remote: String? = nil, state: String? = nil, bytesDone: Int? = nil, bytesTotal: Int? = nil, progress: Double? = nil, outcome: String? = nil, files: [TransferFile] = []) {
        self.transferID = transferID
        self.direction = direction
        self.hostID = hostID
        self.local = local
        self.remote = remote
        self.state = state
        self.bytesDone = bytesDone
        self.bytesTotal = bytesTotal
        self.progress = progress
        self.outcome = outcome
        self.files = files
    }

    private enum CodingKeys: String, CodingKey {
        case transferID = "transferId"
        case direction
        case hostID = "hostId"
        case local
        case remote
        case state
        case bytesDone
        case bytesTotal
        case progress
        case outcome
        case files
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        transferID = try c.decode(String.self, forKey: .transferID)
        direction = try c.decodeIfPresent(String.self, forKey: .direction)
        hostID = try c.decodeIfPresent(String.self, forKey: .hostID)
        local = try c.decodeIfPresent(String.self, forKey: .local)
        remote = try c.decodeIfPresent(String.self, forKey: .remote)
        state = try c.decodeIfPresent(String.self, forKey: .state)
        bytesDone = try c.decodeIfPresent(Int.self, forKey: .bytesDone)
        bytesTotal = try c.decodeIfPresent(Int.self, forKey: .bytesTotal)
        progress = try c.decodeIfPresent(Double.self, forKey: .progress)
        outcome = try c.decodeIfPresent(String.self, forKey: .outcome)
        files = try c.decodeIfPresent([TransferFile].self, forKey: .files) ?? []
    }
}

/// A remote process (`proc.list`, `diag.processes`).
public struct ProcessInfo: Codable, Sendable, Equatable {
    public var pid: Int
    public var ppid: Int?
    public var name: String?
    public var user: String?
    public var cpu: Double?
    public var memory: Int?
    public var cmd: String?

    public init(pid: Int, ppid: Int? = nil, name: String? = nil, user: String? = nil, cpu: Double? = nil, memory: Int? = nil, cmd: String? = nil) {
        self.pid = pid
        self.ppid = ppid
        self.name = name
        self.user = user
        self.cpu = cpu
        self.memory = memory
        self.cmd = cmd
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        pid = try c.decode(Int.self, forKey: AnyKey("pid"))
        ppid = try c.decodeIfPresent(Int.self, forKey: AnyKey("ppid"))
        name = try c.decodeIfPresent(String.self, forKey: AnyKey("name"))
        user = try c.decodeIfPresent(String.self, forKey: AnyKey("user"))
        cpu = try c.decodeIfPresent(Double.self, forKey: AnyKey("cpu"))
        memory = try c.decodeIfPresent(Int.self, forKey: AnyKey("memory"))
        cmd = try c.decodeIfPresent(String.self, forKey: AnyKey("cmd"))
    }
}

/// System diagnostics (`diag.system`).
public struct DiagSystem: Codable, Sendable, Equatable {
    public var os: String?
    public var build: String?
    public var cpu: JSONValue?
    public var memory: JSONValue?
    public var disk: JSONValue?
    public var gpu: JSONValue?
    public var uptime: JSONValue?

    public init(os: String? = nil, build: String? = nil, cpu: JSONValue? = nil, memory: JSONValue? = nil, disk: JSONValue? = nil, gpu: JSONValue? = nil, uptime: JSONValue? = nil) {
        self.os = os
        self.build = build
        self.cpu = cpu
        self.memory = memory
        self.disk = disk
        self.gpu = gpu
        self.uptime = uptime
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        os = try c.decodeIfPresent(String.self, forKey: AnyKey("os"))
        build = try c.decodeIfPresent(String.self, forKey: AnyKey("build"))
        cpu = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("cpu"))
        memory = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("memory"))
        disk = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("disk"))
        gpu = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("gpu"))
        uptime = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("uptime"))
    }
}

/// A remote event log entry (`diag.events`).
public struct EventLogEntry: Codable, Sendable, Equatable {
    public var time: Timestamp?
    public var level: String?
    public var provider: String?
    public var eventID: Int?
    public var message: String?
    public var log: String?

    public init(time: Timestamp? = nil, level: String? = nil, provider: String? = nil, eventID: Int? = nil, message: String? = nil, log: String? = nil) {
        self.time = time
        self.level = level
        self.provider = provider
        self.eventID = eventID
        self.message = message
        self.log = log
    }

    private enum CodingKeys: String, CodingKey {
        case time
        case level
        case provider
        case eventID = "eventId"
        case message
        case log
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        time = try c.decodeIfPresent(Timestamp.self, forKey: .time)
        level = try c.decodeIfPresent(String.self, forKey: .level)
        provider = try c.decodeIfPresent(String.self, forKey: .provider)
        eventID = try c.decodeIfPresent(Int.self, forKey: .eventID)
        message = try c.decodeIfPresent(String.self, forKey: .message)
        log = try c.decodeIfPresent(String.self, forKey: .log)
    }
}

/// A debuggable target (`debug.electron.launch`, `debug.attach`).
public struct DebugTarget: Codable, Sendable, Equatable {
    public var id: String
    public var role: String?
    public var url: String?
    public var title: String?

    public init(id: String, role: String? = nil, url: String? = nil, title: String? = nil) {
        self.id = id
        self.role = role
        self.url = url
        self.title = title
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey("id"))
        role = try c.decodeIfPresent(String.self, forKey: AnyKey("role"))
        url = try c.decodeIfPresent(String.self, forKey: AnyKey("url"))
        title = try c.decodeIfPresent(String.self, forKey: AnyKey("title"))
    }
}

/// Result of `debug.electron.launch` and `debug.attach`.
public struct DebugLaunch: Codable, Sendable, Equatable {
    public var debugID: String
    public var pid: Int?
    public var targets: [DebugTarget]

    public init(debugID: String, pid: Int? = nil, targets: [DebugTarget] = []) {
        self.debugID = debugID
        self.pid = pid
        self.targets = targets
    }

    private enum CodingKeys: String, CodingKey {
        case debugID = "debugId"
        case pid
        case targets
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        debugID = try c.decode(String.self, forKey: .debugID)
        pid = try c.decodeIfPresent(Int.self, forKey: .pid)
        targets = try c.decodeIfPresent([DebugTarget].self, forKey: .targets) ?? []
    }
}

/// A paused call frame (`debug.state`).
public struct CallFrame: Codable, Sendable, Equatable {
    public var functionName: String?
    public var url: String?
    public var line: Int?
    public var column: Int?

    public init(functionName: String? = nil, url: String? = nil, line: Int? = nil, column: Int? = nil) {
        self.functionName = functionName
        self.url = url
        self.line = line
        self.column = column
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        functionName = try c.decodeIfPresent(String.self, forKey: AnyKey("functionName"))
        url = try c.decodeIfPresent(String.self, forKey: AnyKey("url"))
        line = try c.decodeIfPresent(Int.self, forKey: AnyKey("line"))
        column = try c.decodeIfPresent(Int.self, forKey: AnyKey("column"))
    }
}

/// Debugger state (`debug.state`).
public struct DebugState: Codable, Sendable, Equatable {
    public var paused: Bool
    public var reason: String?
    public var callFrames: [CallFrame]
    public var scopes: [JSONValue]

    public init(paused: Bool = false, reason: String? = nil, callFrames: [CallFrame] = [], scopes: [JSONValue] = []) {
        self.paused = paused
        self.reason = reason
        self.callFrames = callFrames
        self.scopes = scopes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        paused = try c.decodeIfPresent(Bool.self, forKey: AnyKey("paused")) ?? false
        reason = try c.decodeIfPresent(String.self, forKey: AnyKey("reason"))
        callFrames = try c.decodeIfPresent([CallFrame].self, forKey: AnyKey("callFrames")) ?? []
        scopes = try c.decodeIfPresent([JSONValue].self, forKey: AnyKey("scopes")) ?? []
    }
}

/// One step of a QA run.
public struct QAStep: Codable, Sendable, Equatable {
    public var index: Int?
    public var name: String?
    public var status: String?
    public var detail: String?
    public var durationMs: Int?
    public var artifacts: [Artifact]

    public init(index: Int? = nil, name: String? = nil, status: String? = nil, detail: String? = nil, durationMs: Int? = nil, artifacts: [Artifact] = []) {
        self.index = index
        self.name = name
        self.status = status
        self.detail = detail
        self.durationMs = durationMs
        self.artifacts = artifacts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        index = try c.decodeIfPresent(Int.self, forKey: AnyKey("index"))
        name = try c.decodeIfPresent(String.self, forKey: AnyKey("name"))
        status = try c.decodeIfPresent(String.self, forKey: AnyKey("status"))
        detail = try c.decodeIfPresent(String.self, forKey: AnyKey("detail"))
        durationMs = try c.decodeIfPresent(Int.self, forKey: AnyKey("durationMs"))
        artifacts = try c.decodeIfPresent([Artifact].self, forKey: AnyKey("artifacts")) ?? []
    }
}

/// A QA scenario run (`qa.get`, `qa.list`).
public struct QARun: Codable, Sendable, Equatable {
    public var runID: String
    public var status: String?
    public var scenario: String?
    public var hostID: String?
    public var startedAt: Timestamp?
    public var finishedAt: Timestamp?
    public var steps: [QAStep]

    public init(runID: String, status: String? = nil, scenario: String? = nil, hostID: String? = nil, startedAt: Timestamp? = nil, finishedAt: Timestamp? = nil, steps: [QAStep] = []) {
        self.runID = runID
        self.status = status
        self.scenario = scenario
        self.hostID = hostID
        self.startedAt = startedAt
        self.finishedAt = finishedAt
        self.steps = steps
    }

    private enum CodingKeys: String, CodingKey {
        case runID = "runId"
        case status
        case scenario
        case hostID = "hostId"
        case startedAt
        case finishedAt
        case steps
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runID = try c.decode(String.self, forKey: .runID)
        status = try c.decodeIfPresent(String.self, forKey: .status)
        scenario = try c.decodeIfPresent(String.self, forKey: .scenario)
        hostID = try c.decodeIfPresent(String.self, forKey: .hostID)
        startedAt = try c.decodeIfPresent(Timestamp.self, forKey: .startedAt)
        finishedAt = try c.decodeIfPresent(Timestamp.self, forKey: .finishedAt)
        steps = try c.decodeIfPresent([QAStep].self, forKey: .steps) ?? []
    }
}

/// A human or AI action record (`timeline.list`).
public struct TimelineEntry: Codable, Sendable, Equatable {
    public var ts: Timestamp?
    public var actor: String?
    public var clientID: String?
    public var hostID: String?
    public var action: String?
    public var summary: String?
    public var detail: JSONValue?

    public init(ts: Timestamp? = nil, actor: String? = nil, clientID: String? = nil, hostID: String? = nil, action: String? = nil, summary: String? = nil, detail: JSONValue? = nil) {
        self.ts = ts
        self.actor = actor
        self.clientID = clientID
        self.hostID = hostID
        self.action = action
        self.summary = summary
        self.detail = detail
    }

    private enum CodingKeys: String, CodingKey {
        case ts
        case actor
        case clientID = "clientId"
        case hostID = "hostId"
        case action
        case summary
        case detail
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ts = try c.decodeIfPresent(Timestamp.self, forKey: .ts)
        actor = try c.decodeIfPresent(String.self, forKey: .actor)
        clientID = try c.decodeIfPresent(String.self, forKey: .clientID)
        hostID = try c.decodeIfPresent(String.self, forKey: .hostID)
        action = try c.decodeIfPresent(String.self, forKey: .action)
        summary = try c.decodeIfPresent(String.self, forKey: .summary)
        detail = try c.decodeIfPresent(JSONValue.self, forKey: .detail)
    }
}
struct AnyKey: CodingKey {
    var stringValue: String
    var intValue: Int? { nil }

    init(_ string: String) { stringValue = string }
    init?(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { nil }
}

/// A daemon timestamp: an RFC 3339 string or a Unix time in seconds or milliseconds.
public struct Timestamp: Codable, Sendable, Equatable {
    public let raw: JSONValue
    public var date: Date? {
        switch raw {
        case .string(let s):
            return Timestamp.parse(s)
        case .int(let n):
            return Timestamp.date(fromEpoch: Double(n))
        case .double(let n):
            return Timestamp.date(fromEpoch: n)
        default:
            return nil
        }
    }

    public init(raw: JSONValue) { self.raw = raw }

    public init(from decoder: Decoder) throws {
        raw = try JSONValue(from: decoder)
    }

    public func encode(to encoder: Encoder) throws {
        try raw.encode(to: encoder)
    }

    private static func date(fromEpoch value: Double) -> Date {
        Date(timeIntervalSince1970: value > 1e11 ? value / 1000 : value)
    }

    private static func parse(_ string: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = fractional.date(from: string) { return d }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return plain.date(from: string)
    }
}

public enum EnvelopeStatus: String, Codable, Sendable, Equatable {
    case succeeded
    case failed
    case running
    case waitingForUser = "waiting_for_user"
    case unknown
    case cancelled

    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = EnvelopeStatus(rawValue: raw) ?? .unknown
    }
}

/// The `error` member of a result envelope.
public struct EnvelopeError: Codable, Sendable, Equatable {
    public var code: String?
    public var message: String?
    public var retryable: Bool?
    public var nextAction: String?

    public init(code: String? = nil, message: String? = nil, retryable: Bool? = nil, nextAction: String? = nil) {
        self.code = code
        self.message = message
        self.retryable = retryable
        self.nextAction = nextAction
    }

    public init(from decoder: Decoder) throws {
        if let s = try? decoder.singleValueContainer().decode(String.self) {
            self.init(message: s)
            return
        }
        let c = try decoder.container(keyedBy: AnyKey.self)
        self.init(
            code: try c.decodeIfPresent(String.self, forKey: AnyKey("code")),
            message: try c.decodeIfPresent(String.self, forKey: AnyKey("message")),
            retryable: try c.decodeIfPresent(Bool.self, forKey: AnyKey("retryable")),
            nextAction: try c.decodeIfPresent(String.self, forKey: AnyKey("nextAction")))
    }
}

/// Common result envelope (local-api.md section 2) plus the method-specific payload decoded from the same object.
public struct Envelope<Payload: Decodable & Sendable>: Decodable, Sendable {
    public let schemaVersion: Int?
    public let requestID: String?
    public let jobID: String?
    public let hostID: String?
    public let sessionID: String?
    public let connectionGeneration: Int?
    public let status: EnvelopeStatus
    public let action: ActionResult?
    public let error: EnvelopeError?
    public let nextAction: String?
    public let artifacts: [Artifact]
    public let payload: Payload

    private enum CodingKeys: String, CodingKey {
        case schemaVersion
        case requestID = "requestId"
        case jobID = "jobId"
        case hostID = "hostId"
        case sessionID = "sessionId"
        case connectionGeneration
        case status
        case action
        case error
        case nextAction
        case artifacts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        schemaVersion = try c.decodeIfPresent(Int.self, forKey: .schemaVersion)
        requestID = try c.decodeIfPresent(String.self, forKey: .requestID)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        hostID = try c.decodeIfPresent(String.self, forKey: .hostID)
        sessionID = try c.decodeIfPresent(String.self, forKey: .sessionID)
        connectionGeneration = try c.decodeIfPresent(Int.self, forKey: .connectionGeneration)
        status = try c.decodeIfPresent(EnvelopeStatus.self, forKey: .status) ?? .unknown
        action = try c.decodeIfPresent(ActionResult.self, forKey: .action)
        error = try c.decodeIfPresent(EnvelopeError.self, forKey: .error)
        nextAction = try? c.decodeIfPresent(String.self, forKey: .nextAction)
        artifacts = try c.decodeIfPresent([Artifact].self, forKey: .artifacts) ?? []
        payload = try Payload(from: decoder)
    }
}

/// An event notification (`event` method) delivered to `DeskhandClient.events`.
public struct DaemonEvent: Sendable, Equatable {
    public let seq: Int
    public let ts: Timestamp?
    public let hostID: String?
    public let kind: String
    /// Every member of the notification params, with the original key spelling.
    public let payload: [String: JSONValue]

    public init(seq: Int, ts: Timestamp? = nil, hostID: String? = nil, kind: String, payload: [String: JSONValue] = [:]) {
        self.seq = seq
        self.ts = ts
        self.hostID = hostID
        self.kind = kind
        self.payload = payload
    }

    init?(params: JSONValue) {
        guard case .object(let o) = params, let kind = o["kind"]?.stringValue else { return nil }
        self.init(
            seq: o["seq"]?.intValue ?? 0,
            ts: o["ts"].map(Timestamp.init(raw:)),
            hostID: o["host_id"]?.stringValue,
            kind: kind,
            payload: o)
    }
}
