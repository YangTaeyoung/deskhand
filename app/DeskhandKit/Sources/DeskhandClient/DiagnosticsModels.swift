import Foundation

/// A remote process (`proc.list`, protocol.md §3.1).
public struct ProcessInfo: Codable, Sendable, Equatable {
    public var pid: Int
    public var ppid: Int
    public var name: String
    public var path: String?
    public var cmd: String?
    /// Working set in KiB.
    public var memKb: Int
    /// Share of all logical cores, 0–100, over the agent's two samples 0.5 s apart.
    public var cpuPct: Double?
    public var user: String?
    /// Creation time; absent when the agent cannot read it.
    public var started: Timestamp?
    /// The agent started this process (`exec.*`, `app.launch`, `pty`) or one of its ancestors.
    public var startedByDeskhand: Bool
    /// The agent's own process (`self`).
    public var isSelf: Bool

    public init(pid: Int, ppid: Int, name: String, path: String? = nil, cmd: String? = nil, memKb: Int, cpuPct: Double? = nil,
                user: String? = nil, started: Timestamp? = nil, startedByDeskhand: Bool, isSelf: Bool = false) {
        self.pid = pid
        self.ppid = ppid
        self.name = name
        self.path = path
        self.cmd = cmd
        self.memKb = memKb
        self.cpuPct = cpuPct
        self.user = user
        self.started = started
        self.startedByDeskhand = startedByDeskhand
        self.isSelf = isSelf
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
        started = try c.decodeIfPresent(Timestamp.self, forKey: AnyKey("started"))
        startedByDeskhand = try c.decodeIfPresent(Bool.self, forKey: AnyKey("startedByDeskhand")) ?? false
        isSelf = try c.decodeIfPresent(Bool.self, forKey: AnyKey("self")) ?? false
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: AnyKey.self)
        try c.encode(pid, forKey: AnyKey("pid"))
        try c.encode(ppid, forKey: AnyKey("ppid"))
        try c.encode(name, forKey: AnyKey("name"))
        try c.encodeIfPresent(path, forKey: AnyKey("path"))
        try c.encodeIfPresent(cmd, forKey: AnyKey("cmd"))
        try c.encode(memKb, forKey: AnyKey("memKb"))
        try c.encodeIfPresent(cpuPct, forKey: AnyKey("cpuPct"))
        try c.encodeIfPresent(user, forKey: AnyKey("user"))
        try c.encodeIfPresent(started, forKey: AnyKey("started"))
        try c.encode(startedByDeskhand, forKey: AnyKey("startedByDeskhand"))
        try c.encode(isSelf, forKey: AnyKey("self"))
    }
}

/// Operating system in `DiagSystem`.
public struct DiagOS: Codable, Sendable, Equatable {
    /// `Windows 11 Pro`.
    public var name: String
    /// `24H2`.
    public var version: String
    /// `26100.2314`.
    public var build: String
    /// `Professional`.
    public var edition: String

    public init(name: String, version: String, build: String, edition: String) {
        self.name = name
        self.version = version
        self.build = build
        self.edition = edition
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        version = try c.decodeIfPresent(String.self, forKey: AnyKey("version")) ?? ""
        build = try c.decodeIfPresent(String.self, forKey: AnyKey("build")) ?? ""
        edition = try c.decodeIfPresent(String.self, forKey: AnyKey("edition")) ?? ""
    }
}

/// Processor in `DiagSystem`.
public struct DiagCPU: Codable, Sendable, Equatable {
    public var name: String
    public var cores: Int
    public var threads: Int
    /// Use of all logical cores, 0–100.
    public var usagePct: Double

    public init(name: String, cores: Int, threads: Int, usagePct: Double) {
        self.name = name
        self.cores = cores
        self.threads = threads
        self.usagePct = usagePct
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        cores = try c.decode(Int.self, forKey: AnyKey("cores"))
        threads = try c.decode(Int.self, forKey: AnyKey("threads"))
        usagePct = try c.decode(Double.self, forKey: AnyKey("usagePct"))
    }
}

/// Physical memory in `DiagSystem`.
public struct DiagMemory: Codable, Sendable, Equatable {
    public var totalBytes: Int
    public var usedBytes: Int

    public init(totalBytes: Int, usedBytes: Int) {
        self.totalBytes = totalBytes
        self.usedBytes = usedBytes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        totalBytes = try c.decode(Int.self, forKey: AnyKey("totalBytes"))
        usedBytes = try c.decode(Int.self, forKey: AnyKey("usedBytes"))
    }
}

/// One volume in `DiagSystem`.
public struct DiagDisk: Codable, Sendable, Equatable {
    /// `C:\`.
    public var root: String
    public var label: String?
    /// `NTFS`, `ReFS`, `exFAT`.
    public var fs: String
    public var totalBytes: Int
    public var freeBytes: Int

    public init(root: String, label: String? = nil, fs: String, totalBytes: Int, freeBytes: Int) {
        self.root = root
        self.label = label
        self.fs = fs
        self.totalBytes = totalBytes
        self.freeBytes = freeBytes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        root = try c.decode(String.self, forKey: AnyKey("root"))
        label = try c.decodeIfPresent(String.self, forKey: AnyKey("label"))
        fs = try c.decodeIfPresent(String.self, forKey: AnyKey("fs")) ?? ""
        totalBytes = try c.decode(Int.self, forKey: AnyKey("totalBytes"))
        freeBytes = try c.decode(Int.self, forKey: AnyKey("freeBytes"))
    }
}

