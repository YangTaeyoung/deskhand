import DeskhandClient
import Foundation

/// Which console entries the console shows.
public struct ConsoleFilter: Equatable, Hashable, Sendable {
    /// Levels shown; debug messages are hidden until the person turns them on.
    public var levels: Set<ConsoleLevel> = [.info, .log, .warning, .error]
    /// Text the entry contains, ignoring case.
    public var query = ""
    /// Only entries of this target; every target when nil.
    public var targetID: String?

    public init(levels: Set<ConsoleLevel> = [.info, .log, .warning, .error], query: String = "", targetID: String? = nil) {
        self.levels = levels
        self.query = query
        self.targetID = targetID
    }

    /// The query lowercased and trimmed, empty when there is none.
    var needle: String { query.trimmingCharacters(in: .whitespaces).lowercased() }

    func matches(_ entry: ConsoleEntry, key: String, needle: String, ignoringLevel: Bool = false) -> Bool {
        if !ignoringLevel, !levels.contains(entry.consoleLevel) { return false }
        if let targetID, entry.targetID != targetID { return false }
        return needle.isEmpty || key.contains(needle)
    }
}

/// The outcome of an expression evaluated from the console.
public enum EvaluationResult: Equatable, Sendable {
    case pending
    case value(DebugEvaluation)
    case failure(OperationError)
}

/// An expression the person evaluated, placed after the console entry that was the latest when it ran.
public struct EvaluationRecord: Identifiable, Equatable, Sendable {
    public let id: Int
    public var expression: String
    /// The target it ran in; its result's object belongs to this target.
    public var targetID: String
    /// The function of the frame it ran in, or nil for a global evaluation.
    public var frame: String?
    public var afterSeq: Int
    public var time: Date
    public var result: EvaluationResult
}

/// A row of the console: an entry from the daemon, an evaluation, or entries the daemon
/// discarded before they could be read.
public enum ConsoleItem: Identifiable, Equatable, Sendable {
    case entry(ConsoleEntry)
    case evaluation(EvaluationRecord)
    /// Entries before the entry with this `seq` were never received.
    case gap(Int)

    public var id: String {
        switch self {
        case .entry(let entry): "e\(entry.seq)"
        case .evaluation(let record): "x\(record.id)"
        case .gap(let seq): "gap\(seq)"
        }
    }
}

/// The console of a session: daemon entries in `seq` order without duplicates, at most
/// `capacity` of them, with lowercased search keys and per-target level counts kept as entries
/// arrive, the evaluations the person ran placed among them, and the places where entries are
/// missing. Clearing removes everything up to a `seq`; entries up to it are not taken again.
public struct ConsoleLog: Equatable, Sendable {
    public let capacity: Int
    /// Ascending `seq`.
    public private(set) var entries: [ConsoleEntry] = []
    /// Lowercased text and URL of `entries`, by index.
    private var keys: [String] = []
    /// Shown entries per target and level.
    private var countsByTarget: [String: [ConsoleLevel: Int]] = [:]
    /// Older entries than `entries.first` exist (on the daemon or trimmed here).
    public private(set) var hasEarlier = false
    /// Entries the daemon discarded since the console was last cleared.
    public var dropped: Int { max(0, daemonDropped - droppedAtClear) }
    public private(set) var evaluations: [EvaluationRecord] = []
    /// Entries up to this `seq` were cleared.
    public private(set) var clearedThrough = 0
    /// `seq`s of entries that follow entries the daemon discarded before they were read.
    public private(set) var gaps: Set<Int> = []
    private var daemonDropped = 0
    private var droppedAtClear = 0
    private var knownCursor = 0
    private var nextEvaluationID = 1

    public init(capacity: Int = 5000) {
        self.capacity = capacity
    }

    /// The largest `seq` received or cleared, to pass as `cursor`.
    public var cursor: Int { max(entries.last?.seq ?? 0, knownCursor, clearedThrough) }
    /// The smallest `seq` held, to pass as `before`.
    public var firstSeq: Int? { entries.first?.seq }

    /// Adds entries not held yet and returns whether old entries were trimmed to `capacity`.
    @discardableResult
    public mutating func merge(_ incoming: [ConsoleEntry]) -> Bool {
        let fresh = incoming.filter { $0.seq > clearedThrough }.sorted { $0.seq < $1.seq }
        guard !fresh.isEmpty else { return false }
        if let last = entries.last?.seq, fresh[0].seq <= last {
            insertOutOfOrder(fresh)
        } else {
            var previous = entries.last?.seq ?? Int.min
            for entry in fresh where entry.seq > previous {
                append(entry)
                previous = entry.seq
            }
        }
        return trim()
    }

    private mutating func append(_ entry: ConsoleEntry) {
        entries.append(entry)
        keys.append(Self.key(entry))
        countsByTarget[entry.targetID, default: [:]][entry.consoleLevel, default: 0] += 1
    }

    private mutating func insertOutOfOrder(_ fresh: [ConsoleEntry]) {
        var bySeq = Dictionary(entries.map { ($0.seq, $0) }, uniquingKeysWith: { first, _ in first })
        for entry in fresh where bySeq[entry.seq] == nil { bySeq[entry.seq] = entry }
        entries = []
        keys = []
        countsByTarget = [:]
        for entry in bySeq.values.sorted(by: { $0.seq < $1.seq }) { append(entry) }
    }

