import DeskhandClient
import Foundation

/// The scopes of the selected call frame, or evaluated objects, and the properties the person
/// expanded. Children load on expansion and are kept per object; expanded paths survive pauses
/// and the same variables stay open while stepping. A path is the root's path followed by
/// property names.
public struct VariableTree: Equatable, Sendable {
    /// Properties asked of the daemon per page.
    public static let pageSize = 100

    public typealias Path = [String]

    /// An object whose properties are needed, and the target that owns it.
    public struct Load: Equatable, Hashable, Sendable {
        public var objectID: String
        public var targetID: String

        public init(objectID: String, targetID: String) {
            self.objectID = objectID
            self.targetID = targetID
        }
    }

    public enum Children: Equatable, Sendable {
        case loading
        case loaded([DebugProperty], total: Int)
        case failed(OperationError)
    }

    public struct Row: Identifiable, Equatable, Sendable {
        public enum Kind: Hashable, Sendable {
            case scope
            case property
            case loading
            case failure
            case more
        }

        public struct ID: Hashable, Sendable {
            public var path: Path
            public var kind: Kind
        }

        public var kind: Kind
        public var path: Path
        public var depth: Int
        public var title: String
        public var value: RemoteValue?
        public var propertyKind: DebugPropertyKind = .property
        public var scopeType: String?
        public var isExpandable = false
        public var isExpanded = false
        /// The object whose children this row shows, loads, fails or pages.
        public var objectID: String?
        public var targetID: String
        public var error: OperationError?
        public var remaining = 0
        /// The next page of a more row is being read.
        public var busy = false

        public var id: ID { ID(path: path, kind: kind) }
    }

    struct Root: Equatable, Sendable {
        var path: Path
        var title: String
        var objectID: String
        /// The scope type for scope roots; nil for an evaluated object.
        var scopeType: String?
        var value: RemoteValue?
        var targetID: String
    }

    /// The pause the scope object IDs belong to.
    public private(set) var gen = 0
    public private(set) var frameID: String?
    private(set) var roots: [Root] = []
    private(set) var children: [String: Children] = [:]
    private(set) var expanded: Set<Path> = []
    /// Objects whose next page is being read.
    private(set) var loadingMore: Set<String> = []
    private var started = false

    public init() {}

    /// Shows the scopes of `frame` from pause `gen` of `targetID`. Loaded children are dropped;
    /// expanded paths stay.
    public mutating func reset(frame: DebugCallFrame?, gen: Int, targetID: String) {
        self.gen = gen
        frameID = frame?.frameID
        children = [:]
        loadingMore = []
        roots = (frame?.scopeChain ?? []).enumerated().map { index, scope in
            Root(path: ["\(index):\(scope.type)"], title: Self.title(of: scope), objectID: scope.objectID, scopeType: scope.type,
                 targetID: targetID)
        }
        if !started, let first = roots.first {
            started = true
            expanded.insert(first.path)
        }
    }

    /// Adds a collapsed root for an object outside the scopes, such as an evaluation result.
    public mutating func addRoot(path: Path, title: String, value: RemoteValue, targetID: String, first: Bool = false) {
        guard let objectID = value.objectID, !roots.contains(where: { $0.path == path }) else { return }
        let root = Root(path: path, title: title, objectID: objectID, value: value, targetID: targetID)
        if first { roots.insert(root, at: 0) } else { roots.append(root) }
    }

    /// Clears the scopes, as when the target runs.
    public mutating func clear() {
        frameID = nil
        roots = []
        children = [:]
        loadingMore = []
    }

    public mutating func toggle(_ path: Path) {
        if expanded.contains(path) { expanded.remove(path) } else { expanded.insert(path) }
    }

    public func isExpanded(_ path: Path) -> Bool { expanded.contains(path) }

    public mutating func setExpanded(_ path: Path, _ open: Bool) {
        if open { expanded.insert(path) } else { expanded.remove(path) }
    }

    public mutating func beginLoading(_ objectID: String) {
        if children[objectID] == nil { children[objectID] = .loading }
    }

    /// Marks the next page of `objectID` as in flight; false when one already is.
    public mutating func beginLoadingMore(_ objectID: String) -> Bool {
        guard case .loaded = children[objectID] else { return false }
        return loadingMore.insert(objectID).inserted
    }

