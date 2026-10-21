import AppKit
import Foundation

/// A remote row being dragged, as written to the pasteboard.
public struct RemoteDragItem: Codable, Sendable, Equatable {
    public var hostID: String
    public var path: String
    public var name: String
    public var dir: Bool
    public var size: Int?
    public var modified: Date?

    public init(hostID: String, path: String, name: String, dir: Bool, size: Int? = nil, modified: Date? = nil) {
        self.hostID = hostID
        self.path = path
        self.name = name
        self.dir = dir
        self.size = size
        self.modified = modified
    }

    public init(hostID: String, item: FileItem) {
        self.init(hostID: hostID, path: item.path, name: item.name, dir: item.isDirectory, size: item.size, modified: item.modified)
    }
}

extension NSPasteboard.PasteboardType {
    /// A remote file or folder row of the files tab (`RemoteDragItem` JSON).
    public static let deskhandRemoteItem = NSPasteboard.PasteboardType("io.github.yangtaeyoung.Deskhand.remote-item")
}

/// What a drag carries into a pane.
public enum FileDropPayload: Sendable, Equatable {
    /// Files and folders of this Mac, from the local pane or Finder.
    case local([URL])
    /// Rows of a remote pane.
    case remote([RemoteDragItem])

    public init?(pasteboard: NSPasteboard) {
        let decoder = JSONDecoder()
        let remote = (pasteboard.pasteboardItems ?? []).compactMap { item -> RemoteDragItem? in
            guard let data = item.data(forType: .deskhandRemoteItem) else { return nil }
            return try? decoder.decode(RemoteDragItem.self, from: data)
        }
        if !remote.isEmpty {
            self = .remote(remote)
            return
        }
        let urls = pasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
        guard !urls.isEmpty else { return nil }
        self = .local(urls)
    }

    public var count: Int {
        switch self {
        case .local(let urls): urls.count
        case .remote(let items): items.count
        }
    }
}
