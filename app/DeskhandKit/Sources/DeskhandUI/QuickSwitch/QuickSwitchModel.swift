import Foundation
import Observation

/// What opening a quick switch item does.
public enum QuickSwitchAction: Equatable, Sendable {
    case selectHost(String)
    /// A tab of the selected computer.
    case showTab(WorkspaceTab)
    case showTransfer(hostID: String, transferID: String)
    case showDebugSession(hostID: String, debugID: String)
    case showQARun(hostID: String, runID: String)
    case command(QuickSwitchCommand)
}

/// Commands the quick switch offers.
public enum QuickSwitchCommand: String, CaseIterable, Sendable {
    case newTerminal
    case runScenario
    case showTransferQueue
    case emergencyStop
    case takeControl
    case giveBackToAI
    case runDoctor
    case addHost
    case openSettings
}

/// One thing the quick switch can open.
public struct QuickSwitchItem: Identifiable, Equatable, Sendable {
    /// Kinds in the order their sections appear when nothing is typed.
    public enum Kind: Int, Comparable, Sendable {
        case tab
        case command
        case host
        case transfer
        case debugSession
        case qaRun

        public static func < (lhs: Kind, rhs: Kind) -> Bool { lhs.rawValue < rhs.rawValue }

        /// Short name of the kind beside a search result.
        public var title: String {
            switch self {
            case .tab: .ui("quickSwitch.kind.tab", "탭")
            case .command: .ui("quickSwitch.kind.command", "명령")
            case .host: .ui("quickSwitch.kind.host", "컴퓨터")
            case .transfer: .ui("quickSwitch.kind.transfer", "전송")
            case .debugSession: .ui("quickSwitch.kind.debugSession", "디버그 세션")
            case .qaRun: .ui("quickSwitch.kind.qaRun", "QA 실행")
            }
        }
    }

    /// Color family of the item's symbol tile.
    public enum Tone: Sendable, Equatable {
        case accent
        case neutral
        case danger
        case human
        case ai
        case success
        case warning
    }

    /// The same across launches: `host:<id>`, `tab:<tab>`, `command:<command>`, `transfer:<id>`,
    /// `debug:<id>`, `qa:<id>`.
    public let id: String
    public let kind: Kind
    public let title: String
    public let subtitle: String?
    /// Other words that find the item, such as its English name.
    public let keywords: [String]
    public let symbol: String
    public let tone: Tone
    /// The item's own key equivalent in the menus, for commands.
    public let shortcut: String?
    /// The computer's connection, for computers.
    public let connection: ConnectionState?
    public let action: QuickSwitchAction
    /// The title, keywords and subtitle prepared for matching.
    let titleCandidate: FuzzyCandidate
    let keywordCandidates: [FuzzyCandidate]
    let subtitleCandidate: FuzzyCandidate?

    public init(id: String, kind: Kind, title: String, subtitle: String? = nil, keywords: [String] = [], symbol: String,
                tone: Tone = .accent, shortcut: String? = nil, connection: ConnectionState? = nil, action: QuickSwitchAction) {
        self.id = id
        self.kind = kind
        self.title = title
        self.subtitle = subtitle
        self.keywords = keywords
        self.symbol = symbol
        self.tone = tone
        self.shortcut = shortcut
        self.connection = connection
        self.action = action
        titleCandidate = FuzzyCandidate(title)
        keywordCandidates = keywords.map(FuzzyCandidate.init)
        subtitleCandidate = subtitle.map(FuzzyCandidate.init)
    }
}

/// A result row: the item, the offsets of the title characters the query matched, and the
/// keyword that matched when no title characters did.
public struct QuickSwitchRow: Identifiable, Equatable, Sendable {
    public let item: QuickSwitchItem
    public let highlights: [Int]
    public let keyword: String?

    public init(item: QuickSwitchItem, highlights: [Int], keyword: String? = nil) {
        self.item = item
        self.highlights = highlights
        self.keyword = keyword
    }

    public var id: String { item.id }
}

public struct QuickSwitchSection: Identifiable, Equatable, Sendable {
    public enum Kind: Sendable, Equatable {
        case recent
        case tabs
        case commands
        case hosts
        case work
        /// Everything that matched what is typed, best first.
        case results
    }

    public let kind: Kind
    public let rows: [QuickSwitchRow]

    public var id: String { "\(kind)" }

    /// Header of the section; nil for search results.
    public var title: String? {
        switch kind {
        case .recent: .ui("quickSwitch.section.recent", "최근 사용")
        case .tabs: .ui("quickSwitch.section.tabs", "탭")
        case .commands: .ui("quickSwitch.section.commands", "명령")
        case .hosts: .ui("quickSwitch.section.hosts", "컴퓨터")
        case .work: .ui("quickSwitch.section.work", "최근 작업")
        case .results: nil
        }
    }
}

