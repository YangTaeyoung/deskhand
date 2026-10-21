import Foundation

/// One page of a folder listing.
public struct FileListPage: Sendable, Equatable {
    public var items: [FileItem]
    /// The folder as its file system spells it, when the source reports it.
    public var path: String?
    /// Cursor of the next page; nil when this page ends the folder.
    public var nextCursor: String?
    /// Items in the folder, when the source knows it.
    public var total: Int?
    /// The source stopped listing before the end of the folder.
    public var truncated: Bool

    public init(items: [FileItem], path: String? = nil, nextCursor: String? = nil, total: Int? = nil, truncated: Bool = false) {
        self.items = items
        self.path = path
        self.nextCursor = nextCursor
        self.total = total
        self.truncated = truncated
    }
}

/// Where a pane reads its folders from.
public protocol FileListingSource: Sendable {
    var style: PathStyle { get }
    /// Items of the folder at `path` after `cursor`, at most `limit` when the source pages.
    func list(path: String, cursor: String?, limit: Int) async throws -> FileListPage
}

/// Folders of this Mac, read with `FileManager`, with the items of `overlay` added. Every
/// folder comes back as one page.
public struct LocalFileSource: FileListingSource {
    public var style: PathStyle { .posix }
    public let overlay: LocalOverlay?

    public init(overlay: LocalOverlay? = nil) {
        self.overlay = overlay
    }

    private static let keys: [URLResourceKey] = [
        .isDirectoryKey, .isPackageKey, .isSymbolicLinkKey, .isHiddenKey, .isWritableKey, .fileSizeKey,
        .contentModificationDateKey, .localizedTypeDescriptionKey,
    ]

    public func list(path: String, cursor: String?, limit: Int) async throws -> FileListPage {
        let overlay = self.overlay
        let added = overlay?.items(in: path) ?? []
        do {
            let page = try await Task.detached(priority: .userInitiated) {
                try Self.read(path)
            }.value
            guard !added.isEmpty else { return page }
            let addedPaths = Set(added.map(\.path))
            let items = page.items.filter { !addedPaths.contains($0.path) } + added
            return FileListPage(items: items, total: items.count)
        } catch let error as OperationError where error.code == "NOT_FOUND" && overlay?.hasFolder(path) == true {
            return FileListPage(items: added, total: added.count)
        }
    }

    /// The item at `path` on disk or in the overlay.
    public func item(at path: String) -> FileItem? {
        if let item = overlay?.item(at: path) { return item }
        return Self.exists(path) ? Self.item(for: URL(fileURLWithPath: path)) : nil
    }

    static func read(_ path: String) throws -> FileListPage {
        let folder = URL(fileURLWithPath: path, isDirectory: true)
        let urls: [URL]
        do {
            urls = try FileManager.default.contentsOfDirectory(at: folder, includingPropertiesForKeys: keys, options: [])
        } catch {
            throw failure(error, path: path)
        }
        let items = urls.map(item(for:))
        return FileListPage(items: items, total: items.count)
    }

    /// The row for a file or folder of this Mac.
    public static func item(for url: URL) -> FileItem {
        let values = try? url.resourceValues(forKeys: Set(keys))
        let isDirectory = values?.isDirectory ?? false
        let isPackage = values?.isPackage ?? false
        let name = url.lastPathComponent
        let kind = FileKind(name: name, isDirectory: isDirectory, isPackage: isPackage)
        var title = kind.title(for: name)
        if kind == .other || kind == .package, let system = values?.localizedTypeDescription, !system.isEmpty {
            title = system
        }
        return FileItem(name: name, path: url.path, isDirectory: isDirectory, size: values?.fileSize,
                        modified: values?.contentModificationDate, kind: title,
                        isLink: values?.isSymbolicLink ?? false, isHidden: values?.isHidden ?? name.hasPrefix("."),
                        isReadOnly: !(values?.isWritable ?? true), isPackage: isPackage)
    }

    /// What the person sees when this Mac refuses or lacks a folder.
    static func failure(_ error: any Error, path: String) -> OperationError {
        let name = FileManager.default.displayName(atPath: path)
        if let error = error as? CocoaError {
            switch error.code {
            case .fileReadNoPermission:
                return OperationError(code: localPermissionCode,
                                      message: .ui("local.error.permission", "‘\(name)’ 폴더를 읽을 권한이 없습니다"))
            case .fileReadNoSuchFile, .fileNoSuchFile:
                return OperationError(code: "NOT_FOUND", message: .ui("local.error.missing", "‘\(name)’ 폴더가 없습니다"))
            default:
                break
            }
        }
        return OperationError(message: .ui("local.error.read", "‘\(name)’ 폴더를 읽지 못했습니다: \(error.localizedDescription)"))
    }

    /// Error code of a folder of this Mac that macOS privacy settings keep from the app.
    public static let localPermissionCode = "LOCAL_PERMISSION_DENIED"

    /// The person's home folder.
    public static var home: String { FileManager.default.homeDirectoryForCurrentUser.path }

    /// Localized name of a folder of this Mac, as Finder shows it (`다운로드`, `Macintosh HD`).
    public static func displayName(_ path: String) -> String {
        FileManager.default.displayName(atPath: path)
    }

    /// Whether an item exists at `path` on this Mac.
    public static func exists(_ path: String) -> Bool {
        FileManager.default.fileExists(atPath: path)
    }

    /// Standard places of this Mac for the location menu.
    public static var places: [PathCrumb] {
        let fm = FileManager.default
        var places = [PathCrumb(title: .ui("place.home", "홈"), path: home)]
        let standard: [(FileManager.SearchPathDirectory, String)] = [
            (.desktopDirectory, .ui("place.desktop", "데스크탑")),
            (.downloadsDirectory, .ui("place.downloads", "다운로드")),
            (.documentDirectory, .ui("place.documents", "문서")),
            (.applicationDirectory, .ui("place.applications", "응용 프로그램")),
        ]
        for (directory, title) in standard {
            if let url = fm.urls(for: directory, in: directory == .applicationDirectory ? .localDomainMask : .userDomainMask).first {
                places.append(PathCrumb(title: title, path: url.path))
            }
        }
        places.append(PathCrumb(title: displayName("/"), path: "/"))
        return places
    }
}
