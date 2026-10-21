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
    /// Path inside the transferred folder with `/` separators, or the name of a single file.
    public var rel: String
    public var size: Int
    public var bytesDone: Int
    public var state: String
    public var outcome: String?
    public var sha256: String?
    /// Where the file was written; differs from the request after a rename.
    public var path: String?
    /// `{code, message}` (local-api.md §8); a plain string is read as the message.
    public var error: EnvelopeError?

    public init(rel: String, size: Int, bytesDone: Int, state: String, outcome: String? = nil, sha256: String? = nil, path: String? = nil,
                error: EnvelopeError? = nil) {
        self.rel = rel
        self.size = size
        self.bytesDone = bytesDone
        self.state = state
        self.outcome = outcome
        self.sha256 = sha256
        self.path = path
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
        path = try c.decodeIfPresent(String.self, forKey: AnyKey("path"))
        error = try c.decodeIfPresent(EnvelopeError.self, forKey: AnyKey("error"))
    }
}

/// A file transfer as `transfer.list` and `transfer.updated` carry it: counts and byte totals of
/// its files without the files (local-api.md §8). `rate_bps` and the byte counts are bytes.
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
    public var filesFailed: Int
    public var filesTotal: Int
    public var rateBps: Int
    public var outcome: String?
    /// `{code, message}` (local-api.md §8); a plain string is read as the message.
    public var error: EnvelopeError?
    /// Increases by one on every change of the transfer; only a larger `rev` is newer.
    public var rev: Int
    public var updatedAt: Timestamp

    public init(transferID: String, hostID: String, direction: String, local: String, remote: String, onConflict: String, state: String,
                bytesDone: Int, bytesTotal: Int, filesDone: Int, filesFailed: Int = 0, filesTotal: Int, rateBps: Int, outcome: String? = nil,
                error: EnvelopeError? = nil, rev: Int = 1, updatedAt: Timestamp = Timestamp(date: .now)) {
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
        self.filesFailed = filesFailed
        self.filesTotal = filesTotal
        self.rateBps = rateBps
        self.outcome = outcome
        self.error = error
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
        case filesFailed
        case filesTotal
        case rateBps
        case outcome
        case error
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
        filesFailed = try c.decodeIfPresent(Int.self, forKey: .filesFailed) ?? 0
        filesTotal = try c.decode(Int.self, forKey: .filesTotal)
        rateBps = try c.decode(Int.self, forKey: .rateBps)
        outcome = try c.decodeIfPresent(String.self, forKey: .outcome)
        error = try c.decodeIfPresent(EnvelopeError.self, forKey: .error)
        rev = try c.decode(Int.self, forKey: .rev)
        updatedAt = try c.decode(Timestamp.self, forKey: .updatedAt)
    }
}

/// A transfer with its files (`transfer.get`, `transfer.pause`, `transfer.resume`,
/// `transfer.cancel`): the fields of `Transfer` and `files` in one flat object. The fields of the
/// transfer read and write through to `transfer`.
@dynamicMemberLookup
public struct TransferDetail: Codable, Sendable, Equatable {
    public var transfer: Transfer
    /// Every file of the transfer, folders left out, in transfer order.
    public var files: [TransferFile]

    public init(_ transfer: Transfer, files: [TransferFile] = []) {
        self.transfer = transfer
        self.files = files
    }

    public subscript<T>(dynamicMember keyPath: WritableKeyPath<Transfer, T>) -> T {
        get { transfer[keyPath: keyPath] }
        set { transfer[keyPath: keyPath] = newValue }
    }

    public subscript<T>(dynamicMember keyPath: KeyPath<Transfer, T>) -> T {
        transfer[keyPath: keyPath]
    }

    public init(from decoder: Decoder) throws {
        transfer = try Transfer(from: decoder)
        let c = try decoder.container(keyedBy: AnyKey.self)
        files = try c.decodeIfPresent([TransferFile].self, forKey: AnyKey("files")) ?? []
    }

    public func encode(to encoder: Encoder) throws {
        try transfer.encode(to: encoder)
        var c = encoder.container(keyedBy: AnyKey.self)
        try c.encode(files, forKey: AnyKey("files"))
    }
}

/// The fields of a `transfer.updated` event: the transfer without its files, and the files whose
/// state, outcome, path or error changed since the previous event of that transfer.
public struct TransferUpdate: Decodable, Sendable, Equatable {
    public var transfer: Transfer
    public var changedFiles: [TransferFile]

    public init(transfer: Transfer, changedFiles: [TransferFile] = []) {
        self.transfer = transfer
        self.changedFiles = changedFiles
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        transfer = try c.decode(Transfer.self, forKey: AnyKey("transfer"))
        changedFiles = try c.decodeIfPresent([TransferFile].self, forKey: AnyKey("changedFiles")) ?? []
    }
}

/// `files.stat`: what a remote path leads to. A missing path is `exists: false`, not an error.
public struct FileStat: Codable, Sendable, Equatable {
    public var exists: Bool
    /// The path as the PC spells it.
    public var path: String
    public var name: String
    public var dir: Bool
    public var size: Int
    /// The entry at the path is a link, also when what it points to is missing.
    public var link: Bool
    public var mtime: Timestamp?
    /// `user`, `administrators`, `system` or an SID, when the agent reports it.
    public var owner: String?
    public var sha256: String?
    /// Another program holds the file open, when the agent reports it.
    public var locked: Bool?

    public init(exists: Bool, path: String, name: String, dir: Bool = false, size: Int = 0, link: Bool = false, mtime: Timestamp? = nil,
                owner: String? = nil, sha256: String? = nil, locked: Bool? = nil) {
        self.exists = exists
        self.path = path
        self.name = name
        self.dir = dir
        self.size = size
        self.link = link
        self.mtime = mtime
        self.owner = owner
        self.sha256 = sha256
        self.locked = locked
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        exists = try c.decode(Bool.self, forKey: AnyKey("exists"))
        path = try c.decode(String.self, forKey: AnyKey("path"))
        name = try c.decodeIfPresent(String.self, forKey: AnyKey("name")) ?? ""
        dir = try c.decodeIfPresent(Bool.self, forKey: AnyKey("dir")) ?? false
        size = try c.decodeIfPresent(Int.self, forKey: AnyKey("size")) ?? 0
        link = try c.decodeIfPresent(Bool.self, forKey: AnyKey("link")) ?? false
        mtime = try c.decodeIfPresent(Timestamp.self, forKey: AnyKey("mtime"))
        owner = try c.decodeIfPresent(String.self, forKey: AnyKey("owner"))
        sha256 = try c.decodeIfPresent(String.self, forKey: AnyKey("sha256"))
        locked = try c.decodeIfPresent(Bool.self, forKey: AnyKey("locked"))
    }

    /// The entry as `files.list` would list it; nil for a missing path.
    public var entry: FileEntry? {
        guard exists else { return nil }
        return FileEntry(name: name, path: path, dir: dir, size: size, mtime: mtime ?? Timestamp(date: .distantPast), link: link)
    }
}
