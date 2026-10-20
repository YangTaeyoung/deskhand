import Foundation
import Observation

/// Files tab settings the person chose: the conflict policy, move confirmation, each pane's
/// sort, hidden files and columns, the last folders, and the queue drawer. Kept in UserDefaults.
@MainActor
@Observable
public final class FilesPreferences {
    enum Keys {
        static let conflictPolicy = "filesConflictPolicy"
        static let confirmMoves = "filesConfirmMoves"
        static let sort = "filesSort"
        static let showsHidden = "filesShowsHidden"
        static let columnWidths = "filesColumnWidths"
        static let hiddenColumns = "filesHiddenColumns"
        static let localPath = "filesLocalPath"
        static let remotePaths = "filesRemotePaths"
        static let queueExpanded = "filesQueueExpanded"
        static let queueHeight = "filesQueueHeight"
        static let split = "filesSplitFraction"
    }

    /// What a transfer does when the destination has an item of the same name. `fail` asks
    /// with the conflict sheet before the transfer starts.
    public var conflictPolicy: ConflictPolicy {
        didSet { defaults.set(conflictPolicy.rawValue, forKey: Keys.conflictPolicy) }
    }

    /// Moves from menus and the toolbar ask for confirmation first.
    public var confirmMoves: Bool {
        didSet { defaults.set(confirmMoves, forKey: Keys.confirmMoves) }
    }

    public var queueExpanded: Bool {
        didSet { defaults.set(queueExpanded, forKey: Keys.queueExpanded) }
    }

    /// Height of the expanded queue drawer in points.
    public var queueHeight: Double {
        didSet { defaults.set(queueHeight, forKey: Keys.queueHeight) }
    }

    /// Share of the width the local pane takes.
    public var splitFraction: Double {
        didSet { defaults.set(splitFraction, forKey: Keys.split) }
    }

    /// The local pane's last folder.
    public var localPath: String? {
        didSet { defaults.set(localPath, forKey: Keys.localPath) }
    }

    private var sorts: [String: String] {
        didSet { defaults.set(sorts, forKey: Keys.sort) }
    }

    private var hidden: [String: Bool] {
        didSet { defaults.set(hidden, forKey: Keys.showsHidden) }
    }

    /// Widths by `<side>.<column>`.
    private var widths: [String: Double] {
        didSet { defaults.set(widths, forKey: Keys.columnWidths) }
    }

    /// Hidden columns as `<side>.<column>`.
    private var hiddenColumns: [String] {
        didSet { defaults.set(hiddenColumns, forKey: Keys.hiddenColumns) }
    }

    /// Remote pane folders by host ID.
    private var remotePaths: [String: String] {
        didSet { defaults.set(remotePaths, forKey: Keys.remotePaths) }
    }

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        conflictPolicy = defaults.string(forKey: Keys.conflictPolicy).flatMap(ConflictPolicy.init(rawValue:)) ?? .fail
        confirmMoves = defaults.object(forKey: Keys.confirmMoves) as? Bool ?? true
        queueExpanded = defaults.object(forKey: Keys.queueExpanded) as? Bool ?? true
        queueHeight = defaults.object(forKey: Keys.queueHeight) as? Double ?? 232
        splitFraction = defaults.object(forKey: Keys.split) as? Double ?? 0.5
        localPath = defaults.string(forKey: Keys.localPath)
        sorts = defaults.dictionary(forKey: Keys.sort) as? [String: String] ?? [:]
        hidden = defaults.dictionary(forKey: Keys.showsHidden) as? [String: Bool] ?? [:]
        widths = defaults.dictionary(forKey: Keys.columnWidths) as? [String: Double] ?? [:]
        hiddenColumns = defaults.stringArray(forKey: Keys.hiddenColumns) ?? []
        remotePaths = defaults.dictionary(forKey: Keys.remotePaths) as? [String: String] ?? [:]
    }

    public func sort(for side: FileSide) -> FileSort {
        sorts[side.rawValue].flatMap(FileSort.init(storageValue:)) ?? .standard
    }

    public func setSort(_ sort: FileSort, for side: FileSide) {
        sorts[side.rawValue] = sort.storageValue
    }

    public func showsHidden(_ side: FileSide) -> Bool {
        hidden[side.rawValue] ?? false
    }

    public func setShowsHidden(_ shows: Bool, for side: FileSide) {
        hidden[side.rawValue] = shows
    }

    public func columnWidth(_ column: FileColumn, side: FileSide) -> Double? {
        widths["\(side.rawValue).\(column.rawValue)"]
    }

    public func setColumnWidth(_ width: Double, _ column: FileColumn, side: FileSide) {
        let key = "\(side.rawValue).\(column.rawValue)"
        guard widths[key] != width else { return }
        widths[key] = width
    }

    public func isColumnHidden(_ column: FileColumn, side: FileSide) -> Bool {
        hiddenColumns.contains("\(side.rawValue).\(column.rawValue)")
    }

    public func setColumn(_ column: FileColumn, hidden isHidden: Bool, side: FileSide) {
        let key = "\(side.rawValue).\(column.rawValue)"
        hiddenColumns.removeAll { $0 == key }
        if isHidden { hiddenColumns.append(key) }
    }

    public func remotePath(for hostID: String) -> String? {
        remotePaths[hostID]
    }

    public func setRemotePath(_ path: String, for hostID: String) {
        remotePaths[hostID] = path
    }
}

/// Columns of a pane's table, in display order.
public enum FileColumn: String, CaseIterable, Sendable {
    case name
    case modified
    case size
    case kind

    public var title: String {
        switch self {
        case .name: .ui("column.name", "이름")
        case .modified: .ui("column.modified", "수정한 날짜")
        case .size: .ui("column.size", "크기")
        case .kind: .ui("column.kind", "종류")
        }
    }

    /// Width before the person resizes the column.
    public var defaultWidth: Double {
        switch self {
        case .name: 260
        case .modified: 156
        case .size: 76
        case .kind: 132
        }
    }

    public var minWidth: Double {
        switch self {
        case .name: 140
        case .modified: 96
        case .size: 56
        case .kind: 72
        }
    }

    /// Pane widths below which the column is hidden so the name column keeps its room.
    public var collapseBelow: Double {
        switch self {
        case .name: 0
        case .modified: 460
        case .size: 0
        case .kind: 660
        }
    }
}