    private mutating func trim() -> Bool {
        let excess = entries.count - capacity
        guard excess > 0 else { return false }
        for entry in entries.prefix(excess) {
            countsByTarget[entry.targetID]?[entry.consoleLevel, default: 1] -= 1
        }
        entries.removeFirst(excess)
        keys.removeFirst(excess)
        hasEarlier = true
        return true
    }

    private static func key(_ entry: ConsoleEntry) -> String {
        (entry.text + "\u{1F}" + (entry.url ?? "")).lowercased()
    }

    /// Applies a page of `debug.console`; `earlier` for a page read with `before`. The first page
    /// and earlier pages say whether still older entries exist; a page with `gap` marks where
    /// entries were lost.
    public mutating func apply(_ page: ConsolePage, earlier: Bool = false) {
        let first = entries.isEmpty
        if page.gap, !earlier, let firstNew = page.entries.first(where: { $0.seq > cursor }) { gaps.insert(firstNew.seq) }
        let trimmed = merge(page.entries)
        if earlier || first {
            let olderExist = page.hasEarlier || trimmed
            hasEarlier = olderExist && (firstSeq.map { $0 - 1 > clearedThrough } ?? false)
        }
        daemonDropped = max(daemonDropped, page.dropped)
        knownCursor = max(knownCursor, page.nextCursor)
    }

    /// Removes every entry up to `seq` and every evaluation; later entries show.
    public mutating func clear(through seq: Int) {
        clearedThrough = max(clearedThrough, seq)
        let kept = entries.filter { $0.seq > clearedThrough }
        entries = []
        keys = []
        countsByTarget = [:]
        for entry in kept { append(entry) }
        evaluations = []
        hasEarlier = false
        droppedAtClear = daemonDropped
        gaps = gaps.filter { $0 > clearedThrough }
    }

    public func visible(_ filter: ConsoleFilter) -> [ConsoleEntry] {
        let needle = filter.needle
        var shown: [ConsoleEntry] = []
        for (index, entry) in entries.enumerated() where filter.matches(entry, key: keys[index], needle: needle) {
            shown.append(entry)
        }
        return shown
    }

    /// Shown entries per level, ignoring the level filter.
    public func counts(_ filter: ConsoleFilter) -> [ConsoleLevel: Int] {
        let needle = filter.needle
        var counts: [ConsoleLevel: Int] = [:]
        if needle.isEmpty {
            for (target, levels) in countsByTarget where filter.targetID == nil || filter.targetID == target {
                for (level, count) in levels where count > 0 { counts[level, default: 0] += count }
            }
            return counts
        }
        for (index, entry) in entries.enumerated() where filter.matches(entry, key: keys[index], needle: needle, ignoringLevel: true) {
            counts[entry.consoleLevel, default: 0] += 1
        }
        return counts
    }

    /// Records an evaluation that runs now and returns its id.
    public mutating func addEvaluation(expression: String, targetID: String, frame: String?, time: Date = .now) -> Int {
        let id = nextEvaluationID
        nextEvaluationID += 1
        evaluations.append(EvaluationRecord(id: id, expression: expression, targetID: targetID, frame: frame, afterSeq: cursor,
                                            time: time, result: .pending))
        return id
    }

    public mutating func resolveEvaluation(_ id: Int, _ result: EvaluationResult) {
        guard let i = evaluations.firstIndex(where: { $0.id == id }) else { return }
        evaluations[i].result = result
    }

    /// Shown entries with gap rows and the evaluations placed after the entry that was the
    /// latest when each ran.
    public func items(_ filter: ConsoleFilter) -> [ConsoleItem] {
        let shown = visible(filter)
        var items: [ConsoleItem] = []
        items.reserveCapacity(shown.count + evaluations.count + gaps.count)
        var pending = evaluations[...]
        for entry in shown {
            while let next = pending.first, next.afterSeq < entry.seq {
                items.append(.evaluation(next))
                pending = pending.dropFirst()
            }
            if gaps.contains(entry.seq) { items.append(.gap(entry.seq)) }
            items.append(.entry(entry))
        }
        items.append(contentsOf: pending.map(ConsoleItem.evaluation))
        return items
    }
}

/// Expressions evaluated in the console, walked with ↑ and ↓ like a shell history.
public struct EvaluateHistory: Equatable, Sendable {
    public static let limit = 100

    /// Oldest first.
    public private(set) var items: [String] = []
    private var index: Int?
    private var draft = ""

    public init(items: [String] = []) {
        self.items = Array(items.suffix(Self.limit))
    }

    /// Adds an evaluated expression unless it is empty or repeats the last one, and stops browsing.
    public mutating func record(_ expression: String) {
        index = nil
        let trimmed = expression.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, items.last != trimmed else { return }
        items.append(trimmed)
        if items.count > Self.limit { items.removeFirst(items.count - Self.limit) }
    }

    /// The expression before the shown one; the first step keeps `current` as the draft.
    public mutating func previous(current: String) -> String? {
        guard !items.isEmpty else { return nil }
        if let index {
            guard index > 0 else { return nil }
            self.index = index - 1
        } else {
            draft = current
            index = items.count - 1
        }
        return index.map { items[$0] }
    }

    /// The expression after the shown one, then the draft; nil when not browsing.
    public mutating func next() -> String? {
        guard let index else { return nil }
        if index + 1 < items.count {
            self.index = index + 1
            return items[index + 1]
        }
        self.index = nil
        return draft
    }
}
