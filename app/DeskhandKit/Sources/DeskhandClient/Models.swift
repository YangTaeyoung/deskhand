import Foundation

/// State of one host capability (`ready`, `unavailable`, `unsupported`, `needs_user`).
public struct Capability: Codable, Sendable, Equatable {
    public var state: String
    public var reason: String?

    public init(state: String, reason: String? = nil) {
        self.state = state
        self.reason = reason
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        state = try c.decode(String.self, forKey: AnyKey("state"))
        reason = try c.decodeIfPresent(String.self, forKey: AnyKey("reason"))
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
    public var locked: Bool
    public var inputDesktop: String
    public var integrity: String?

    public init(sessionID: Int, user: String, sid: String? = nil, connectState: String, protocolKind: String, locked: Bool, inputDesktop: String, integrity: String? = nil) {
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
        locked = try c.decode(Bool.self, forKey: .locked)
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
    public var artifactID: String
    public var jobID: String?
    public var kind: String
    public var name: String
    public var path: String
    public var size: Int
    public var mime: String
    public var sha256: String
    public var createdAt: Timestamp

    public init(artifactID: String, jobID: String? = nil, kind: String, name: String, path: String, size: Int, mime: String, sha256: String, createdAt: Timestamp) {
        self.artifactID = artifactID
        self.jobID = jobID
        self.kind = kind
        self.name = name
        self.path = path
        self.size = size
        self.mime = mime
        self.sha256 = sha256
        self.createdAt = createdAt
    }

    private enum CodingKeys: String, CodingKey {
        case artifactID = "artifactId"
        case jobID = "jobId"
        case kind
        case name
        case path
        case size
        case mime
        case sha256
        case createdAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        artifactID = try c.decode(String.self, forKey: .artifactID)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        kind = try c.decode(String.self, forKey: .kind)
        name = try c.decode(String.self, forKey: .name)
        path = try c.decode(String.self, forKey: .path)
        size = try c.decode(Int.self, forKey: .size)
        mime = try c.decode(String.self, forKey: .mime)
        sha256 = try c.decode(String.self, forKey: .sha256)
        createdAt = try c.decode(Timestamp.self, forKey: .createdAt)
    }
}

/// A background job (`jobs.*`).
public struct Job: Codable, Sendable, Equatable {
    public var jobID: String
    public var hostID: String
    public var kind: String
    public var status: String
    public var requestID: String?
    public var summary: String
    public var progress: Double?
    public var createdAt: Timestamp
    public var updatedAt: Timestamp
    public var finishedAt: Timestamp?
    public var result: JSONValue?

    public init(jobID: String, hostID: String, kind: String, status: String, requestID: String? = nil, summary: String, progress: Double? = nil, createdAt: Timestamp, updatedAt: Timestamp, finishedAt: Timestamp? = nil, result: JSONValue? = nil) {
        self.jobID = jobID
        self.hostID = hostID
        self.kind = kind
        self.status = status
        self.requestID = requestID
        self.summary = summary
        self.progress = progress
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.finishedAt = finishedAt
        self.result = result
    }

    private enum CodingKeys: String, CodingKey {
        case jobID = "jobId"
        case hostID = "hostId"
        case kind
        case status
        case requestID = "requestId"
        case summary
        case progress
        case createdAt
        case updatedAt
        case finishedAt
        case result
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = try c.decode(String.self, forKey: .jobID)
        hostID = try c.decode(String.self, forKey: .hostID)
        kind = try c.decode(String.self, forKey: .kind)
        status = try c.decode(String.self, forKey: .status)
        requestID = try c.decodeIfPresent(String.self, forKey: .requestID)
        summary = try c.decode(String.self, forKey: .summary)
        progress = try c.decodeIfPresent(Double.self, forKey: .progress)
        createdAt = try c.decode(Timestamp.self, forKey: .createdAt)
        updatedAt = try c.decode(Timestamp.self, forKey: .updatedAt)
        finishedAt = try c.decodeIfPresent(Timestamp.self, forKey: .finishedAt)
        result = try c.decodeIfPresent(JSONValue.self, forKey: .result)
    }
}

/// A remote file or directory (`files.list`).
public struct FileEntry: Codable, Sendable, Equatable {
    public var name: String
    public var path: String
    public var dir: Bool
    public var size: Int
    public var mtime: Timestamp
    public var link: Bool
    public var hidden: Bool?
    public var readonly: Bool?

    public init(name: String, path: String, dir: Bool, size: Int, mtime: Timestamp, link: Bool, hidden: Bool? = nil, readonly: Bool? = nil) {
        self.name = name
        self.path = path
        self.dir = dir
        self.size = size
        self.mtime = mtime
        self.link = link
        self.hidden = hidden
        self.readonly = readonly
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        path = try c.decode(String.self, forKey: AnyKey("path"))
        dir = try c.decode(Bool.self, forKey: AnyKey("dir"))
        size = try c.decode(Int.self, forKey: AnyKey("size"))
        mtime = try c.decode(Timestamp.self, forKey: AnyKey("mtime"))
        link = try c.decode(Bool.self, forKey: AnyKey("link"))
        hidden = try c.decodeIfPresent(Bool.self, forKey: AnyKey("hidden"))
        readonly = try c.decodeIfPresent(Bool.self, forKey: AnyKey("readonly"))
    }
}

/// Per-file state of a transfer.
public struct TransferFile: Codable, Sendable, Equatable {
    public var rel: String
    public var size: Int
    public var bytesDone: Int
    public var state: String
    public var outcome: String?
    public var sha256: String?
    /// `{code, message}` (local-api.md §8); a plain string is read as the message.
    public var error: EnvelopeError?

    public init(rel: String, size: Int, bytesDone: Int, state: String, outcome: String? = nil, sha256: String? = nil, error: EnvelopeError? = nil) {
        self.rel = rel
        self.size = size
        self.bytesDone = bytesDone
        self.state = state
        self.outcome = outcome
        self.sha256 = sha256
        self.error = error
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        rel = try c.decode(String.self, forKey: AnyKey("rel"))
        size = try c.decode(Int.self, forKey: AnyKey("size"))
        bytesDone = try c.decode(Int.self, forKey: AnyKey("bytesDone"))
        state = try c.decode(String.self, forKey: AnyKey("state"))
        outcome = try c.decodeIfPresent(String.self, forKey: AnyKey("outcome"))
        sha256 = try c.decodeIfPresent(String.self, forKey: AnyKey("sha256"))
        error = try c.decodeIfPresent(EnvelopeError.self, forKey: AnyKey("error"))
    }
}

/// A file transfer (`transfer.*`). `rate_bps` and the byte counts are bytes (local-api.md §8).
public struct Transfer: Codable, Sendable, Equatable {
    public var transferID: String
    public var hostID: String
    public var direction: String
    public var local: String
    public var remote: String
    public var onConflict: String
    public var state: String
    public var bytesDone: Int
    public var bytesTotal: Int
    public var filesDone: Int
    public var filesTotal: Int
    public var rateBps: Int
    public var outcome: String?
    /// `{code, message}` (local-api.md §8); a plain string is read as the message.
    public var error: EnvelopeError?
    public var files: [TransferFile]
    /// Increases by one on every change of the transfer; only a larger `rev` is newer.
    public var rev: Int
    public var updatedAt: Timestamp

    public init(transferID: String, hostID: String, direction: String, local: String, remote: String, onConflict: String, state: String, bytesDone: Int, bytesTotal: Int, filesDone: Int, filesTotal: Int, rateBps: Int, outcome: String? = nil, error: EnvelopeError? = nil, files: [TransferFile] = [], rev: Int = 1, updatedAt: Timestamp = Timestamp(date: .now)) {
        self.transferID = transferID
        self.hostID = hostID
        self.direction = direction
        self.local = local
        self.remote = remote
        self.onConflict = onConflict
        self.state = state
        self.bytesDone = bytesDone
        self.bytesTotal = bytesTotal
        self.filesDone = filesDone
        self.filesTotal = filesTotal
        self.rateBps = rateBps
        self.outcome = outcome
        self.error = error
        self.files = files
        self.rev = rev
        self.updatedAt = updatedAt
    }

    private enum CodingKeys: String, CodingKey {
        case transferID = "transferId"
        case hostID = "hostId"
        case direction
        case local
        case remote
        case onConflict
        case state
        case bytesDone
        case bytesTotal
        case filesDone
        case filesTotal
        case rateBps
        case outcome
        case error
        case files
        case rev
        case updatedAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        transferID = try c.decode(String.self, forKey: .transferID)
        hostID = try c.decode(String.self, forKey: .hostID)
        direction = try c.decode(String.self, forKey: .direction)
        local = try c.decode(String.self, forKey: .local)
        remote = try c.decode(String.self, forKey: .remote)
        onConflict = try c.decode(String.self, forKey: .onConflict)
        state = try c.decode(String.self, forKey: .state)
        bytesDone = try c.decode(Int.self, forKey: .bytesDone)
        bytesTotal = try c.decode(Int.self, forKey: .bytesTotal)
        filesDone = try c.decode(Int.self, forKey: .filesDone)
        filesTotal = try c.decode(Int.self, forKey: .filesTotal)
        rateBps = try c.decode(Int.self, forKey: .rateBps)
        outcome = try c.decodeIfPresent(String.self, forKey: .outcome)
        error = try c.decodeIfPresent(EnvelopeError.self, forKey: .error)
        files = try c.decodeIfPresent([TransferFile].self, forKey: .files) ?? []
        rev = try c.decode(Int.self, forKey: .rev)
        updatedAt = try c.decode(Timestamp.self, forKey: .updatedAt)
    }
}

/// A remote process (`proc.list`).
public struct ProcessInfo: Codable, Sendable, Equatable {
    public var pid: Int
    public var ppid: Int
    public var name: String
    public var path: String?
    public var cmd: String?
    public var memKb: Int
    public var cpuPct: Double?
    public var user: String?
    public var startedByDeskhand: Bool

    public init(pid: Int, ppid: Int, name: String, path: String? = nil, cmd: String? = nil, memKb: Int, cpuPct: Double? = nil, user: String? = nil, startedByDeskhand: Bool) {
        self.pid = pid
        self.ppid = ppid
        self.name = name
        self.path = path
        self.cmd = cmd
        self.memKb = memKb
        self.cpuPct = cpuPct
        self.user = user
        self.startedByDeskhand = startedByDeskhand
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        pid = try c.decode(Int.self, forKey: AnyKey("pid"))
        ppid = try c.decode(Int.self, forKey: AnyKey("ppid"))
        name = try c.decode(String.self, forKey: AnyKey("name"))
        path = try c.decodeIfPresent(String.self, forKey: AnyKey("path"))
        cmd = try c.decodeIfPresent(String.self, forKey: AnyKey("cmd"))
        memKb = try c.decode(Int.self, forKey: AnyKey("memKb"))
        cpuPct = try c.decodeIfPresent(Double.self, forKey: AnyKey("cpuPct"))
        user = try c.decodeIfPresent(String.self, forKey: AnyKey("user"))
        startedByDeskhand = try c.decode(Bool.self, forKey: AnyKey("startedByDeskhand"))
    }
}

/// CPU description in `DiagSystem`.
public struct DiagCPU: Codable, Sendable, Equatable {
    public var name: String
    public var cores: Int
    public var threads: Int
    public var loadPct: Double

    public init(name: String, cores: Int, threads: Int, loadPct: Double) {
        self.name = name
        self.cores = cores
        self.threads = threads
        self.loadPct = loadPct
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        cores = try c.decode(Int.self, forKey: AnyKey("cores"))
        threads = try c.decode(Int.self, forKey: AnyKey("threads"))
        loadPct = try c.decode(Double.self, forKey: AnyKey("loadPct"))
    }
}

/// Memory totals in `DiagSystem`.
public struct DiagMemory: Codable, Sendable, Equatable {
    public var totalBytes: Int
    public var availableBytes: Int

    public init(totalBytes: Int, availableBytes: Int) {
        self.totalBytes = totalBytes
        self.availableBytes = availableBytes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        totalBytes = try c.decode(Int.self, forKey: AnyKey("totalBytes"))
        availableBytes = try c.decode(Int.self, forKey: AnyKey("availableBytes"))
    }
}

/// One disk in `DiagSystem`.
public struct DiagDisk: Codable, Sendable, Equatable {
    public var name: String
    public var totalBytes: Int
    public var freeBytes: Int

    public init(name: String, totalBytes: Int, freeBytes: Int) {
        self.name = name
        self.totalBytes = totalBytes
        self.freeBytes = freeBytes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        totalBytes = try c.decode(Int.self, forKey: AnyKey("totalBytes"))
        freeBytes = try c.decode(Int.self, forKey: AnyKey("freeBytes"))
    }
}

/// One GPU in `DiagSystem`.
public struct DiagGPU: Codable, Sendable, Equatable {
    public var name: String
    public var driver: String

    public init(name: String, driver: String) {
        self.name = name
        self.driver = driver
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        driver = try c.decode(String.self, forKey: AnyKey("driver"))
    }
}

/// System diagnostics (`diag.system`).
public struct DiagSystem: Codable, Sendable, Equatable {
    public var os: String
    public var version: String
    public var build: String
    public var arch: String
    public var hostname: String
    public var cpu: DiagCPU
    public var memory: DiagMemory
    public var disks: [DiagDisk]
    public var gpus: [DiagGPU]
    public var uptimeS: Int
    public var bootTime: Timestamp

    public init(os: String, version: String, build: String, arch: String, hostname: String, cpu: DiagCPU, memory: DiagMemory, disks: [DiagDisk] = [], gpus: [DiagGPU] = [], uptimeS: Int, bootTime: Timestamp) {
        self.os = os
        self.version = version
        self.build = build
        self.arch = arch
        self.hostname = hostname
        self.cpu = cpu
        self.memory = memory
        self.disks = disks
        self.gpus = gpus
        self.uptimeS = uptimeS
        self.bootTime = bootTime
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        os = try c.decode(String.self, forKey: AnyKey("os"))
        version = try c.decode(String.self, forKey: AnyKey("version"))
        build = try c.decode(String.self, forKey: AnyKey("build"))
        arch = try c.decode(String.self, forKey: AnyKey("arch"))
        hostname = try c.decode(String.self, forKey: AnyKey("hostname"))
        cpu = try c.decode(DiagCPU.self, forKey: AnyKey("cpu"))
        memory = try c.decode(DiagMemory.self, forKey: AnyKey("memory"))
        disks = try c.decodeIfPresent([DiagDisk].self, forKey: AnyKey("disks")) ?? []
        gpus = try c.decodeIfPresent([DiagGPU].self, forKey: AnyKey("gpus")) ?? []
        uptimeS = try c.decode(Int.self, forKey: AnyKey("uptimeS"))
        bootTime = try c.decode(Timestamp.self, forKey: AnyKey("bootTime"))
    }
}

/// A remote event log entry (`diag.events`).
public struct EventLogEntry: Codable, Sendable, Equatable {
    public var time: Timestamp
    public var log: String
    public var level: String
    public var provider: String
    public var eventID: Int
    public var message: String

    public init(time: Timestamp, log: String, level: String, provider: String, eventID: Int, message: String) {
        self.time = time
        self.log = log
        self.level = level
        self.provider = provider
        self.eventID = eventID
        self.message = message
    }

    private enum CodingKeys: String, CodingKey {
        case time
        case log
        case level
        case provider
        case eventID = "eventId"
        case message
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        time = try c.decode(Timestamp.self, forKey: .time)
        log = try c.decode(String.self, forKey: .log)
        level = try c.decode(String.self, forKey: .level)
        provider = try c.decode(String.self, forKey: .provider)
        eventID = try c.decode(Int.self, forKey: .eventID)
        message = try c.decode(String.self, forKey: .message)
    }
}

/// A debuggable target.
public struct DebugTarget: Codable, Sendable, Equatable {
    public var id: String
    public var role: String
    public var type: String
    public var url: String
    public var title: String

    public init(id: String, role: String, type: String, url: String, title: String) {
        self.id = id
        self.role = role
        self.type = type
        self.url = url
        self.title = title
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey("id"))
        role = try c.decode(String.self, forKey: AnyKey("role"))
        type = try c.decode(String.self, forKey: AnyKey("type"))
        url = try c.decode(String.self, forKey: AnyKey("url"))
        title = try c.decode(String.self, forKey: AnyKey("title"))
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

/// A scope of a call frame.
public struct CallScope: Codable, Sendable, Equatable {
    public var type: String
    public var name: String?
    public var objectID: String

    public init(type: String, name: String? = nil, objectID: String) {
        self.type = type
        self.name = name
        self.objectID = objectID
    }

    private enum CodingKeys: String, CodingKey {
        case type
        case name
        case objectID = "objectId"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decode(String.self, forKey: .type)
        name = try c.decodeIfPresent(String.self, forKey: .name)
        objectID = try c.decode(String.self, forKey: .objectID)
    }
}

/// A paused call frame (`debug.state`).
public struct CallFrame: Codable, Sendable, Equatable {
    public var id: String
    public var function: String
    public var url: String
    public var line: Int
    public var column: Int
    public var scopes: [CallScope]

    public init(id: String, function: String, url: String, line: Int, column: Int, scopes: [CallScope] = []) {
        self.id = id
        self.function = function
        self.url = url
        self.line = line
        self.column = column
        self.scopes = scopes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey("id"))
        function = try c.decode(String.self, forKey: AnyKey("function"))
        url = try c.decode(String.self, forKey: AnyKey("url"))
        line = try c.decode(Int.self, forKey: AnyKey("line"))
        column = try c.decode(Int.self, forKey: AnyKey("column"))
        scopes = try c.decodeIfPresent([CallScope].self, forKey: AnyKey("scopes")) ?? []
    }
}

/// Debugger state (`debug.state`).
public struct DebugState: Codable, Sendable, Equatable {
    public var paused: Bool
    public var reason: String?
    public var callFrames: [CallFrame]

    public init(paused: Bool, reason: String? = nil, callFrames: [CallFrame] = []) {
        self.paused = paused
        self.reason = reason
        self.callFrames = callFrames
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        paused = try c.decode(Bool.self, forKey: AnyKey("paused"))
        reason = try c.decodeIfPresent(String.self, forKey: AnyKey("reason"))
        callFrames = try c.decodeIfPresent([CallFrame].self, forKey: AnyKey("callFrames")) ?? []
    }
}

/// One step of a QA run.
public struct QAStep: Codable, Sendable, Equatable {
    public var index: Int
    public var name: String
    public var action: String
    public var status: String
    public var startedAt: Timestamp?
    public var durationMs: Int?
    public var message: String?
    public var artifacts: [Artifact]

    public init(index: Int, name: String, action: String, status: String, startedAt: Timestamp? = nil, durationMs: Int? = nil, message: String? = nil, artifacts: [Artifact] = []) {
        self.index = index
        self.name = name
        self.action = action
        self.status = status
        self.startedAt = startedAt
        self.durationMs = durationMs
        self.message = message
        self.artifacts = artifacts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        index = try c.decode(Int.self, forKey: AnyKey("index"))
        name = try c.decode(String.self, forKey: AnyKey("name"))
        action = try c.decode(String.self, forKey: AnyKey("action"))
        status = try c.decode(String.self, forKey: AnyKey("status"))
        startedAt = try c.decodeIfPresent(Timestamp.self, forKey: AnyKey("startedAt"))
        durationMs = try c.decodeIfPresent(Int.self, forKey: AnyKey("durationMs"))
        message = try c.decodeIfPresent(String.self, forKey: AnyKey("message"))
        artifacts = try c.decodeIfPresent([Artifact].self, forKey: AnyKey("artifacts")) ?? []
    }
}

/// A QA scenario run (`qa.get`, `qa.list`).
public struct QARun: Codable, Sendable, Equatable {
    public var runID: String
    public var hostID: String
    public var name: String
    public var status: String
    public var startedAt: Timestamp
    public var finishedAt: Timestamp?
    public var stepsTotal: Int
    public var stepsPassed: Int
    public var stepsFailed: Int
    public var stepsBlocked: Int
    public var reportDir: String?
    public var steps: [QAStep]

    public init(runID: String, hostID: String, name: String, status: String, startedAt: Timestamp, finishedAt: Timestamp? = nil, stepsTotal: Int, stepsPassed: Int, stepsFailed: Int, stepsBlocked: Int, reportDir: String? = nil, steps: [QAStep] = []) {
        self.runID = runID
        self.hostID = hostID
        self.name = name
        self.status = status
        self.startedAt = startedAt
        self.finishedAt = finishedAt
        self.stepsTotal = stepsTotal
        self.stepsPassed = stepsPassed
        self.stepsFailed = stepsFailed
        self.stepsBlocked = stepsBlocked
        self.reportDir = reportDir
        self.steps = steps
    }

    private enum CodingKeys: String, CodingKey {
        case runID = "runId"
        case hostID = "hostId"
        case name
        case status
        case startedAt
        case finishedAt
        case stepsTotal
        case stepsPassed
        case stepsFailed
        case stepsBlocked
        case reportDir
        case steps
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runID = try c.decode(String.self, forKey: .runID)
        hostID = try c.decode(String.self, forKey: .hostID)
        name = try c.decode(String.self, forKey: .name)
        status = try c.decode(String.self, forKey: .status)
        startedAt = try c.decode(Timestamp.self, forKey: .startedAt)
        finishedAt = try c.decodeIfPresent(Timestamp.self, forKey: .finishedAt)
        stepsTotal = try c.decode(Int.self, forKey: .stepsTotal)
        stepsPassed = try c.decode(Int.self, forKey: .stepsPassed)
        stepsFailed = try c.decode(Int.self, forKey: .stepsFailed)
        stepsBlocked = try c.decode(Int.self, forKey: .stepsBlocked)
        reportDir = try c.decodeIfPresent(String.self, forKey: .reportDir)
        steps = try c.decodeIfPresent([QAStep].self, forKey: .steps) ?? []
    }
}

/// A human, AI or system action record (`timeline.list`).
public struct TimelineEntry: Codable, Sendable, Equatable {
    public var seq: Int
    public var ts: Timestamp
    public var hostID: String
    public var actor: String
    public var clientID: String?
    public var clientName: String?
    public var kind: String
    public var summary: String
    public var outcome: String
    public var jobID: String?
    public var artifacts: [Artifact]

    public init(seq: Int, ts: Timestamp, hostID: String, actor: String, clientID: String? = nil, clientName: String? = nil, kind: String, summary: String, outcome: String, jobID: String? = nil, artifacts: [Artifact] = []) {
        self.seq = seq
        self.ts = ts
        self.hostID = hostID
        self.actor = actor
        self.clientID = clientID
        self.clientName = clientName
        self.kind = kind
        self.summary = summary
        self.outcome = outcome
        self.jobID = jobID
        self.artifacts = artifacts
    }

    private enum CodingKeys: String, CodingKey {
        case seq
        case ts
        case hostID = "hostId"
        case actor
        case clientID = "clientId"
        case clientName
        case kind
        case summary
        case outcome
        case jobID = "jobId"
        case artifacts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(Int.self, forKey: .seq)
        ts = try c.decode(Timestamp.self, forKey: .ts)
        hostID = try c.decode(String.self, forKey: .hostID)
        actor = try c.decode(String.self, forKey: .actor)
        clientID = try c.decodeIfPresent(String.self, forKey: .clientID)
        clientName = try c.decodeIfPresent(String.self, forKey: .clientName)
        kind = try c.decode(String.self, forKey: .kind)
        summary = try c.decode(String.self, forKey: .summary)
        outcome = try c.decode(String.self, forKey: .outcome)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        artifacts = try c.decodeIfPresent([Artifact].self, forKey: .artifacts) ?? []
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
            ts: o["ts"]?.stringValue.flatMap(Timestamp.init(string:)),
            hostID: o["host_id"]?.stringValue,
            kind: kind,
            payload: o)
    }
}
