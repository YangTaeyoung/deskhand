import DeskhandClient
import Foundation

/// `direction` of `transfer.start` and `Transfer` (local-api.md §3, §5).
public enum TransferDirection: String, Sendable, CaseIterable, Codable {
    /// This Mac → the remote computer.
    case push
    /// The remote computer → this Mac.
    case pull
    /// Push, then delete the source on this Mac once the destination is verified.
    case movePush = "move_push"
    /// Pull, then delete the source on the remote computer once the destination is verified.
    case movePull = "move_pull"

    public var isMove: Bool { self == .movePush || self == .movePull }
    /// The destination is on the remote computer.
    public var isUpload: Bool { self == .push || self == .movePush }

    public static func upload(move: Bool) -> TransferDirection { move ? .movePush : .push }
    public static func download(move: Bool) -> TransferDirection { move ? .movePull : .pull }
}

/// `on_conflict`: what a transfer does when the destination already has an item of the same name.
public enum ConflictPolicy: String, Sendable, CaseIterable, Codable {
    /// The file fails with `TRANSFER_CONFLICT` and the destination is left as it is.
    case fail
    /// The destination is replaced after the new copy is verified.
    case replace
    /// The file is not transferred.
    case skip
    /// The new copy is written under a free name such as `name (2).ext`.
    case rename
}

/// `Transfer.state` and `TransferFile.state`.
public enum TransferState: String, Sendable, CaseIterable {
    case queued
    case running
    case paused
    case verifying
    case done
    case failed
    case cancelled

    public init(raw: String) {
        self = TransferState(rawValue: raw) ?? .failed
    }

    /// No further progress happens without a resume.
    public var isFinished: Bool { self == .done || self == .failed || self == .cancelled }
}

/// `Transfer.outcome` and `TransferFile.outcome`.
public enum TransferOutcome: String, Sendable, CaseIterable {
    case written
    case skipped
    case renamed
    /// A move copied and verified the destination but could not delete the source.
    case copiedSourceRetained = "copied_source_retained"
    /// Some files were transferred and others failed.
    case partial
    case failed
}

/// Parameters of `transfer.start`.
public struct TransferRequest: Sendable, Equatable {
    public var hostID: String
    public var direction: TransferDirection
    /// Path on this Mac: the source of a push, the destination of a pull.
    public var local: String
    /// Path on the remote computer: the destination of a push, the source of a pull.
    public var remote: String
    public var onConflict: ConflictPolicy

    public init(hostID: String, direction: TransferDirection, local: String, remote: String, onConflict: ConflictPolicy) {
        self.hostID = hostID
        self.direction = direction
        self.local = local
        self.remote = remote
        self.onConflict = onConflict
    }
}

/// One page of a remote folder listing.
public struct FileEntryPage: Sendable, Equatable {
    public var entries: [FileEntry]
    /// Cursor of the next page; nil when this page ends the folder.
    public var nextCursor: String?
    /// Entries in the folder, when known.
    public var total: Int?
    /// The agent stopped listing before the end of the folder.
    public var truncated: Bool

    public init(entries: [FileEntry], nextCursor: String? = nil, total: Int? = nil, truncated: Bool = false) {
        self.entries = entries
        self.nextCursor = nextCursor
        self.total = total
        self.truncated = truncated
    }
}

extension Transfer {
    public var transferState: TransferState { TransferState(raw: state) }
    public var transferOutcome: TransferOutcome? { outcome.flatMap(TransferOutcome.init(rawValue:)) }
    public var transferDirection: TransferDirection { TransferDirection(rawValue: direction) ?? .push }
}

extension TransferFile {
    public var transferState: TransferState { TransferState(raw: state) }
    public var transferOutcome: TransferOutcome? { outcome.flatMap(TransferOutcome.init(rawValue:)) }
}
