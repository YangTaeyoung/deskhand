import DeskhandClient
import Foundation

// MARK: Console

/// Which console entries the console shows.
public struct ConsoleFilter: Equatable, Sendable {
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

    func matches(_ entry: ConsoleEntry) -> Bool {
        guard levels.contains(entry.consoleLevel) else { return false }
        if let targetID, entry.targetID != targetID { return false }
        let needle = query.trimmingCharacters(in: .whitespaces)
        guard !needle.isEmpty else { return true }
        return entry.text.localizedCaseInsensitiveContains(needle)
            || (entry.url.map { $0.localizedCaseInsensitiveContains(needle) } ?? false)
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
    public var targetID: String
    /// The function of the frame it ran in, or nil for a global evaluation.
    public var frame: String?
    public var afterSeq: Int
    public var time: Date
    public var result: EvaluationResult
}

/// A row of the console: an entry from the daemon or an evaluation.
public enum ConsoleItem: Identifiable, Equatable, Sendable {
    case entry(ConsoleEntry)
    case evaluation(EvaluationRecord)

    public var id: String {
        switch self {
        case .entry(let entry): "e\(entry.seq)"
        case .evaluation(let record): "x\(record.id)"
        }
    }
}

/// The console of a session: daemon entries in `seq` order without duplicates, at most
/// `capacity` of them, with the evaluations the person ran placed among them.
public struct ConsoleLog: Equatable, Sendable {
    public let capacity: Int
    /// Ascending `seq`.
    public private(set) var entries: [ConsoleEntry] = []
    /// Older entries than `entries.first` exist (on the daemon or trimmed here).
    public private(set) var hasEarlier = false
    /// Entries the daemon discarded during the session.
    public private(set) var dropped = 0
    public private(set) var evaluations: [EvaluationRecord] = []
    /// Entries up to this `seq` were cleared from view.
    public private(set) var clearedThrough = 0
    private var knownCursor = 0
    private var nextEvaluationID = 1

    public init(capacity: Int = 5000) {
        self.capacity = capacity
    }

    /// The largest `seq` received, to pass as `cursor`.
    public var cursor: Int { max(entries.last?.seq ?? 0, knownCursor) }
    /// The smallest `seq` held, to pass as `before`.
    public var firstSeq: Int? { entries.first?.seq }

    /// Adds entries not held yet and returns whether old entries were trimmed to `capacity`.
    @discardableResult
    public mutating func merge(_ incoming: [ConsoleEntry]) -> Bool {
        guard !incoming.isEmpty else { return false }
        var bySeq = Dictionary(entries.map { ($0.seq, $0) }, uniquingKeysWith: { first, _ in first })
        for entry in incoming where bySeq[entry.seq] == nil { bySeq[entry.seq] = entry }
        var merged = bySeq.values.sorted { $0.seq < $1.seq }
        var trimmed = false
        if merged.count > capacity {
            merged.removeFirst(merged.count - capacity)
            hasEarlier = true
            trimmed = true
        }
        entries = merged
        return trimmed
    }

    /// Applies a page of `debug.console`; `earlier` for a page read with `before`. The first page
    /// and earlier pages say whether still older entries exist.
    public mutating func apply(_ page: ConsolePage, earlier: Bool = false) {
        let first = entries.isEmpty
        let trimmed = merge(page.entries)
        if earlier || first { hasEarlier = page.hasEarlier || trimmed }
        dropped = max(dropped, page.dropped)
        knownCursor = max(knownCursor, page.nextCursor)
    }

    /// Hides every entry and evaluation so far; later entries show.
    public mutating func clear() {
        clearedThrough = cursor
        evaluations = []
    }

    public func visible(_ filter: ConsoleFilter) -> [ConsoleEntry] {
        entries.filter { $0.seq > clearedThrough && filter.matches($0) }
    }

    /// Shown entries per level, ignoring the level filter.
    public func counts(_ filter: ConsoleFilter) -> [ConsoleLevel: Int] {
        var all = filter
        all.levels = Set(ConsoleLevel.allCases)
        var counts: [ConsoleLevel: Int] = [:]
        for entry in visible(all) { counts[entry.consoleLevel, default: 0] += 1 }
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

    /// Shown entries with the evaluations placed after the entry that was the latest when each ran.
    public func items(_ filter: ConsoleFilter) -> [ConsoleItem] {
        let shown = visible(filter)
        var items: [ConsoleItem] = []
        items.reserveCapacity(shown.count + evaluations.count)
        var pending = evaluations[...]
        for entry in shown {
            while let next = pending.first, next.afterSeq < entry.seq {
                items.append(.evaluation(next))
                pending = pending.dropFirst()
            }
            items.append(.entry(entry))
        }
        items.append(contentsOf: pending.map(ConsoleItem.evaluation))
        return items
    }
}

// MARK: History

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

// MARK: Variables

/// The scopes of the selected call frame and the properties the person expanded. Children load
/// on expansion and are kept per object; expanded paths survive pauses and the same variables
/// stay open while stepping.
public struct VariableTree: Equatable, Sendable {
    /// Properties asked of the daemon per page.
    public static let pageSize = 100

