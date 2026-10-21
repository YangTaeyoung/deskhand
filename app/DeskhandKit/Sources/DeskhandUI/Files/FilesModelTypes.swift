import AppKit
import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// An item about to be transferred: its name, where it comes from and the full path it goes to.
public struct TransferCandidate: Sendable, Equatable, Hashable {
    public var name: String
    public var source: String
    public var destination: String
    public var isDirectory: Bool
    public var size: Int?
    public var modified: Date?

    public init(name: String, source: String, destination: String, isDirectory: Bool, size: Int? = nil, modified: Date? = nil) {
        self.name = name
        self.source = source
        self.destination = destination
        self.isDirectory = isDirectory
        self.size = size
        self.modified = modified
    }
}

/// A transfer whose destination already has an item of the same name.
public struct ConflictItem: Sendable, Equatable, Identifiable {
    public var candidate: TransferCandidate
    public var existing: FileItem

    public var id: String { candidate.destination }

    public init(candidate: TransferCandidate, existing: FileItem) {
        self.candidate = candidate
        self.existing = existing
    }
}

/// Conflicts waiting for the person's decision, the first one shown.
public struct ConflictPrompt: Sendable, Equatable, Identifiable {
    public let id: UUID
    public var direction: TransferDirection
    /// `이 Mac` or the computer's name: where the items come from.
    public var sourceName: String
    /// `개발 PC` or `이 Mac`: where the destination folder is.
    public var destinationName: String
    public var folder: String
    public var items: [ConflictItem]

    public init(id: UUID = UUID(), direction: TransferDirection, sourceName: String, destinationName: String, folder: String,
                items: [ConflictItem]) {
        self.id = id
        self.direction = direction
        self.sourceName = sourceName
        self.destinationName = destinationName
        self.folder = folder
        self.items = items
    }

    public var current: ConflictItem? { items.first }
}

/// A move started from a menu or the toolbar, waiting for the person to confirm it.
public struct MoveRequest: Sendable, Equatable, Identifiable {
    public let id: UUID
    /// `move_push` or `move_pull`.
    public var direction: TransferDirection
    public var candidates: [TransferCandidate]
    public var folder: String
    /// `이 Mac` or the computer's name: where the sources are deleted after verification.
    public var sourceName: String
    public var destinationName: String

    public init(id: UUID = UUID(), direction: TransferDirection, candidates: [TransferCandidate], folder: String,
                sourceName: String, destinationName: String) {
        self.id = id
        self.direction = direction
        self.candidates = candidates
        self.folder = folder
        self.sourceName = sourceName
        self.destinationName = destinationName
    }
}

/// Items awaiting confirmation of a permanent delete.
public struct DeletionRequest: Sendable, Equatable, Identifiable {
    public let id = UUID()
    public var items: [FileItem]
}

/// The info sheet: the item, refreshed with `files.stat` for remote items.
public struct FileInfoState: Sendable, Equatable, Identifiable {
    public var item: FileItem
    public var side: FileSide
    public var isLoading: Bool
    public var error: OperationError?
    /// `files.stat` of a remote item: its owner and whether another program holds it open.
    public var stat: FileStat?
    /// `files.stat` found nothing at the path.
    public var missing = false

    public init(item: FileItem, side: FileSide, isLoading: Bool, error: OperationError? = nil, stat: FileStat? = nil, missing: Bool = false) {
        self.item = item
        self.side = side
        self.isLoading = isLoading
        self.error = error
        self.stat = stat
        self.missing = missing
    }

    public var id: String { item.path }
}

/// A short message at the bottom of the files tab.
public struct FilesNotice: Sendable, Equatable, Identifiable {
    public enum Tone: Sendable, Equatable {
        case info
        case success
        case error
    }

    public let id = UUID()
    public var tone: Tone
    public var message: String
    public var detail: String?
    public var code: String?
    /// The pane an error belongs to; errors with a pane stay in that pane until dismissed.
    public var side: FileSide?
}

/// What a drag over a pane would do, for the pane's drop banner.
public struct FileDropHover: Sendable, Equatable {
    /// Folder row under the pointer; nil for the open folder.
    public var folder: FileItem?
    /// ⌘ is held: the drop moves.
    public var move: Bool
    public var count: Int
}

/// A menu item of a pane's context menu.
public struct FileMenuEntry {
    public var title: String
    public var symbol: String?
    /// Shown next to the title; context menus do not trigger it.
    public var key: String
    public var modifiers: NSEvent.ModifierFlags
    public var isEnabled: Bool
    public var isSeparator: Bool
    public var action: @MainActor () -> Void

    public init(_ title: String, symbol: String? = nil, key: String = "", modifiers: NSEvent.ModifierFlags = [.command],
                isEnabled: Bool = true, action: @escaping @MainActor () -> Void) {
        self.title = title
        self.symbol = symbol
        self.key = key
        self.modifiers = modifiers
        self.isEnabled = isEnabled
        self.isSeparator = false
        self.action = action
    }

    public static var separator: FileMenuEntry {
        var entry = FileMenuEntry("") {}
        entry.isSeparator = true
        return entry
    }
}

/// Whether the remote pane can list the computer's files.
public enum RemoteFilesAvailability: Equatable, Sendable {
    case ready
    case connecting
    case reconnecting
    case offline
    case blocked
    /// The agent reports the `files` capability as not ready.
    case unavailable(CapabilityState, reason: String?)

    public init(host: Host) {
        switch host.connection {
        case .offline: self = .offline
        case .blocked: self = .blocked
        case .connecting: self = .connecting
        case .reconnecting: self = .reconnecting
        case .ready, .degraded:
            if let files = host.caps.files, files.capabilityState != .ready {
                self = .unavailable(files.capabilityState, reason: files.reason)
            } else {
                self = .ready
            }
        }
    }
}
