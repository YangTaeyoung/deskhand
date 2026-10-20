import Darwin
import Foundation

/// Reads `Host` aliases from an OpenSSH client configuration without modifying it.
///
/// Follows `Include` (globs, `~`, and paths relative to the directory of the top-level file,
/// which is `~/.ssh` for the user configuration), lists each alias of a multi-alias `Host` line,
/// and skips patterns containing `*`, `?` or `!`.
public enum SSHConfigReader {
    public static var defaultConfig: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".ssh/config")
    }

    /// Include nesting limit, as in OpenSSH.
    static let maxDepth = 16

    /// Concrete aliases in file order, without duplicates. Missing or unreadable files yield none.
    public static func hosts(from url: URL = defaultConfig) -> [String] {
        var reader = Reader(base: url.deletingLastPathComponent())
        reader.read(url, depth: 0)
        return reader.aliases
    }

    private struct Reader {
        let base: URL
        var aliases: [String] = []
        var seenAliases: Set<String> = []
        var visiting: Set<String> = []

        init(base: URL) {
            self.base = base
        }

        mutating func read(_ url: URL, depth: Int) {
            guard depth <= SSHConfigReader.maxDepth else { return }
            let key = url.resolvingSymlinksInPath().standardizedFileURL.path
            guard !visiting.contains(key), let text = try? String(contentsOf: url, encoding: .utf8) else { return }
            visiting.insert(key)
            defer { visiting.remove(key) }

            for line in text.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline) {
                guard let (keyword, arguments) = SSHConfigReader.parse(line) else { continue }
                switch keyword {
                case "host":
                    for alias in arguments where SSHConfigReader.isConcrete(alias) && !seenAliases.contains(alias) {
                        seenAliases.insert(alias)
                        aliases.append(alias)
                    }
                case "include":
                    for pattern in arguments {
                        for file in SSHConfigReader.expand(pattern, base: base) {
                            read(file, depth: depth + 1)
                        }
                    }
                default:
                    break
                }
            }
        }
    }

    /// Lowercased keyword and its arguments; nil for blank and comment lines.
    static func parse(_ line: Substring) -> (String, [String])? {
        var tokens: [String] = []
        var current = ""
        var quoted = false
        var hasToken = false
        var consumedEquals = false

        func flush() {
            if hasToken { tokens.append(current) }
            current = ""
            hasToken = false
        }

        for ch in line {
            if quoted {
                if ch == "\"" { quoted = false } else { current.append(ch) }
                continue
            }
            switch ch {
            case "\"":
                quoted = true
                hasToken = true
            case "#" where !hasToken:
                flush()
                return tokens.first.map { ($0.lowercased(), Array(tokens.dropFirst())) }
            case "=" where !consumedEquals && ((tokens.isEmpty && hasToken) || (tokens.count == 1 && !hasToken)):
                flush()
                consumedEquals = true
            case " ", "\t":
                flush()
            default:
                current.append(ch)
                hasToken = true
            }
        }
        flush()
        return tokens.first.map { ($0.lowercased(), Array(tokens.dropFirst())) }
    }

    static func isConcrete(_ alias: String) -> Bool {
        !alias.isEmpty && !alias.contains(where: { $0 == "*" || $0 == "?" || $0 == "!" })
    }

    /// Files matching an `Include` argument, sorted as `glob(3)` returns them.
    static func expand(_ pattern: String, base: URL) -> [URL] {
        var path = pattern
        if path == "~" || path.hasPrefix("~/") {
            path = FileManager.default.homeDirectoryForCurrentUser.path + path.dropFirst()
        } else if !path.hasPrefix("/") {
            path = base.appendingPathComponent(path).path
        }
        var result = glob_t()
        defer { globfree(&result) }
        guard glob(path, 0, nil, &result) == 0 else { return [] }
        var urls: [URL] = []
        for i in 0..<Int(result.gl_pathc) {
            if let cString = result.gl_pathv[i] {
                urls.append(URL(fileURLWithPath: String(cString: cString)))
            }
        }
        return urls
    }
}