    /// Stores a page of `objectID`'s properties; `append` adds it after the loaded ones.
    public mutating func finishLoading(_ objectID: String, _ result: Result<DebugPropertyPage, OperationError>, append: Bool = false) {
        if append { loadingMore.remove(objectID) }
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

    /// The target that owns the objects under the root at the start of `path`.
    public func targetID(of path: Path) -> String? {
        roots.first { $0.path.first == path.first }?.targetID
    }

    /// Objects shown expanded whose children were never asked for.
    public var pendingLoads: [Load] {
        var pending: [Load] = []
        for root in roots where expanded.contains(root.path) {
            collectPending(objectID: root.objectID, path: root.path, targetID: root.targetID, into: &pending)
        }
        return pending
    }

    private func collectPending(objectID: String, path: Path, targetID: String, into pending: inout [Load]) {
        guard let state = children[objectID] else {
            let load = Load(objectID: objectID, targetID: targetID)
            if !pending.contains(load) { pending.append(load) }
            return
        }
        guard case .loaded(let props, _) = state else { return }
        for prop in props {
            let child = path + [prop.name]
            guard expanded.contains(child), let id = prop.value.objectID, prop.value.hasChildren else { continue }
            collectPending(objectID: id, path: child, targetID: targetID, into: &pending)
        }
    }

    /// Rows in display order.
    public var rows: [Row] {
        var rows: [Row] = []
        for root in roots {
            let open = expanded.contains(root.path)
            rows.append(Row(kind: .scope, path: root.path, depth: 0, title: root.title, value: root.value, scopeType: root.scopeType,
                            isExpandable: true, isExpanded: open, objectID: root.objectID, targetID: root.targetID))
            if open { appendChildren(of: root.objectID, path: root.path, depth: 1, targetID: root.targetID, into: &rows) }
        }
        return rows
    }

    /// The rows below the root at `path` while it is expanded.
    public func rows(under path: Path) -> [Row] {
        guard let root = roots.first(where: { $0.path == path }), expanded.contains(path) else { return [] }
        var rows: [Row] = []
        appendChildren(of: root.objectID, path: root.path, depth: 1, targetID: root.targetID, into: &rows)
        return rows
    }

    private func appendChildren(of objectID: String, path: Path, depth: Int, targetID: String, into rows: inout [Row]) {
        switch children[objectID] {
        case nil, .loading:
            rows.append(Row(kind: .loading, path: path, depth: depth, title: "", objectID: objectID, targetID: targetID))
        case .failed(let error):
            rows.append(Row(kind: .failure, path: path, depth: depth, title: "", objectID: objectID, targetID: targetID, error: error))
        case .loaded(let props, let total):
            for prop in props {
                let child = path + [prop.name]
                let expandable = prop.value.hasChildren && prop.value.objectID != nil
                let open = expandable && expanded.contains(child)
                rows.append(Row(kind: .property, path: child, depth: depth, title: prop.name, value: prop.value,
                                propertyKind: prop.propertyKind, isExpandable: expandable, isExpanded: open,
                                objectID: prop.value.objectID, targetID: targetID))
                if open, let id = prop.value.objectID { appendChildren(of: id, path: child, depth: depth + 1, targetID: targetID, into: &rows) }
            }
            if props.count < total {
                rows.append(Row(kind: .more, path: path, depth: depth, title: "", objectID: objectID, targetID: targetID,
                                remaining: total - props.count, busy: loadingMore.contains(objectID)))
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

/// Per target, the latest pause state, applied only when its `gen` is larger than the applied
/// one (local-api.md §9). A target that reports a newer pause without its state is announced:
/// its `gen` counts only once the state arrives.
public struct PauseBook: Equatable, Sendable {
    public enum TargetUpdate: Equatable, Sendable {
        case unchanged
        /// The target paused at a `gen` whose call stack is not known yet; read `debug.state`.
        case needsState
        case resumed
    }

    private(set) var states: [String: DebugPauseState] = [:]
    /// The newest paused `gen` a target reported without its state.
    private(set) var announced: [String: Int] = [:]

    public init() {}

    public func state(for targetID: String) -> DebugPauseState? { states[targetID] }

    public func isPaused(_ targetID: String) -> Bool { states[targetID]?.paused ?? false }

    /// The `gen` of the applied state.
    public func gen(of targetID: String) -> Int { states[targetID]?.gen ?? -1 }

    /// The paused `gen` waiting for its state, when one is newer than the applied state.
    public func announcedGen(of targetID: String) -> Int? {
        guard let gen = announced[targetID], gen > self.gen(of: targetID) else { return nil }
        return gen
    }

    /// Stores `state` when its `gen` is newer than the applied state.
    @discardableResult
    public mutating func apply(_ state: DebugPauseState) -> Bool {
        guard state.gen > gen(of: state.targetID) else { return false }
        states[state.targetID] = state
        if let waiting = announced[state.targetID], waiting <= state.gen { announced[state.targetID] = nil }
        return true
    }

    /// Stores a resume newer than both the applied state and an announced pause.
    @discardableResult
    public mutating func applyResumed(targetID: String, gen: Int) -> Bool {
        guard gen > self.gen(of: targetID), gen > (announced[targetID] ?? -1) else { return false }
        states[targetID] = DebugPauseState(targetID: targetID, gen: gen, paused: false)
        announced[targetID] = nil
        return true
    }

    /// Reads `paused` and `gen` of a target from a list, a reply or an event.
    public mutating func apply(_ target: DebugTarget) -> TargetUpdate {
        guard target.gen > gen(of: target.id) else { return .unchanged }
        if target.paused {
            announced[target.id] = max(announced[target.id] ?? -1, target.gen)
            return .needsState
        }
        return applyResumed(targetID: target.id, gen: target.gen) ? .resumed : .unchanged
    }

    /// Drops a target that left the session.
    public mutating func remove(_ targetID: String) {
        states[targetID] = nil
        announced[targetID] = nil
    }
}
