import AppKit
import Foundation

/// Copies text to the general pasteboard.
@MainActor
enum Pasteboard {
    static func copy(_ text: String) {
        let board = NSPasteboard.general
        board.clearContents()
        board.setString(text, forType: .string)
    }
}

/// Shows files in Finder.
@MainActor
enum FinderReveal {
    /// Selects `path` in Finder, or opens the nearest existing parent folder when it does not exist.
    static func reveal(path: String) {
        let fm = FileManager.default
        var url = URL(fileURLWithPath: path)
        if fm.fileExists(atPath: url.path) {
            NSWorkspace.shared.activateFileViewerSelecting([url])
            return
        }
        while url.path != "/" {
            url.deleteLastPathComponent()
            if fm.fileExists(atPath: url.path) {
                NSWorkspace.shared.open(url)
                return
            }
        }
    }
}

extension String {
    /// The path with the home directory written as `~`.
    var abbreviatingHome: String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        guard hasPrefix(home) else { return self }
        return "~" + dropFirst(home.count)
    }
}
