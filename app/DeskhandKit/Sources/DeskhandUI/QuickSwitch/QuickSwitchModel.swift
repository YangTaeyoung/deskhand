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
    }
}

/// A result row: the item and the offsets of the title characters the query matched.
public struct QuickSwitchRow: Identifiable, Equatable, Sendable {
    public let item: QuickSwitchItem
    public let highlights: [Int]

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
            guard let (base, highlights) = match(trimmed, words: words, item: item) else { continue }
            let score = base + (recents.firstIndex(of: item.id).map { max(4, 12 - 2 * $0) } ?? 0)
            scored.append((QuickSwitchRow(item: item, highlights: highlights), score))
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

    /// The item's score and title highlights for the query, or nil when it does not match well enough.
    private static func match(_ query: String, words: [String], item: QuickSwitchItem) -> (Int, [Int])? {
        if let whole = bestMatch(query, item: item, subtitle: false) { return whole }
        guard words.count > 1 else { return nil }
        var total = 0
        var highlights: [Int] = []
        var matchedTitleOrKeyword = false
        for word in words {
            guard let (score, indices, field) = bestField(word, item: item, subtitle: true) else { return nil }
            total += score
            highlights += indices
            if field != .subtitle { matchedTitleOrKeyword = true }
        }
        guard matchedTitleOrKeyword else { return nil }
        return (total / words.count, Array(Set(highlights)).sorted())
    }

    private static func bestMatch(_ text: String, item: QuickSwitchItem, subtitle: Bool) -> (Int, [Int])? {
        bestField(text, item: item, subtitle: subtitle).map { ($0.0, $0.1) }
    }

    private enum Field { case title, keyword, subtitle }

    private static func bestField(_ text: String, item: QuickSwitchItem, subtitle: Bool) -> (Int, [Int], Field)? {
        var best: (Int, [Int], Field)?
        func consider(_ score: Int, _ indices: [Int], _ field: Field) {
            if best.map({ score > $0.0 }) ?? true { best = (score, indices, field) }
        }
        let minimum = text.filter { !$0.isWhitespace }.count * minimumScorePerCharacter
        if let title = FuzzyMatcher.match(text, in: item.title), title.score >= minimum { consider(title.score, title.indices, .title) }
        for keyword in item.keywords {
            if let match = FuzzyMatcher.match(text, in: keyword), match.score >= minimum {
                consider(match.score - keywordPenalty, [], .keyword)
            }
        }
        if subtitle, let line = item.subtitle, let match = FuzzyMatcher.match(text, in: line), match.score >= minimum {
            consider(match.score - subtitlePenalty, [], .subtitle)
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
    /// ⌘ with a digit key, 1 through 9 for the visible rows.
    case commandDigit(Int)
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
/// opens the selected row, ⌘1–⌘9 open the first nine rows, and esc clears what is typed and
/// then closes.
public enum QuickSwitchNavigator {
    public static func effect(for key: QuickSwitchKey, selection: Int, count: Int, queryIsEmpty: Bool) -> QuickSwitchEffect {
        switch key {
        case .down:
            return selection + 1 < count ? .select(selection + 1) : .none
        case .up:
            return selection > 0 && count > 0 ? .select(min(selection, count) - 1) : .none
        case .enter:
            return count > 0 ? .activate(min(max(selection, 0), count - 1)) : .none
        case .commandDigit(let digit):
            return (1...9).contains(digit) && digit <= count ? .activate(digit - 1) : .none
        case .escape:
            return queryIsEmpty ? .dismiss : .clearQuery
        }
    }
}

/// State of the quick switch palette (⌘K): whether it shows, what is typed, the selected row
/// and the items opened from it recently. The recent items are kept in UserDefaults.
@MainActor
@Observable
public final class QuickSwitchModel {
    /// Recently opened items kept at most.
    static let recentLimit = 8

    public private(set) var isPresented = false
    public var query = "" {
        didSet { if query != oldValue { selection = 0 } }
    }
    public var selection = 0
    /// IDs of items opened from the palette, newest first.
    public private(set) var recents: [String]
    /// A key a menu command passed on while the palette shows, for the palette to apply to its rows.
    public private(set) var pendingKey: QuickSwitchKey?

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        recents = defaults.data(forKey: SettingsKey.QuickSwitch.recents)
            .flatMap { try? JSONDecoder().decode([String].self, from: $0) } ?? []
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

    /// Passes `key` from a menu command to the palette.
    public func send(_ key: QuickSwitchKey) {
        pendingKey = key
    }

    /// Takes the key a menu command passed on, if any.
    public func takePendingKey() -> QuickSwitchKey? {
        defer { pendingKey = nil }
        return pendingKey
    }

    /// Applies `key` to `rows`; returns the item to open, if the key opens one.
    public func handle(_ key: QuickSwitchKey, rows: [QuickSwitchRow]) -> QuickSwitchItem? {
        switch QuickSwitchNavigator.effect(for: key, selection: selection, count: rows.count, queryIsEmpty: query.isEmpty) {
        case .none: return nil
        case .select(let index): selection = index
        case .activate(let index): return rows[index].item
        case .clearQuery: query = ""
        case .dismiss: dismiss()
        }
        return nil
    }

    /// Shows the palette with `query`, `selection` and `recents` without touching the stored
    /// recent items. Snapshot scenes use it.
    public func seed(query: String, selection: Int = 0, recents: [String]) {
        self.query = query
        self.selection = selection
        self.recents = recents
        isPresented = true
    }

    /// Puts `id` first among the recent items.
    func record(_ id: String) {
        recents.removeAll { $0 == id }
        recents.insert(id, at: 0)
        if recents.count > Self.recentLimit { recents.removeLast(recents.count - Self.recentLimit) }
        if let data = try? JSONEncoder().encode(recents) { defaults.set(data, forKey: SettingsKey.QuickSwitch.recents) }
    }
}
