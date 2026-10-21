import Foundation
import Observation

/// Why a work root was not added.
public enum WorkRootProblem: Error, Equatable, Sendable {
    case empty
    /// Not a full path: a drive or UNC path on Windows, `/…` elsewhere.
    case notAbsolute
    /// The same folder is already a work root.
    case duplicate(String)
    /// The folder is inside a work root already.
    case covered(by: String)

    public var message: String {
        switch self {
        case .empty:
            .ui("workRoot.problem.empty", "폴더 경로를 입력하세요.")
        case .notAbsolute:
            .ui("workRoot.problem.notAbsolute", "C:\\Work 처럼 드라이브부터 시작하는 전체 경로를 입력하세요.")
        case .duplicate(let root):
            .ui("workRoot.problem.duplicate", "이미 작업 루트입니다: \(root)")
        case .covered(let root):
            .ui("workRoot.problem.covered", "이미 작업 루트 안에 있는 폴더입니다: \(root)")
        }
    }
}

/// Paths of work roots: folders on a remote computer that work happens in.
public enum WorkRoots {
    /// `path` trimmed, unquoted and written the way the computer writes it: on Windows `\`
    /// separators and an upper-case drive letter; no trailing separator except on a drive or
    /// file system root. Nil when nothing is left.
    public static func normalize(_ path: String, style: PathStyle) -> String? {
        var text = path.trimmingCharacters(in: .whitespacesAndNewlines)
        if text.count >= 2, text.first == "\"", text.last == "\"" {
            text = String(text.dropFirst().dropLast()).trimmingCharacters(in: .whitespaces)
        }
        guard !text.isEmpty else { return nil }
        switch style {
        case .windows:
            text = text.replacingOccurrences(of: "/", with: "\\")
            if text.count >= 2, text.dropFirst().first == ":", let drive = text.first, drive.isLetter {
                text = drive.uppercased() + text.dropFirst()
                if text.count == 2 { return text + "\\" }
            }
            while text.count > 3, text.hasSuffix("\\") {
                text.removeLast()
            }
            return text
        case .posix:
            while text.count > 1, text.hasSuffix("/") { text.removeLast() }
            return text
        }
    }

    /// True for `C:\…` and `\\server\share…` on Windows, `/…` elsewhere.
    public static func isAbsolute(_ path: String, style: PathStyle) -> Bool {
        switch style {
        case .windows:
            let chars = Array(path)
            if chars.count >= 3, chars[0].isLetter, chars[1] == ":", chars[2] == "\\" { return true }
            return path.hasPrefix("\\\\") && path.count > 2
        case .posix:
            return path.hasPrefix("/")
        }
    }

    /// The normalized root to add, or why `path` cannot be added beside `existing`.
    public static func validate(_ path: String, existing: [String], style: PathStyle) -> Result<String, WorkRootProblem> {
        guard let root = normalize(path, style: style) else { return .failure(.empty) }
        guard isAbsolute(root, style: style) else { return .failure(.notAbsolute) }
        if let same = existing.first(where: { style.sameName($0, root) }) { return .failure(.duplicate(same)) }
        if let parent = existing.first(where: { contains($0, root, style: style) }) { return .failure(.covered(by: parent)) }
        return .success(root)
    }

    /// True when `path` is `root` or inside it, after resolving `.` and `..` in `path`. Names
    /// compare without case on Windows.
    public static func contains(_ root: String, _ path: String, style: PathStyle) -> Bool {
        guard let root = normalize(root, style: style), let path = normalize(path, style: style).map({ resolve($0, style: style) }) else {
            return false
        }
        let separator = style == .windows ? "\\" : "/"
        if style.sameName(root, path) { return true }
        let base = root.hasSuffix(separator) ? root : root + separator
        guard path.count > base.count else { return false }
        return style.sameName(String(path.prefix(base.count)), base)
    }

    /// `path` with `.` components dropped and each `..` removing the component before it.
    static func resolve(_ path: String, style: PathStyle) -> String {
        let separator: Character = style == .windows ? "\\" : "/"
        let unc = style == .windows && path.hasPrefix("\\\\")
        var parts: [Substring] = []
        for part in path.split(separator: separator, omittingEmptySubsequences: true) {
            switch part {
            case ".": continue
            case "..": if parts.count > (unc ? 2 : 1) { parts.removeLast() }
            default: parts.append(part)
            }
        }
        let joined = parts.joined(separator: String(separator))
        switch style {
        case .windows:
            if unc { return "\\\\" + joined }
            return parts.count == 1 ? joined + "\\" : joined
        case .posix:
            return "/" + joined
        }
    }
}