/// Orders quick switch items for a query.
public enum QuickSwitchRanking {
    /// Rows shown for a query at most.
    static let resultLimit = 40
    /// Recently opened items shown at most when nothing is typed.
    static let recentLimit = 5
    /// Transfers, debug sessions and QA runs shown at most when nothing is typed.
    static let workLimit = 6
    /// Keyword matches score this much below a title match.
    static let keywordPenalty = 8
    /// Subtitle matches score this much below a title match.
    static let subtitlePenalty = 16
    /// The score a match needs per typed character to be listed.
    static let minimumScorePerCharacter = 14

    /// With nothing typed: the recently opened items, then tabs, commands, computers and recent
    /// work. Otherwise one list of the items whose title or keywords match, best first, where a
    /// recently opened item scores higher. With several words each word may match the title,
    /// a keyword or the subtitle, and at least one the title or a keyword.
    public static func sections(query: String, items: [QuickSwitchItem], recents: [String]) -> [QuickSwitchSection] {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return browsing(items: items, recents: recents) }
        let words = trimmed.split(whereSeparator: \.isWhitespace).map(String.init)
        var scored: [(row: QuickSwitchRow, score: Int)] = []
        for item in items {
            guard let found = match(trimmed, words: words, item: item) else { continue }
            let score = found.score + (recents.firstIndex(of: item.id).map { max(4, 12 - 2 * $0) } ?? 0)
            scored.append((QuickSwitchRow(item: item, highlights: found.highlights, keyword: found.keyword), score))
        }
        guard !scored.isEmpty else { return [] }
        scored.sort { a, b in
            if a.score != b.score { return a.score > b.score }
            if a.row.item.kind != b.row.item.kind { return a.row.item.kind < b.row.item.kind }
            return a.row.item.title.localizedStandardCompare(b.row.item.title) == .orderedAscending
        }
        return [QuickSwitchSection(kind: .results, rows: scored.prefix(resultLimit).map(\.row))]
    }

    private static func browsing(items: [QuickSwitchItem], recents: [String]) -> [QuickSwitchSection] {
        let byID = Dictionary(items.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let recent = recents.compactMap { byID[$0] }.prefix(recentLimit)
        let shown = Set(recent.map(\.id))
        func rows(_ kinds: Set<QuickSwitchItem.Kind>, limit: Int = .max) -> [QuickSwitchRow] {
            items.filter { kinds.contains($0.kind) && !shown.contains($0.id) }.prefix(limit)
                .map { QuickSwitchRow(item: $0, highlights: []) }
        }
        let sections = [
            QuickSwitchSection(kind: .recent, rows: recent.map { QuickSwitchRow(item: $0, highlights: []) }),
            QuickSwitchSection(kind: .tabs, rows: rows([.tab])),
            QuickSwitchSection(kind: .commands, rows: rows([.command])),
            QuickSwitchSection(kind: .hosts, rows: rows([.host])),
            QuickSwitchSection(kind: .work, rows: rows([.transfer, .debugSession, .qaRun], limit: workLimit)),
        ]
        return sections.filter { !$0.rows.isEmpty }
    }

    private struct Found {
        var score: Int
        var highlights: [Int]
        var keyword: String?
        var field: Field
    }

    private enum Field { case title, keyword, subtitle }

    /// The item's score, title highlights and matched keyword for the query, or nil when it does
    /// not match well enough.
    private static func match(_ query: String, words: [String], item: QuickSwitchItem) -> Found? {
        if let whole = bestField(query, item: item, subtitle: false) { return whole }
        guard words.count > 1 else { return nil }
        var total = 0
        var highlights: [Int] = []
        var keyword: String?
        var matchedTitleOrKeyword = false
        for word in words {
            guard let found = bestField(word, item: item, subtitle: true) else { return nil }
            total += found.score
            highlights += found.highlights
            if found.field == .keyword, keyword == nil { keyword = found.keyword }
            if found.field != .subtitle { matchedTitleOrKeyword = true }
        }
        guard matchedTitleOrKeyword else { return nil }
        let sorted = Array(Set(highlights)).sorted()
        return Found(score: total / words.count, highlights: sorted, keyword: sorted.isEmpty ? keyword : nil, field: .title)
    }

    private static func bestField(_ text: String, item: QuickSwitchItem, subtitle: Bool) -> Found? {
        var best: Found?
        func consider(_ found: Found) {
            if best.map({ found.score > $0.score }) ?? true { best = found }
        }
        let minimum = text.filter { !$0.isWhitespace }.count * minimumScorePerCharacter
        if let title = FuzzyMatcher.match(text, in: item.titleCandidate), title.score >= minimum {
            consider(Found(score: title.score, highlights: title.indices, keyword: nil, field: .title))
        }
        for candidate in item.keywordCandidates {
            if let match = FuzzyMatcher.match(text, in: candidate), match.score >= minimum {
                consider(Found(score: match.score - keywordPenalty, highlights: [], keyword: candidate.text, field: .keyword))
            }
        }
        if subtitle, let line = item.subtitleCandidate, let match = FuzzyMatcher.match(text, in: line), match.score >= minimum {
            consider(Found(score: match.score - subtitlePenalty, highlights: [], keyword: nil, field: .subtitle))
        }
        return best
    }
}