/// One display adapter in `DiagSystem`.
public struct DiagGPU: Codable, Sendable, Equatable {
    public var name: String
    public var driverVersion: String?
    public var memoryBytes: Int?

    public init(name: String, driverVersion: String? = nil, memoryBytes: Int? = nil) {
        self.name = name
        self.driverVersion = driverVersion
        self.memoryBytes = memoryBytes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        name = try c.decode(String.self, forKey: AnyKey("name"))
        driverVersion = try c.decodeIfPresent(String.self, forKey: AnyKey("driverVersion"))
        memoryBytes = try c.decodeIfPresent(Int.self, forKey: AnyKey("memoryBytes"))
    }
}

/// System summary (`diag.system`, protocol.md §3.1).
public struct DiagSystem: Codable, Sendable, Equatable {
    public var os: DiagOS
    public var computerName: String
    public var cpu: DiagCPU
    public var memory: DiagMemory
    public var disks: [DiagDisk]
    public var gpus: [DiagGPU]
    public var bootTime: Timestamp
    public var uptimeS: Int

    public init(os: DiagOS, computerName: String, cpu: DiagCPU, memory: DiagMemory, disks: [DiagDisk] = [], gpus: [DiagGPU] = [],
                bootTime: Timestamp, uptimeS: Int) {
        self.os = os
        self.computerName = computerName
        self.cpu = cpu
        self.memory = memory
        self.disks = disks
        self.gpus = gpus
        self.bootTime = bootTime
        self.uptimeS = uptimeS
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        os = try c.decode(DiagOS.self, forKey: AnyKey("os"))
        computerName = try c.decodeIfPresent(String.self, forKey: AnyKey("computerName")) ?? ""
        cpu = try c.decode(DiagCPU.self, forKey: AnyKey("cpu"))
        memory = try c.decode(DiagMemory.self, forKey: AnyKey("memory"))
        disks = try c.decodeIfPresent([DiagDisk].self, forKey: AnyKey("disks")) ?? []
        gpus = try c.decodeIfPresent([DiagGPU].self, forKey: AnyKey("gpus")) ?? []
        bootTime = try c.decode(Timestamp.self, forKey: AnyKey("bootTime"))
        uptimeS = try c.decode(Int.self, forKey: AnyKey("uptimeS"))
    }
}

/// `level` of a Windows event (`diag.events`).
public enum EventLevel: String, Codable, Sendable, CaseIterable, Comparable {
    case critical
    case error
    case warning
    case information
    case verbose

    /// Severity order: `critical` is the most severe.
    private var rank: Int { Self.allCases.firstIndex(of: self) ?? 0 }

    public static func < (lhs: EventLevel, rhs: EventLevel) -> Bool { lhs.rank < rhs.rank }
}

/// A Windows event log record (`diag.events`, protocol.md §3.1).
public struct EventLogEntry: Codable, Sendable, Equatable, Identifiable {
    /// Record number, unique within its log.
    public var recordID: Int
    public var time: Timestamp
    public var level: String
    public var provider: String
    public var eventID: Int
    public var message: String

    public var id: Int { recordID }
    /// `level` as a known value, or nil for one this app does not know.
    public var eventLevel: EventLevel? { EventLevel(rawValue: level) }

    public init(recordID: Int, time: Timestamp, level: String, provider: String, eventID: Int, message: String) {
        self.recordID = recordID
        self.time = time
        self.level = level
        self.provider = provider
        self.eventID = eventID
        self.message = message
    }

    private enum CodingKeys: String, CodingKey {
        case recordID = "recordId"
        case time
        case level
        case provider
        case eventID = "eventId"
        case message
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        recordID = try c.decode(Int.self, forKey: .recordID)
        time = try c.decode(Timestamp.self, forKey: .time)
        level = try c.decode(String.self, forKey: .level)
        provider = try c.decode(String.self, forKey: .provider)
        eventID = try c.decode(Int.self, forKey: .eventID)
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
    }
}

/// `kind` of a crash dump (`diag.crashes`).
public enum CrashDumpKind: String, Codable, Sendable, CaseIterable {
    case minidump
    case full
    /// A Windows Error Reporting report without a memory dump.
    case report
}

/// A crash dump or error report on the remote computer (`diag.crashes`, protocol.md §3.1).
public struct CrashDump: Codable, Sendable, Equatable, Identifiable {
    public var path: String
    /// Executable that crashed, such as `Shop.exe`.
    public var app: String
    public var time: Timestamp
    public var sizeBytes: Int
    public var kind: String

    public var id: String { path }
    public var dumpKind: CrashDumpKind? { CrashDumpKind(rawValue: kind) }

    public init(path: String, app: String, time: Timestamp, sizeBytes: Int, kind: String) {
        self.path = path
        self.app = app
        self.time = time
        self.sizeBytes = sizeBytes
        self.kind = kind
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        path = try c.decode(String.self, forKey: AnyKey("path"))
        app = try c.decode(String.self, forKey: AnyKey("app"))
        time = try c.decode(Timestamp.self, forKey: AnyKey("time"))
        sizeBytes = try c.decode(Int.self, forKey: AnyKey("sizeBytes"))
        kind = try c.decode(String.self, forKey: AnyKey("kind"))
    }
}