/// Which folders of a computer an AI client may read and change.
public enum AIFileAccess: String, Codable, CaseIterable, Identifiable, Sendable {
    case all
    /// Only the computer's work roots.
    case workRoots
    case none

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .all: .ui("ai.files.all", "모든 폴더")
        case .workRoots: .ui("ai.files.workRoots", "작업 루트만")
        case .none: .ui("ai.files.none", "허용 안 함")
        }
    }
}

/// What of a computer reaches AI clients: screen captures, files and the clipboard.
public struct AIScope: Codable, Equatable, Sendable {
    /// Something in a scope that leaves the AI with less than the person likely meant.
    public enum Problem: Equatable, Sendable {
        /// Files are limited to work roots and the computer has none.
        case noWorkRoots
    }

    public var allowsScreen: Bool
    public var files: AIFileAccess
    public var allowsClipboard: Bool

    public init(allowsScreen: Bool, files: AIFileAccess, allowsClipboard: Bool) {
        self.allowsScreen = allowsScreen
        self.files = files
        self.allowsClipboard = allowsClipboard
    }

    public static let unrestricted = AIScope(allowsScreen: true, files: .all, allowsClipboard: true)

    public var isRestricted: Bool { self != .unrestricted }

    /// True when an AI client may read or change `path` under this scope.
    public func allowsFile(_ path: String, roots: [String], style: PathStyle) -> Bool {
        switch files {
        case .all: true
        case .none: false
        case .workRoots: roots.contains { WorkRoots.contains($0, path, style: style) }
        }
    }

    public func problems(rootCount: Int) -> [Problem] {
        files == .workRoots && rootCount == 0 ? [.noWorkRoots] : []
    }

    /// `화면 허용 · 작업 루트 2곳 · 클립보드 차단`.
    public func summary(rootCount: Int) -> String {
        let screen = allowsScreen ? String.ui("ai.summary.screen", "화면 허용") : .ui("ai.summary.screenBlocked", "화면 차단")
        let files: String = switch self.files {
        case .all: .ui("ai.summary.filesAll", "모든 폴더 허용")
        case .workRoots: .ui("ai.summary.workRoots", "작업 루트 \(String(rootCount))곳")
        case .none: .ui("ai.summary.filesBlocked", "파일 차단")
        }
        let clipboard = allowsClipboard ? String.ui("ai.summary.clipboard", "클립보드 허용") : .ui("ai.summary.clipboardBlocked", "클립보드 차단")
        return [screen, files, clipboard].joined(separator: " · ")
    }
}

/// The AI data scope of each computer the person set. Kept in UserDefaults.
@MainActor
@Observable
public final class PrivacyPreferences {
    private var scopes: [String: AIScope] {
        didSet {
            guard let data = try? JSONEncoder().encode(scopes) else { return }
            defaults.set(data, forKey: SettingsKey.Privacy.aiScopes)
        }
    }

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        scopes = defaults.data(forKey: SettingsKey.Privacy.aiScopes)
            .flatMap { try? JSONDecoder().decode([String: AIScope].self, from: $0) } ?? [:]
    }

    public func scope(for hostID: String) -> AIScope {
        scopes[hostID] ?? .unrestricted
    }

    public func setScope(_ scope: AIScope, for hostID: String) {
        scopes[hostID] = scope == .unrestricted ? nil : scope
    }
}

/// How long a finished transfer stays in the transfer queue after it last changed.
public enum TransferRetention: Int, CaseIterable, Identifiable, Sendable {
    case day = 1
    case week = 7
    case month = 30

    public var id: Int { rawValue }

    public var title: String {
        switch self {
        case .day: .ui("retention.day", "1일")
        case .week: .ui("retention.week", "7일")
        case .month: .ui("retention.month", "30일")
        }
    }

    public var interval: TimeInterval { TimeInterval(rawValue) * 24 * 60 * 60 }
}