    public enum Children: Equatable, Sendable {
        case loading
        case loaded([DebugProperty], total: Int)
        case failed(OperationError)
    }

    public struct Row: Identifiable, Equatable, Sendable {
        public enum Kind: Equatable, Sendable {
            case scope
            case property
            case loading
            case failure
            case more
        }

        public var id: String
        public var kind: Kind
        public var path: String
        public var depth: Int
        public var title: String
        public var value: RemoteValue?
        public var propertyKind: DebugPropertyKind = .property
        public var scopeType: String?
        public var isExpandable = false
        public var isExpanded = false
        /// The object whose children this row loads, fails or pages.
        public var objectID: String?
        public var error: OperationError?
        public var remaining = 0
    }

    struct Root: Equatable, Sendable {
        var path: String
        var title: String
        var objectID: String
        /// The scope type for scope roots; nil for an evaluated object.
        var scopeType: String?
        var value: RemoteValue?
    }

    /// The pause the object IDs belong to.
    public private(set) var gen = 0
    public private(set) var frameID: String?
    private(set) var roots: [Root] = []
    private(set) var children: [String: Children] = [:]
    private(set) var expanded: Set<String> = []
    private var started = false

    public init() {}

    /// Shows the scopes of `frame` from pause `gen`. Loaded children are dropped; expanded paths stay.
    public mutating func reset(frame: DebugCallFrame?, gen: Int) {
        self.gen = gen
        frameID = frame?.frameID
        children = [:]
        roots = (frame?.scopeChain ?? []).enumerated().map { index, scope in
            Root(path: "\(index):\(scope.type)", title: Self.title(of: scope), objectID: scope.objectID, scopeType: scope.type)
        }
        if !started, let first = roots.first {
            started = true
            expanded.insert(first.path)
        }
    }

    /// Adds a collapsed root for an object outside the scopes, such as an evaluation result.
    public mutating func addRoot(path: String, title: String, value: RemoteValue) {
        guard let objectID = value.objectID, !roots.contains(where: { $0.path == path }) else { return }
        roots.append(Root(path: path, title: title, objectID: objectID, value: value))
    }

    /// The rows below the root at `path` while it is expanded.
    public func rows(under path: String) -> [Row] {
        guard let root = roots.first(where: { $0.path == path }), expanded.contains(path) else { return [] }
        var rows: [Row] = []
        appendChildren(of: root.objectID, path: root.path, depth: 1, into: &rows)
        return rows
    }

    /// Clears the scopes, as when the target runs.
    public mutating func clear() {
        frameID = nil
        roots = []
        children = [:]
    }

    public mutating func toggle(_ path: String) {
        if expanded.contains(path) { expanded.remove(path) } else { expanded.insert(path) }
    }

    public func isExpanded(_ path: String) -> Bool { expanded.contains(path) }

    public mutating func setExpanded(_ path: String, _ open: Bool) {
        if open { expanded.insert(path) } else { expanded.remove(path) }
    }

    public mutating func beginLoading(_ objectID: String) {
        if children[objectID] == nil { children[objectID] = .loading }
    }

    /// Stores a page of `objectID`'s properties; `append` adds it after the loaded ones.
    public mutating func finishLoading(_ objectID: String, _ result: Result<DebugPropertyPage, OperationError>, append: Bool = false) {
        switch result {
        case .success(let page):
            if append, case .loaded(let current, _) = children[objectID] {
                children[objectID] = .loaded(current + page.properties, total: page.total)
            } else {
                children[objectID] = .loaded(page.properties, total: page.total)
            }
        case .failure(let error):
            if append, case .loaded = children[objectID] { return }
            children[objectID] = .failed(error)
        }
    }

    /// Forgets a failed load so it runs again.
    public mutating func retry(_ objectID: String) {
        if case .failed = children[objectID] { children[objectID] = nil }
    }

    /// The offset of the next page of `objectID`.
    public func nextOffset(_ objectID: String) -> Int {
        if case .loaded(let props, _) = children[objectID] { return props.count }
        return 0
    }