/// Keys the quick switch handles while its search field has focus.
public enum QuickSwitchKey: Equatable, Sendable {
    case up
    case down
    case enter
    case escape
}

/// What a key does to the quick switch.
public enum QuickSwitchEffect: Equatable, Sendable {
    case none
    case select(Int)
    case activate(Int)
    case clearQuery
    case dismiss
}

/// Keyboard rules of the quick switch: ↑ and ↓ move the selection and stop at the ends, ↩
/// opens the selected row, and esc clears what is typed and then closes.
public enum QuickSwitchNavigator {
    public static func effect(for key: QuickSwitchKey, selection: Int, count: Int, queryIsEmpty: Bool) -> QuickSwitchEffect {
        switch key {
        case .down:
            return selection + 1 < count ? .select(selection + 1) : .none
        case .up:
            return selection > 0 && count > 0 ? .select(min(selection, count) - 1) : .none
        case .enter:
            return count > 0 ? .activate(min(max(selection, 0), count - 1)) : .none
        case .escape:
            return queryIsEmpty ? .dismiss : .clearQuery
        }
    }
}

/// State of the quick switch palette (⌘K): whether it shows, what is typed, the items it
/// offers ranked for the query, the selected row and the items opened from it recently. The
/// recent items are kept in UserDefaults.
@MainActor
@Observable
public final class QuickSwitchModel {
    /// Recently opened items kept at most.
    static let recentLimit = 8

    public private(set) var isPresented = false
    public var query = "" {
        didSet {
            guard query != oldValue else { return }
            selection = 0
            rank()
        }
    }
    /// Index of the selected row in `rows`.
    public var selection = 0
    /// IDs of items opened from the palette, newest first.
    public private(set) var recents: [String]
    public private(set) var items: [QuickSwitchItem] = []
    /// `items` ranked for `query`.
    public private(set) var sections: [QuickSwitchSection] = []
    /// The rows of `sections` in order.
    public private(set) var rows: [QuickSwitchRow] = []
    /// Rises each time the query is cleared from the palette, so the search field drops text the
    /// input method is still composing.
    public private(set) var clearRequests = 0

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        recents = defaults.data(forKey: SettingsKey.QuickSwitch.recents)
            .flatMap { try? JSONDecoder().decode([String].self, from: $0) } ?? []
    }

    public var selectedRow: QuickSwitchRow? {
        rows.indices.contains(selection) ? rows[selection] : nil
    }

    /// Shows the palette with nothing typed.
    public func present() {
        query = ""
        selection = 0
        isPresented = true
    }

    public func dismiss() {
        isPresented = false
    }

    public func toggle() {
        if isPresented { dismiss() } else { present() }
    }

    /// Replaces the items the palette offers and ranks them again, keeping the selection within the rows.
    public func update(items: [QuickSwitchItem]) {
        guard items != self.items else { return }
        self.items = items
        rank()
    }

    /// Clears what is typed, including text the input method is composing.
    public func clearQuery() {
        query = ""
        clearRequests += 1
    }

    /// Applies `key` to the rows; returns the item to open, if the key opens one.
    public func handle(_ key: QuickSwitchKey) -> QuickSwitchItem? {
        switch QuickSwitchNavigator.effect(for: key, selection: selection, count: rows.count, queryIsEmpty: query.isEmpty) {
        case .none: return nil
        case .select(let index): selection = index
        case .activate(let index): return rows[index].item
        case .clearQuery: clearQuery()
        case .dismiss: dismiss()
        }
        return nil
    }

    private func rank() {
        sections = QuickSwitchRanking.sections(query: query, items: items, recents: recents)
        rows = sections.flatMap(\.rows)
        selection = min(selection, max(rows.count - 1, 0))
    }

    /// Shows the palette with `query`, `selection` and `recents` over `items` without touching
    /// the stored recent items. Snapshot scenes use it.
    public func seed(items: [QuickSwitchItem], query: String, selection: Int = 0, recents: [String]) {
        self.recents = recents
        self.items = items
        self.query = query
        rank()
        self.selection = selection
        isPresented = true
    }

    /// Puts `id` first among the recent items.
    func record(_ id: String) {
        recents.removeAll { $0 == id }
        recents.insert(id, at: 0)
        if recents.count > Self.recentLimit { recents.removeLast(recents.count - Self.recentLimit) }
        if let data = try? JSONEncoder().encode(recents) { defaults.set(data, forKey: SettingsKey.QuickSwitch.recents) }
        rank()
    }
}
