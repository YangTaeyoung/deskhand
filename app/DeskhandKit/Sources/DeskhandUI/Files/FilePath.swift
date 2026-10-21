import Foundation

/// One crumb of a path: its title and the folder path it opens.
public struct PathCrumb: Sendable, Equatable, Identifiable {
    public var title: String
    public var path: String

    public var id: String { path }

    public init(title: String, path: String) {
        self.title = title
        self.path = path
    }
}

/// Path arithmetic for both styles.
public enum FilePath {
    /// `C:\` for `C:`, `C:\` or `C:/`.
    static func windowsRoot(of path: String) -> String? {
        let chars = Array(path)
        guard chars.count >= 2, chars[0].isLetter, chars[1] == ":" else { return nil }
        return "\(chars[0].uppercased()):\\"
    }

    /// Components after the root.
    private static func names(_ path: String, style: PathStyle) -> (root: String, names: [String]) {
        switch style {
        case .windows:
            let normalized = path.replacingOccurrences(of: "/", with: "\\")
            if let root = windowsRoot(of: normalized) {
                let rest = normalized.dropFirst(2)
                return (root, rest.split(separator: "\\").map(String.init))
            }
            if normalized.hasPrefix("\\\\") {
                let parts = normalized.dropFirst(2).split(separator: "\\").map(String.init)
                guard parts.count >= 2 else { return ("\\\\" + parts.joined(separator: "\\"), []) }
                return ("\\\\\(parts[0])\\\(parts[1])\\", Array(parts.dropFirst(2)))
            }
            return ("", normalized.split(separator: "\\").map(String.init))
        case .posix:
            return (path.hasPrefix("/") ? "/" : "", path.split(separator: "/").map(String.init))
        }
    }

    private static func separator(_ style: PathStyle) -> String { style == .windows ? "\\" : "/" }

    /// The path in its canonical spelling: no trailing separator except on a root.
    public static func normalized(_ path: String, style: PathStyle) -> String {
        let (root, names) = names(path, style: style)
        guard !names.isEmpty else { return root.isEmpty ? path : root }
        return root + names.joined(separator: separator(style))
    }

    /// Crumbs from the root to `path`. `title` names each folder; the root is titled by `rootTitle`.
    public static func crumbs(_ path: String, style: PathStyle, rootTitle: (String) -> String = { $0 }) -> [PathCrumb] {
        let (root, names) = names(path, style: style)
        var crumbs: [PathCrumb] = []
        var current = root
        if !root.isEmpty {
            let title = style == .windows && root.hasSuffix(":\\") ? String(root.dropLast()) : root
            crumbs.append(PathCrumb(title: rootTitle(title), path: root))
        }
        for name in names {
            current = current.isEmpty || current == root ? current + name : current + separator(style) + name
            crumbs.append(PathCrumb(title: name, path: current))
        }
        return crumbs
    }

    /// The folder containing `path`, or nil at a root.
    public static func parent(_ path: String, style: PathStyle) -> String? {
        let (root, names) = names(path, style: style)
        guard !names.isEmpty else { return nil }
        let kept = names.dropLast()
        if kept.isEmpty { return root.isEmpty ? nil : root }
        return root + kept.joined(separator: separator(style))
    }

    public static func join(_ folder: String, _ name: String, style: PathStyle) -> String {
        let base = normalized(folder, style: style)
        let sep = separator(style)
        return base.hasSuffix(sep) ? base + name : base + sep + name
    }

    public static func lastComponent(_ path: String, style: PathStyle) -> String {
        names(path, style: style).names.last ?? path
    }

    /// Whether `child` is `folder` or inside it.
    public static func contains(_ folder: String, _ child: String, style: PathStyle) -> Bool {
        let a = normalized(folder, style: style)
        let b = normalized(child, style: style)
        if style.sameName(a, b) { return true }
        let prefix = a.hasSuffix(separator(style)) ? a : a + separator(style)
        return style == .windows ? b.lowercased().hasPrefix(prefix.lowercased()) : b.hasPrefix(prefix)
    }
}

/// Rules for names typed into the remote pane.
public enum FileNameRule {
    private static let reserved: Set<String> = {
        var names: Set<String> = ["CON", "PRN", "AUX", "NUL"]
        for n in 1...9 {
            names.insert("COM\(n)")
            names.insert("LPT\(n)")
        }
        return names
    }()

    /// Why `name` cannot name a file in a folder of `style`, or nil when it can.
    public static func problem(_ name: String, style: PathStyle) -> String? {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { return .ui("name.empty", "이름을 입력하세요.") }
        if name == "." || name == ".." { return .ui("name.dots", "‘.’과 ‘..’은 이름으로 쓸 수 없습니다.") }
        switch style {
        case .windows:
            if name.unicodeScalars.contains(where: { "<>:\"/\\|?*".unicodeScalars.contains($0) || $0.value < 32 }) {
                return .ui("name.invalidCharacters", "Windows 이름에는 < > : \" / \\ | ? * 를 쓸 수 없습니다.")
            }
            if name.hasSuffix(" ") || name.hasSuffix(".") {
                return .ui("name.trailing", "Windows 이름은 공백이나 마침표로 끝날 수 없습니다.")
            }
            let base = String(name.split(separator: ".", omittingEmptySubsequences: false).first ?? "").uppercased()
                .trimmingCharacters(in: .whitespaces)
            if reserved.contains(base) {
                return .ui("name.reserved", "‘\(base)’ 이름은 Windows가 예약해 두어 쓸 수 없습니다.")
            }
            if name.utf16.count > 255 { return .ui("name.tooLong", "이름은 255자를 넘을 수 없습니다.") }
        case .posix:
            if name.contains("/") { return .ui("name.slash", "이름에는 / 를 쓸 수 없습니다.") }
            if name.utf8.count > 255 { return .ui("name.tooLong", "이름은 255자를 넘을 수 없습니다.") }
        }
        return nil
    }

    /// `name (2).ext`: how a second copy of `name` is commonly named.
    public static func keepBothName(_ name: String) -> String {
        let ext = FileKind.pathExtension(of: name)
        guard !ext.isEmpty else { return "\(name) (2)" }
        let base = name.dropLast(ext.count + 1)
        let suffix = name.suffix(ext.count)
        return "\(base) (2).\(suffix)"
    }

    /// `base`, or `base 2`, `base 3` … — the first name not used by `existing`.
    public static func freeName(_ base: String, in existing: [String], style: PathStyle) -> String {
        func used(_ name: String) -> Bool { existing.contains { style.sameName($0, name) } }
        if !used(base) { return base }
        var n = 2
        while used("\(base) \(n)") { n += 1 }
        return "\(base) \(n)"
    }
}

/// Ways to show a path's crumbs in less room: every crumb, then one more leading crumb folded into
/// a menu at a time, down to the last crumb alone.
enum CrumbLayout {
    struct Candidate: Equatable {
        /// Leading crumbs behind the `…` menu, outermost first.
        var folded: [PathCrumb]
        var shown: [PathCrumb]
    }

    static func candidates(_ crumbs: [PathCrumb]) -> [Candidate] {
        guard !crumbs.isEmpty else { return [Candidate(folded: [], shown: [])] }
        return (0..<crumbs.count).map { Candidate(folded: Array(crumbs.prefix($0)), shown: Array(crumbs.dropFirst($0))) }
    }
}
