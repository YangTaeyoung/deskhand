import DeskhandClient
import Foundation

/// Columns of the process table.
public enum ProcessColumn: String, CaseIterable, Sendable {
    case name
    case pid
    case cpu
    case memory
    case user
    case started
    case path

    public var title: String {
        switch self {
        case .name: .ui("processes.column.name", "이름")
        case .pid: .ui("processes.column.pid", "PID")
        case .cpu: .ui("processes.column.cpu", "CPU %")
        case .memory: .ui("processes.column.memory", "메모리")
        case .user: .ui("processes.column.user", "사용자")
        case .started: .ui("processes.column.started", "시작")
        case .path: .ui("processes.column.path", "경로")
        }
    }

    /// Direction of the first click on the column header: numbers and times start with the largest.
    public var firstSortAscending: Bool {
        switch self {
        case .cpu, .memory, .started: false
        case .name, .pid, .user, .path: true
        }
    }
}

/// Column and direction the process table sorts by.
public struct ProcessSort: Equatable, Sendable {
    public var column: ProcessColumn
    public var ascending: Bool

    public init(column: ProcessColumn, ascending: Bool) {
        self.column = column
        self.ascending = ascending
    }

    /// Busiest first.
    public static let standard = ProcessSort(column: .cpu, ascending: false)
}

/// One row of the process table.
public struct ProcessRow: Identifiable, Equatable, Sendable {
    public var process: RemoteProcess
    /// Nesting level in the tree view; 0 in the flat list.
    public var depth: Int
    public var hasChildren: Bool
    public var isExpanded: Bool
    /// A row that does not match the search, shown as the ancestor of a match.
    public var isContext: Bool

    public var id: Int { process.pid }
}

/// Parent and child links of a process list. A process is a child of the process its `ppid`
/// names only when that process did not start after it; a `ppid` of 0 names no parent.
public struct ProcessForest: Sendable {
    public let processes: [RemoteProcess]
    private let byPID: [Int: RemoteProcess]
    private let parents: [Int: Int]
    private let childPIDs: [Int: [Int]]

    public init(_ processes: [RemoteProcess]) {
        self.processes = processes
        var byPID: [Int: RemoteProcess] = [:]
        for process in processes { byPID[process.pid] = process }
        var parents: [Int: Int] = [:]
        var children: [Int: [Int]] = [:]
        for process in processes where process.ppid != process.pid && process.ppid != 0 {
            guard let parent = byPID[process.ppid] else { continue }
            if let parentStart = parent.started?.date, let childStart = process.started?.date, parentStart > childStart { continue }
            parents[process.pid] = parent.pid
            children[parent.pid, default: []].append(process.pid)
        }
        self.byPID = byPID
        self.parents = parents
        self.childPIDs = children
    }

    public func process(_ pid: Int) -> RemoteProcess? { byPID[pid] }

    public func parent(of pid: Int) -> RemoteProcess? { parents[pid].flatMap { byPID[$0] } }

    public func children(of pid: Int) -> [RemoteProcess] { (childPIDs[pid] ?? []).compactMap { byPID[$0] } }

    /// Every process below `pid`, parents before their children, each once.
    public func descendants(of pid: Int) -> [RemoteProcess] {
        var result: [RemoteProcess] = []
        var seen: Set<Int> = [pid]
        var queue = childPIDs[pid] ?? []
        while !queue.isEmpty {
            let next = queue.removeFirst()
            guard seen.insert(next).inserted, let process = byPID[next] else { continue }
            result.append(process)
            queue += childPIDs[next] ?? []
        }
        return result
    }

    /// Processes without a listed parent, then one process of each parent loop.
    fileprivate var roots: [RemoteProcess] {
        processes.filter { parents[$0.pid] == nil }
    }
}

/// Sorting, search and tree layout of the process table.
public enum ProcessList {
    /// The query matches a name or path containing it, ignoring case, or a PID starting with it.
    public static func matches(_ process: RemoteProcess, query: String) -> Bool {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !needle.isEmpty else { return true }
        if String(process.pid).hasPrefix(needle) { return true }
        if process.name.localizedCaseInsensitiveContains(needle) { return true }
        return process.path?.localizedCaseInsensitiveContains(needle) ?? false
    }

    /// `System Idle Process` (PID 0), whose CPU value is unused capacity rather than use.
    public static func isIdle(_ process: RemoteProcess) -> Bool { process.pid == 0 }

    /// Whether `a` comes before `b`. Missing values, and the CPU value of the idle process, go
    /// last in both directions; ties go by name, then PID.
    public static func precedes(_ a: RemoteProcess, _ b: RemoteProcess, by sort: ProcessSort) -> Bool {
        if let order = compare(a, b, sort.column) {
            if order != .orderedSame { return sort.ascending ? order == .orderedAscending : order == .orderedDescending }
        } else {
            let aMissing = isMissing(a, sort.column)
            let bMissing = isMissing(b, sort.column)
            if aMissing != bMissing { return bMissing }
        }
        if sort.column != .name, let order = compare(a, b, .name), order != .orderedSame { return order == .orderedAscending }
        return a.pid < b.pid
    }

    private static func isMissing(_ process: RemoteProcess, _ column: ProcessColumn) -> Bool {
        switch column {
        case .cpu: process.cpuPct == nil || isIdle(process)
        case .user: process.user == nil
        case .started: process.started == nil
        case .path: process.path == nil
        case .name, .pid, .memory: false
        }
    }