    /// Objects shown expanded whose children were never asked for.
    public var pendingLoads: [String] {
        var pending: [String] = []
        for root in roots where expanded.contains(root.path) {
            collectPending(objectID: root.objectID, path: root.path, into: &pending)
        }
        return pending
    }

    private func collectPending(objectID: String, path: String, into pending: inout [String]) {
        guard let state = children[objectID] else {
            if !pending.contains(objectID) { pending.append(objectID) }
            return
        }
        guard case .loaded(let props, _) = state else { return }
        for prop in props {
            let child = path + "/" + prop.name
            guard expanded.contains(child), let id = prop.value.objectID, prop.value.hasChildren else { continue }
            collectPending(objectID: id, path: child, into: &pending)
        }
    }

    /// Rows in display order.
    public var rows: [Row] {
        var rows: [Row] = []
        for root in roots {
            let open = expanded.contains(root.path)
            rows.append(Row(id: root.path, kind: .scope, path: root.path, depth: 0, title: root.title, value: root.value,
                            scopeType: root.scopeType, isExpandable: true, isExpanded: open, objectID: root.objectID))
            if open { appendChildren(of: root.objectID, path: root.path, depth: 1, into: &rows) }
        }
        return rows
    }

    private func appendChildren(of objectID: String, path: String, depth: Int, into rows: inout [Row]) {
        switch children[objectID] {
        case nil, .loading:
            rows.append(Row(id: path + "#loading", kind: .loading, path: path, depth: depth, title: "", objectID: objectID))
        case .failed(let error):
            rows.append(Row(id: path + "#failure", kind: .failure, path: path, depth: depth, title: "", objectID: objectID, error: error))
        case .loaded(let props, let total):
            for prop in props {
                let child = path + "/" + prop.name
                let expandable = prop.value.hasChildren && prop.value.objectID != nil
                let open = expandable && expanded.contains(child)
                rows.append(Row(id: child, kind: .property, path: child, depth: depth, title: prop.name, value: prop.value,
                                propertyKind: prop.propertyKind, isExpandable: expandable, isExpanded: open, objectID: prop.value.objectID))
                if open, let id = prop.value.objectID { appendChildren(of: id, path: child, depth: depth + 1, into: &rows) }
            }
            if props.count < total {
                rows.append(Row(id: path + "#more", kind: .more, path: path, depth: depth, title: "", objectID: objectID,
                                remaining: total - props.count))
            }
        }
    }

    /// The scope's name in the tree: 지역, 클로저 and its function, 전역 and so on.
    static func title(of scope: DebugScope) -> String {
        let name = scope.name.flatMap { $0.isEmpty ? nil : $0 }
        switch scope.type {
        case "local": return .ui("debugger.scope.local", "지역")
        case "closure":
            if let name { return .ui("debugger.scope.closureNamed", "클로저 \(name)") }
            return .ui("debugger.scope.closure", "클로저")
        case "block": return .ui("debugger.scope.block", "블록")
        case "catch": return .ui("debugger.scope.catch", "catch 블록")
        case "with": return .ui("debugger.scope.with", "with 블록")
        case "script": return .ui("debugger.scope.script", "스크립트")
        case "module":
            if let name { return .ui("debugger.scope.moduleNamed", "모듈 \(name)") }
            return .ui("debugger.scope.module", "모듈")
        case "eval": return .ui("debugger.scope.eval", "eval")
        case "global": return .ui("debugger.scope.global", "전역")
        case "wasm_expression_stack", "wasm-expression-stack": return .ui("debugger.scope.wasm", "WebAssembly 스택")
        default: return scope.type
        }
    }
}

// MARK: Pause order

/// Per target, the latest pause state, applied only when its `gen` is larger than any seen
/// (local-api.md §9).
public struct PauseBook: Equatable, Sendable {
    public enum TargetUpdate: Equatable, Sendable {
        case unchanged
        /// The target paused at a `gen` whose call stack is not known yet; read `debug.state`.
        case needsState
        case resumed
    }

    private(set) var states: [String: DebugPauseState] = [:]
    private(set) var gens: [String: Int] = [:]

    public init() {}

    public func state(for targetID: String) -> DebugPauseState? { states[targetID] }

    public func isPaused(_ targetID: String) -> Bool { states[targetID]?.paused ?? false }

    /// Stores `state` when its `gen` is newer than the stored state and not older than any announced `gen`.
    @discardableResult
    public mutating func apply(_ state: DebugPauseState) -> Bool {
        let stored = states[state.targetID]?.gen ?? -1
        guard state.gen > stored, state.gen >= (gens[state.targetID] ?? -1) else { return false }
        states[state.targetID] = state
        gens[state.targetID] = state.gen
        return true
    }

