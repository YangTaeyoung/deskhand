import Foundation

// Debugger models of local-api.md §5 and §9. Lines and columns count from 1.

/// A console message or uncaught exception of a session.
public struct ConsoleEntry: Codable, Sendable, Equatable, Identifiable {
    /// Rises by one per entry within the session, from 1.
    public var seq: Int
    public var time: Timestamp
    public var targetID: String
    public var source: String
    public var level: String
    public var text: String
    public var scriptID: String?
    public var url: String?
    public var line: Int?
    public var column: Int?
    public var stack: [StackFrameInfo]?

    public var id: Int { seq }
    public var consoleLevel: ConsoleLevel { ConsoleLevel(rawValue: level) ?? .log }
    public var consoleSource: ConsoleSource { ConsoleSource(rawValue: source) ?? .console }

    public init(seq: Int, time: Timestamp, targetID: String, source: ConsoleSource = .console, level: ConsoleLevel, text: String,
                scriptID: String? = nil, url: String? = nil, line: Int? = nil, column: Int? = nil, stack: [StackFrameInfo]? = nil) {
        self.seq = seq
        self.time = time
        self.targetID = targetID
        self.source = source.rawValue
        self.level = level.rawValue
        self.text = text
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
        self.stack = stack
    }

    private enum CodingKeys: String, CodingKey {
        case targetID = "targetId"
        case scriptID = "scriptId"
        case seq, time, source, level, text, url, line, column, stack
    }
}

/// One page of `debug.console`. Entries are oldest first.
public struct ConsolePage: Codable, Sendable, Equatable {
    public var entries: [ConsoleEntry]
    /// The `seq` to pass as `cursor` for the entries after these.
    public var nextCursor: Int
    /// The oldest `seq` the daemon still keeps.
    public var firstSeq: Int
    public var hasMore: Bool
    /// Older entries than the first one are still kept by the daemon.
    public var hasEarlier: Bool
    /// Entries the daemon discarded from its buffer during the session.
    public var dropped: Int
    /// Entries after `cursor` were discarded before they could be read.
    public var gap: Bool

    public init(entries: [ConsoleEntry], nextCursor: Int, firstSeq: Int? = nil, hasMore: Bool = false, hasEarlier: Bool = false,
                dropped: Int = 0, gap: Bool = false) {
        self.entries = entries
        self.nextCursor = nextCursor
        self.firstSeq = firstSeq ?? entries.first?.seq ?? nextCursor + 1
        self.hasMore = hasMore
        self.hasEarlier = hasEarlier
        self.dropped = dropped
        self.gap = gap
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        entries = try c.decodeIfPresent([ConsoleEntry].self, forKey: .entries) ?? []
        nextCursor = try c.decodeIfPresent(Int.self, forKey: .nextCursor) ?? (entries.last?.seq ?? 0)
        firstSeq = try c.decodeIfPresent(Int.self, forKey: .firstSeq) ?? entries.first?.seq ?? nextCursor + 1
        hasMore = try c.decodeIfPresent(Bool.self, forKey: .hasMore) ?? false
        hasEarlier = try c.decodeIfPresent(Bool.self, forKey: .hasEarlier) ?? false
        dropped = try c.decodeIfPresent(Int.self, forKey: .dropped) ?? 0
        gap = try c.decodeIfPresent(Bool.self, forKey: .gap) ?? false
    }

    private enum CodingKeys: String, CodingKey {
        case entries, nextCursor, firstSeq, hasMore, hasEarlier, dropped, gap
    }
}

/// Result of `debug.source`.
public struct DebugSource: Codable, Sendable, Equatable {
    public var scriptID: String
    public var url: String
    public var text: String
    public var truncated: Bool

    public init(scriptID: String, url: String, text: String, truncated: Bool = false) {
        self.scriptID = scriptID
        self.url = url
        self.text = text
        self.truncated = truncated
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        scriptID = try c.decodeIfPresent(String.self, forKey: .scriptID) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        text = try c.decode(String.self, forKey: .text)
        truncated = try c.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
    }

    private enum CodingKeys: String, CodingKey {
        case scriptID = "scriptId"
        case url, text, truncated
    }
}