    /// The order of two values of `column`, or nil when either is missing.
    private static func compare(_ a: RemoteProcess, _ b: RemoteProcess, _ column: ProcessColumn) -> ComparisonResult? {
        func order<T: Comparable>(_ x: T?, _ y: T?) -> ComparisonResult? {
            guard let x, let y else { return nil }
            return x < y ? .orderedAscending : (x > y ? .orderedDescending : .orderedSame)
        }
        switch column {
        case .name: return a.name.localizedStandardCompare(b.name)
        case .pid: return order(a.pid, b.pid)
        case .cpu: return isIdle(a) || isIdle(b) ? nil : order(a.cpuPct, b.cpuPct)
        case .memory: return order(a.memKb, b.memKb)
        case .user:
            guard let x = a.user, let y = b.user else { return nil }
            return x.localizedStandardCompare(y)
        case .started: return order(a.started?.date, b.started?.date)
        case .path:
            guard let x = a.path, let y = b.path else { return nil }
            return x.localizedStandardCompare(y)
        }
    }

    public static func sorted(_ processes: [RemoteProcess], by sort: ProcessSort) -> [RemoteProcess] {
        processes.sorted { precedes($0, $1, by: sort) }
    }

    /// The rows to show: matching processes in `sort` order, or with `tree`, nested under their
    /// parents with siblings in `sort` order. A search in the tree keeps the ancestors of every
    /// match as context rows and opens collapsed ancestors.
    public static func rows(_ processes: [RemoteProcess], query: String, sort: ProcessSort, tree: Bool,
                            collapsed: Set<Int>) -> [ProcessRow] {
        let searching = !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        guard tree else {
            return sorted(processes.filter { matches($0, query: query) }, by: sort).map {
                ProcessRow(process: $0, depth: 0, hasChildren: false, isExpanded: false, isContext: false)
            }
        }
        let forest = ProcessForest(processes)
        var shown: Set<Int>?
        if searching {
            var keep: Set<Int> = []
            for process in processes where matches(process, query: query) {
                var pid: Int? = process.pid
                var guardSet: Set<Int> = []
                while let current = pid, guardSet.insert(current).inserted {
                    keep.insert(current)
                    pid = forest.parent(of: current)?.pid
                }
            }
            shown = keep
        }
        var rows: [ProcessRow] = []
        var visited: Set<Int> = []
        func visit(_ process: RemoteProcess, depth: Int) {
            guard visited.insert(process.pid).inserted else { return }
            if let shown, !shown.contains(process.pid) { return }
            let children = sorted(forest.children(of: process.pid).filter { shown?.contains($0.pid) ?? true }, by: sort)
            let expanded = searching || !collapsed.contains(process.pid)
            rows.append(ProcessRow(process: process, depth: depth, hasChildren: !children.isEmpty, isExpanded: expanded,
                                   isContext: searching && !matches(process, query: query)))
            guard expanded else {
                for descendant in forest.descendants(of: process.pid) { visited.insert(descendant.pid) }
                return
            }
            for child in children { visit(child, depth: depth + 1) }
        }
        for root in sorted(forest.roots, by: sort) { visit(root, depth: 0) }
        for process in sorted(processes, by: sort) where !visited.contains(process.pid) { visit(process, depth: 0) }
        return rows
    }
}

/// Why the app does not offer to end a process.
public enum KillBlock: Equatable, Sendable {
    /// The Deskhand agent that answers this app.
    case agent
    /// A Windows process the system stops without.
    case critical
    /// Ending the tree would end a protected descendant.
    case protectedDescendant(name: String)

    public var reason: String {
        switch self {
        case .agent:
            .ui("processes.block.agent", "Deskhand 에이전트입니다. 끝내면 이 Mac과의 연결이 끊어집니다.")
        case .critical:
            .ui("processes.block.critical", "Windows 핵심 프로세스라 끝낼 수 없습니다. 끝내면 컴퓨터가 멈추거나 다시 시작됩니다.")
        case .protectedDescendant(let name):
            .ui("processes.block.descendant", "하위에 ‘\(name)’ 프로세스가 있어 트리로 끝낼 수 없습니다. 이 프로세스만 종료하세요.")
        }
    }
}

/// The processes the app never offers to end.
public enum ProcessGuard {
    /// Lowercased names of Windows processes the system cannot run without.
    static let criticalNames: Set<String> = [
        "system", "system idle process", "idle", "registry", "secure system", "memory compression",
        "smss.exe", "csrss.exe", "wininit.exe", "services.exe", "lsass.exe", "lsaiso.exe", "winlogon.exe",
    ]
    /// Lowercased executable names of the Deskhand agent, for agents that do not report `self`.
    static let agentNames: Set<String> = ["deskhand-agent.exe"]

    public static func block(for process: RemoteProcess) -> KillBlock? {
        if process.isSelf { return .agent }
        let name = process.name.lowercased()
        if agentNames.contains(name) { return .agent }
        if process.pid == 0 || process.pid == 4 || criticalNames.contains(name) { return .critical }
        return nil
    }

    /// Why the tree of `process` cannot be ended: its own block, or the first protected descendant.
    public static func treeBlock(for process: RemoteProcess, descendants: [RemoteProcess]) -> KillBlock? {
        if let own = block(for: process) { return own }
        if let protected = descendants.first(where: { block(for: $0) != nil }) {
            return .protectedDescendant(name: protected.name)
        }
        return nil
    }
}
