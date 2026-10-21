import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// Folders of a remote computer, read with `files.list` one page at a time.
public struct RemoteFileSource: FileListingSource {
    public let backend: any FilesBackend
    public let hostID: String
    public let style: PathStyle

    public init(backend: any FilesBackend, hostID: String, style: PathStyle) {
        self.backend = backend
        self.hostID = hostID
        self.style = style
    }

    public func list(path: String, cursor: String?, limit: Int) async throws -> FileListPage {
        let page = try await backend.filesList(hostID: hostID, path: path, cursor: cursor, limit: limit)
        return FileListPage(items: page.entries.map { FileItem(entry: $0, style: style) }, path: page.path,
                            nextCursor: page.nextCursor, total: page.total, truncated: page.truncated)
    }

    /// The folder a remote pane opens first: the signed-in user's profile on Windows, else the root.
    public static func startPath(for host: Host) -> String {
        switch PathStyle(os: host.os) {
        case .windows:
            if let user = host.session?.user, !user.isEmpty { return "C:\\Users\\\(user)" }
            return "C:\\"
        case .posix:
            if let user = host.session?.user, !user.isEmpty { return "/home/\(user)" }
            return "/"
        }
    }

    /// Standard places of the remote computer for the location menu.
    public static func places(for host: Host) -> [PathCrumb] {
        guard PathStyle(os: host.os) == .windows else {
            return [PathCrumb(title: .ui("place.home", "홈"), path: startPath(for: host)),
                    PathCrumb(title: "/", path: "/")]
        }
        var places: [PathCrumb] = []
        if let user = host.session?.user, !user.isEmpty {
            let home = "C:\\Users\\\(user)"
            places.append(PathCrumb(title: .ui("place.home", "홈"), path: home))
            places.append(PathCrumb(title: .ui("place.remoteDesktop", "바탕 화면"), path: home + "\\Desktop"))
            places.append(PathCrumb(title: .ui("place.downloads", "다운로드"), path: home + "\\Downloads"))
            places.append(PathCrumb(title: .ui("place.documents", "문서"), path: home + "\\Documents"))
        }
        places.append(PathCrumb(title: .ui("place.drive", "로컬 디스크 (C:)"), path: "C:\\"))
        return places
    }
}
