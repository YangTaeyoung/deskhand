import DeskhandClient
import Foundation

/// How the person names the script of a new breakpoint, and how a breakpoint is named back.
public enum BreakpointPattern: Equatable, Sendable {
    /// A file name such as `checkout.js`, or a path suffix such as `renderer/checkout.js`.
    case fileName(String)
    /// An exact script URL.
    case url(String)
    /// A JavaScript regular expression over script URLs; the daemon validates it.
    case regex(String)

    public enum Problem: Error, Equatable, LocalizedError {
        case empty

        public var errorDescription: String? {
            switch self {
            case .empty: .ui("debugger.pattern.empty", "스크립트 이름이나 URL을 입력하세요.")
            }
        }
    }

    /// The pattern a breakpoint was set with.
    public init(_ breakpoint: DebugBreakpoint) {
        switch breakpoint.matchKind {
        case .fileName: self = .fileName(breakpoint.pattern)
        case .url: self = .url(breakpoint.pattern)
        case .urlRegex: self = .regex(breakpoint.pattern)
        }
    }

    public var text: String {
        switch self {
        case .fileName(let value), .url(let value), .regex(let value): value
        }
    }

    /// The spec of `debug.breakpoint.set` at `line`.
    public func spec(line: Int, column: Int? = nil, condition: String? = nil) throws -> BreakpointSpec {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { throw Problem.empty }
        let match: BreakpointMatch = switch self {
        case .fileName: .fileName
        case .url: .url
        case .regex: .urlRegex
        }
        let pattern = match == .urlRegex ? text : trimmed
        let cleaned = condition?.trimmingCharacters(in: .whitespacesAndNewlines)
        return BreakpointSpec(match: match, pattern: pattern, line: line, column: column,
                              condition: cleaned?.isEmpty == false ? cleaned : nil)
    }

    /// The script part of a breakpoint's name: the file name for file-name patterns and exact
    /// URLs, `/regex/` otherwise.
    public static func scriptTitle(of breakpoint: DebugBreakpoint) -> String {
        switch breakpoint.matchKind {
        case .fileName: ScriptURL.fileName(breakpoint.pattern)
        case .url: ScriptURL.fileName(breakpoint.pattern)
        case .urlRegex: "/" + breakpoint.pattern + "/"
        }
    }

    /// The line the breakpoint stops at: where it bound when it did, else the line asked for.
    public static func line(of breakpoint: DebugBreakpoint) -> Int {
        breakpoint.locations.first?.line ?? breakpoint.line
    }

    /// `checkout.js:42`, with the bound line.
    public static func title(of breakpoint: DebugBreakpoint) -> String {
        let script = scriptTitle(of: breakpoint)
        let line = String(line(of: breakpoint))
        return script + ":" + line
    }

    /// Whether the breakpoint would bind to a script at `url`, as the daemon matches it.
    public static func matches(_ breakpoint: DebugBreakpoint, url: String) -> Bool {
        switch breakpoint.matchKind {
        case .url:
            return breakpoint.pattern == url
        case .fileName:
            return fileNameMatches(breakpoint.pattern, url: url)
        case .urlRegex:
            guard let regex = try? NSRegularExpression(pattern: breakpoint.pattern) else { return false }
            return regex.firstMatch(in: url, range: NSRange(url.startIndex..., in: url)) != nil
        }
    }

    /// The URL without `?query` and `#hash` ends with `pattern` at a path separator.
    static func fileNameMatches(_ pattern: String, url: String) -> Bool {
        let name = pattern.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { return false }
        let path = url.split(maxSplits: 1, whereSeparator: { $0 == "?" || $0 == "#" }).first.map(String.init) ?? url
        guard path.hasSuffix(name) else { return false }
        let before = path.dropLast(name.count).last
        return before == nil || before == "/" || before == "\\"
    }
}

/// Short forms of script URLs.
public enum ScriptURL {
    /// The last path component: `checkout.js` of `app://shop/renderer/checkout.js`.
    public static func fileName(_ url: String) -> String {
        let trimmed = url.split(maxSplits: 1, whereSeparator: { $0 == "?" || $0 == "#" }).first.map(String.init) ?? url
        let parts = trimmed.split(whereSeparator: { $0 == "/" || $0 == "\\" })
        guard let last = parts.last, !last.isEmpty else { return url.isEmpty ? String.ui("debugger.script.anonymous", "(이름 없는 스크립트)") : url }
        return String(last).removingPercentEncoding ?? String(last)
    }

    /// The URL without its scheme and host prefix when it is an app or file URL, for secondary lines.
    public static func display(_ url: String) -> String {
        guard let range = url.range(of: "://") else { return url }
        let rest = url[range.upperBound...]
        if url.hasPrefix("file://") {
            return (String(rest).removingPercentEncoding ?? String(rest)).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        }
        return String(rest)
    }

    /// Node's own scripts (`node:internal/…`).
    public static func isInternal(_ url: String) -> Bool {
        url.hasPrefix("node:")
    }
}

/// Splits an argument line into arguments: spaces separate, double or single quotes group.
public enum ArgumentSplitter {
    public static func split(_ line: String) -> [String] {
        var args: [String] = []
        var current = ""
        var quote: Character?
        var inArgument = false
        for c in line {
            if let q = quote {
                if c == q { quote = nil } else { current.append(c) }
            } else if c == "\"" || c == "'" {
                quote = c
                inArgument = true
            } else if c == " " || c == "\t" {
                if inArgument {
                    args.append(current)
                    current = ""
                    inArgument = false
                }
            } else {
                current.append(c)
                inArgument = true
            }
        }
        if inArgument { args.append(current) }
        return args
    }

    /// The arguments as one line, double-quoting those with spaces.
    public static func join(_ args: [String]) -> String {
        args.map { $0.contains(" ") || $0.isEmpty ? "\"\($0)\"" : $0 }.joined(separator: " ")
    }
}

/// Fuzzy file-name search over script URLs: the query's letters appear in order in the file
/// name, ignoring case; tighter matches rank first, then shorter names.
public enum ScriptSearch {
    public struct Match: Equatable, Sendable {
        public var url: String
        public var fileName: String
        public var score: Int
    }

    /// The URLs that match `query`, best first; `node:` scripts only with `includeInternal`.
    public static func rank(_ urls: [String], query: String, includeInternal: Bool = false) -> [Match] {
        let needle = Array(query.trimmingCharacters(in: .whitespaces).lowercased())
        return urls.compactMap { url -> Match? in
            if !includeInternal, ScriptURL.isInternal(url) { return nil }
            let name = ScriptURL.fileName(url)
            guard let score = score(needle, in: Array(name.lowercased())) else { return nil }
            return Match(url: url, fileName: name, score: score)
        }
        .sorted { a, b in
            if a.score != b.score { return a.score > b.score }
            if a.fileName.count != b.fileName.count { return a.fileName.count < b.fileName.count }
            return a.url < b.url
        }
    }

    /// A score for `needle` as a subsequence of `name`: a start and consecutive letters score more.
    static func score(_ needle: [Character], in name: [Character]) -> Int? {
        guard !needle.isEmpty else { return 0 }
        var score = 0
        var position = 0
        var previous = -2
        for letter in needle {
            guard let found = name[position...].firstIndex(of: letter) else { return nil }
            score += 1
            if found == 0 { score += 6 }
            if found == previous + 1 { score += 4 }
            if found > 0, ".-_ ".contains(name[found - 1]) { score += 3 }
            previous = found
            position = found + 1
        }
        return score
    }
}
