import Foundation

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