    @discardableResult
    public mutating func applyResumed(targetID: String, gen: Int) -> Bool {
        guard gen > (gens[targetID] ?? -1) else { return false }
        states[targetID] = DebugPauseState(targetID: targetID, gen: gen, paused: false)
        gens[targetID] = gen
        return true
    }

    /// Reads `paused` and `gen` of a target from a list, a reply or an event.
    public mutating func apply(_ target: DebugTarget) -> TargetUpdate {
        guard target.gen > (gens[target.id] ?? -1) else { return .unchanged }
        gens[target.id] = target.gen
        if target.paused { return .needsState }
        states[target.id] = DebugPauseState(targetID: target.id, gen: target.gen, paused: false)
        return .resumed
    }

    /// Drops a target that left the session.
    public mutating func remove(_ targetID: String) {
        states[targetID] = nil
        gens[targetID] = nil
    }
}

// MARK: Breakpoint patterns

/// How the person names the script of a new breakpoint, and how a breakpoint is named back.
public enum BreakpointPattern: Equatable, Sendable {
    /// A file name such as `checkout.js`, matched at the end of any script URL.
    case fileName(String)
    /// An exact script URL.
    case url(String)
    /// A JavaScript regular expression over script URLs.
    case regex(String)

    public enum Problem: Error, Equatable, LocalizedError {
        case empty
        case invalidRegex(String)

        public var errorDescription: String? {
            switch self {
            case .empty: .ui("debugger.pattern.empty", "스크립트 이름이나 URL을 입력하세요.")
            case .invalidRegex(let reason): .ui("debugger.pattern.invalid", "정규식이 올바르지 않습니다: \(reason)")
            }
        }
    }

    private static let fileNamePrefix = #"(^|[/\\])"#

    /// The `url` or `url_regex` of `debug.breakpoint.set`.
    public func match() throws -> BreakpointRequest.Match {
        switch self {
        case .fileName(let name):
            let trimmed = name.trimmingCharacters(in: .whitespaces)
            guard !trimmed.isEmpty else { throw Problem.empty }
            return .regex(Self.fileNamePrefix + NSRegularExpression.escapedPattern(for: trimmed) + "$")
        case .url(let url):
            let trimmed = url.trimmingCharacters(in: .whitespaces)
            guard !trimmed.isEmpty else { throw Problem.empty }
            return .url(trimmed)
        case .regex(let pattern):
            guard !pattern.trimmingCharacters(in: .whitespaces).isEmpty else { throw Problem.empty }
            do {
                _ = try NSRegularExpression(pattern: pattern)
            } catch {
                throw Problem.invalidRegex((error as NSError).localizedFailureReason ?? pattern)
            }
            return .regex(pattern)
        }
    }

    /// The script part of a breakpoint's name: the file name for exact URLs and file-name
    /// patterns, `/regex/` otherwise.
    public static func scriptTitle(of breakpoint: DebugBreakpoint) -> String {
        if let url = breakpoint.url { return ScriptURL.fileName(url) }
        guard let regex = breakpoint.urlRegex else { return "" }
        if regex.hasPrefix(fileNamePrefix), regex.hasSuffix("$") {
            let escaped = String(regex.dropFirst(fileNamePrefix.count).dropLast())
            let name = unescape(escaped)
            if NSRegularExpression.escapedPattern(for: name) == escaped { return name }
        }
        return "/" + regex + "/"
    }

    /// `checkout.js:42`.
    public static func title(of breakpoint: DebugBreakpoint) -> String {
        let script = scriptTitle(of: breakpoint)
        let line = String(breakpoint.line)
        return script + ":" + line
    }

    /// Whether the breakpoint would bind to a script at `url`.
    public static func matches(_ breakpoint: DebugBreakpoint, url: String) -> Bool {
        if let exact = breakpoint.url { return exact == url }
        guard let pattern = breakpoint.urlRegex, let regex = try? NSRegularExpression(pattern: pattern) else { return false }
        return regex.firstMatch(in: url, range: NSRange(url.startIndex..., in: url)) != nil
    }

    private static func unescape(_ text: String) -> String {
        var out = ""
        var escaping = false
        for c in text {
            if escaping {
                out.append(c)
                escaping = false
            } else if c == "\\" {
                escaping = true
            } else {
                out.append(c)
            }
        }
        return out
    }
}

/// Short forms of script URLs.
public enum ScriptURL {
    /// The last path component: `checkout.js` of `app://shop/renderer/checkout.js`.
    public static func fileName(_ url: String) -> String {
        let trimmed = url.split(separator: "?", maxSplits: 1).first.map(String.init) ?? url
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
}

// MARK: Arguments

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
