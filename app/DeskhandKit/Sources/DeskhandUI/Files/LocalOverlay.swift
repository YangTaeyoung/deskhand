import Foundation
import Synchronization

/// Items the preview daemon received on this Mac without writing them: kept in memory and
/// shown in the local pane marked as preview items.
public final class LocalOverlay: Sendable {
    /// Items by the path of the folder that holds them.
    private let folders = Mutex<[String: [FileItem]]>([:])

    public init() {}

    /// Adds `item`, replacing an overlay item of the same path, and the folders above it up to
    /// the first folder that exists on disk.
    public func add(_ item: FileItem) {
        var item = item
        item.isPreviewOnly = true
        let parentFolders = Self.missingAncestors(of: item.path)
        folders.withLock { folders in
            for folder in parentFolders {
                guard let parent = FilePath.parent(folder, style: .posix) else { continue }
                let name = FilePath.lastComponent(folder, style: .posix)
                if folders[parent]?.contains(where: { $0.path == folder }) != true {
                    folders[parent, default: []].append(FileItem(name: name, path: folder, isDirectory: true, modified: .now,
                                                                isPreviewOnly: true))
                }
            }
            guard let parent = FilePath.parent(item.path, style: .posix) else { return }
            folders[parent, default: []].removeAll { $0.path == item.path }
            folders[parent, default: []].append(item)
        }
    }

    /// Overlay items directly inside `folder`.
    public func items(in folder: String) -> [FileItem] {
        folders.withLock { $0[FilePath.normalized(folder, style: .posix)] ?? [] }
    }

    public func item(at path: String) -> FileItem? {
        guard let parent = FilePath.parent(path, style: .posix) else { return nil }
        return items(in: parent).first { $0.path == path }
    }

    /// Whether `folder` exists only in the overlay.
    public func hasFolder(_ folder: String) -> Bool {
        item(at: FilePath.normalized(folder, style: .posix))?.isDirectory == true
    }

    /// Folders between `path` and the nearest ancestor that exists on disk, outermost first.
    private static func missingAncestors(of path: String) -> [String] {
        var missing: [String] = []
        var current = FilePath.parent(path, style: .posix)
        while let folder = current, !FileManager.default.fileExists(atPath: folder) {
            missing.insert(folder, at: 0)
            current = FilePath.parent(folder, style: .posix)
        }
        return missing
    }
}

/// A backend whose transfers to this Mac land in a `LocalOverlay` instead of the disk.
public protocol LocalOverlayProviding: Sendable {
    var localOverlay: LocalOverlay { get }
}
