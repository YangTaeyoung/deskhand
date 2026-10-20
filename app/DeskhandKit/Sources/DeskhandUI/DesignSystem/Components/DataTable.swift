import AppKit
import SwiftUI

/// A column of `DataTable`.
struct DataTableColumn {
    let id: String
    let title: String
    var width: CGFloat
    var minWidth: CGFloat
    var maxWidth: CGFloat = 4_000
    var alignment: NSTextAlignment = .left
    /// Direction of the first click on the header; nil for a column that does not sort.
    var firstSortAscending: Bool?
    /// The column takes the width the others leave. Only the last column fills.
    var fills = false
    var headerHelp: String?
    /// The column hides while the table is narrower than this.
    var hideBelow: CGFloat?
}

/// Width of a table's first column: the person's preferred width, narrowed only as far as the
/// table needs to fit its other columns, and never below the column's minimum.
struct FirstColumnWidth: Equatable {
    /// The last width the person dragged to, or the saved or default width.
    private(set) var preferred: CGFloat
    let minimum: CGFloat

    init(preferred: CGFloat, minimum: CGFloat) {
        self.minimum = minimum
        self.preferred = max(minimum, preferred)
    }

    /// The width when `available` points are left for the column.
    func width(available: CGFloat) -> CGFloat {
        max(minimum, min(preferred, available))
    }

    /// Records a width the person dragged the column to.
    mutating func userResized(to width: CGFloat) {
        preferred = max(minimum, width)
    }
}

/// Column and direction a `DataTable` sorts by.
struct DataTableSort: Equatable {
    var column: String
    var ascending: Bool
}

/// Keyboard commands a `DataTable` reports for its selected row.
enum DataTableKey: Equatable {
    /// ⌘⌫.
    case delete
    /// ⌥⌘⌫.
    case deleteTree
    /// Return.
    case open
    /// ⌘C.
    case copy
    /// ←: collapse.
    case left
    /// →: expand.
    case right
}

/// An entry of a `DataTable` context menu.
struct DataTableMenuEntry {
    var title: String
    var symbol: String?
    /// Shown beside the title, such as `⌫` with `modifiers`.
    var key: String = ""
    var modifiers: NSEvent.ModifierFlags = [.command]
    var isEnabled = true
    /// Why the entry is disabled, shown when the pointer rests on it.
    var help: String?
    var isSeparator = false
    var action: @MainActor () -> Void = {}

    static var separator: DataTableMenuEntry { DataTableMenuEntry(title: "", isSeparator: true) }
}

/// A single-selection `NSTableView` with sortable headers, context menus, double-click,
/// keyboard commands and accent-colored selection. Rows are identified by `Row.ID`; a change of
/// rows with the same IDs in the same order redraws only the changed rows, and the selection
/// follows its ID.
struct DataTable<Row: Identifiable & Equatable>: NSViewRepresentable where Row.ID: Hashable {
    let columns: [DataTableColumn]
    let rows: [Row]
    let selection: Row.ID?
    let sort: DataTableSort?
    var rowHeight: CGFloat = 24
    /// Saves column widths in UserDefaults under this name.
    var autosaveName: String?
    let accessibilityLabel: String
    let cell: @MainActor (_ row: Row, _ column: String, _ table: NSTableView) -> NSView
    var onSelect: @MainActor (Row.ID?) -> Void = { _ in }
    var onSort: @MainActor (DataTableSort) -> Void = { _ in }
    var onDoubleClick: @MainActor (Row) -> Void = { _ in }
    var menu: @MainActor (Row) -> [DataTableMenuEntry] = { _ in [] }
    /// Returns whether the key was handled.
    var onKey: @MainActor (DataTableKey, Row) -> Bool = { _, _ in false }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let coordinator = context.coordinator
        let table = DataNSTableView()
        table.keyHandler = { [weak coordinator] key in coordinator?.handle(key) ?? false }
        table.style = .inset
        table.rowHeight = rowHeight
        table.intercellSpacing = NSSize(width: 8, height: 0)
        table.allowsMultipleSelection = false
        table.allowsEmptySelection = true
        table.allowsColumnReordering = false
        table.allowsColumnResizing = true
        table.allowsTypeSelect = true
        table.focusRingType = .none
        table.columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle
        table.headerView = OpaqueTableHeaderView(frame: NSRect(x: 0, y: 0, width: 100, height: 28))
        for column in columns {
            let tableColumn = NSTableColumn(identifier: NSUserInterfaceItemIdentifier(column.id))
            tableColumn.title = column.title
            tableColumn.width = column.width
            tableColumn.minWidth = column.minWidth
            tableColumn.maxWidth = column.maxWidth
            tableColumn.resizingMask = column.fills ? [.autoresizingMask, .userResizingMask] : .userResizingMask
            tableColumn.headerCell.alignment = column.alignment
            tableColumn.headerToolTip = column.headerHelp
            if let ascending = column.firstSortAscending {
                tableColumn.sortDescriptorPrototype = NSSortDescriptor(key: column.id, ascending: ascending)
            }
            table.addTableColumn(tableColumn)
        }
        if let sort { table.sortDescriptors = [NSSortDescriptor(key: sort.column, ascending: sort.ascending)] }
        if let autosaveName {
            table.autosaveName = autosaveName
            table.autosaveTableColumns = true
        }
        if let first = table.tableColumns.first {
            let saved = autosaveName.map { UserDefaults.standard.double(forKey: Coordinator.preferredKey($0)) } ?? 0
            coordinator.firstWidth = FirstColumnWidth(preferred: saved > 0 ? CGFloat(saved) : first.width, minimum: first.minWidth)
        }
        let menu = NSMenu()
        menu.delegate = coordinator
        menu.autoenablesItems = false
        table.menu = menu
        table.target = coordinator
        table.doubleAction = #selector(Coordinator.doubleClick(_:))
        table.dataSource = coordinator
        table.delegate = coordinator
        table.setAccessibilityLabel(accessibilityLabel)

        let scroll = NSScrollView()
        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        scroll.drawsBackground = false
        scroll.postsFrameChangedNotifications = true
        NotificationCenter.default.addObserver(coordinator, selector: #selector(Coordinator.frameChanged(_:)),
                                               name: NSView.frameDidChangeNotification, object: scroll)
        NotificationCenter.default.addObserver(coordinator, selector: #selector(Coordinator.columnResized(_:)),
                                               name: NSTableView.columnDidResizeNotification, object: table)
        coordinator.table = table
        coordinator.scroll = scroll
        coordinator.rows = rows
        table.usesAlternatingRowBackgroundColors = false
        table.reloadData()
        coordinator.applySelection()
        coordinator.fitColumns()
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.update(self)
    }

    static func dismantleNSView(_ scroll: NSScrollView, coordinator: Coordinator) {
        NotificationCenter.default.removeObserver(coordinator)
    }

    @MainActor
    final class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate, NSMenuDelegate {
        var parent: DataTable
        var rows: [Row] = []
        weak var table: DataNSTableView?
        weak var scroll: NSScrollView?
        private var applyingSelection = false
        private var applyingSort = false
        /// The selection last shown, to scroll only when it changes.
        private var shownSelection: Row.ID?
        /// Preferred and minimum width of the first column.
        var firstWidth: FirstColumnWidth?
        /// Column widths are being set by `fitColumns`.
        private var fitting = false

        /// UserDefaults key of the preferred first-column width of the table saved as `autosaveName`.
        static func preferredKey(_ autosaveName: String) -> String { SettingsKey.Table.firstColumnWidth(autosaveName) }

        init(_ parent: DataTable) {
            self.parent = parent
        }

        func update(_ new: DataTable) {
            parent = new
            guard let table else { return }
            if rows != new.rows {
                let old = rows
                rows = new.rows
                applyingSelection = true
                if old.count == rows.count, zip(old, rows).allSatisfy({ $0.id == $1.id }) {
                    let changed = IndexSet(rows.indices.filter { old[$0] != rows[$0] })
                    if !changed.isEmpty {
                        table.reloadData(forRowIndexes: changed, columnIndexes: IndexSet(integersIn: 0..<table.numberOfColumns))
                    }
                } else {
                    table.reloadData()
                }
                applyingSelection = false
                table.enumerateAvailableRowViews { rowView, row in (rowView as? DataRowView)?.striped = row % 2 == 1 }
            }
            applySelection()
            if let sort = new.sort,
               table.sortDescriptors.first.map({ $0.key != sort.column || $0.ascending != sort.ascending }) ?? true {
                applyingSort = true
                table.sortDescriptors = [NSSortDescriptor(key: sort.column, ascending: sort.ascending)]
                applyingSort = false
            }
        }

        /// Selects the row of the selected ID, and scrolls to it when the ID changed from outside.
        func applySelection() {
            guard let table else { return }
            let index = parent.selection.flatMap { id in rows.firstIndex { $0.id == id } }
            let desired = index.map { IndexSet(integer: $0) } ?? IndexSet()
            let moved = parent.selection != shownSelection
            shownSelection = parent.selection
            guard desired != table.selectedRowIndexes else { return }
            applyingSelection = true
            table.selectRowIndexes(desired, byExtendingSelection: false)
            applyingSelection = false
            if moved, let index {
                DispatchQueue.main.async { [weak table] in
                    guard let table, index < table.numberOfRows else { return }
                    table.scrollRowToVisible(index)
                }
            }
        }

        // MARK: Columns

        @objc func frameChanged(_ notification: Notification) {
            fitColumns()
        }

        /// Hides the columns the table is too narrow for, sets the first column to its preferred
        /// width narrowed only as far as the others need, and lets the last column fill the rest.
        func fitColumns() {
            guard let table, let scroll else { return }
            let width = scroll.contentSize.width
            guard width > 0 else { return }
            fitting = true
            defer { fitting = false }
            for column in table.tableColumns {
                guard let spec = parent.columns.first(where: { $0.id == column.identifier.rawValue }), let limit = spec.hideBelow else { continue }
                let hidden = width < limit
                if column.isHidden != hidden { column.isHidden = hidden }
            }
            let visible = table.tableColumns.filter { !$0.isHidden }
            if let first = visible.first, let last = visible.last, first !== last, let firstWidth {
                let spacing = table.intercellSpacing.width * CGFloat(visible.count) + 24
                let others = visible.dropFirst().reduce(0) { $0 + ($1 === last ? $1.minWidth : $1.width) }
                let target = firstWidth.width(available: width - others - spacing)
                if abs(first.width - target) > 0.5 { first.width = target }
            }
            table.sizeLastColumnToFit()
        }

        /// A drag of the first column's edge sets its preferred width, saved with the table's columns.
        @objc func columnResized(_ notification: Notification) {
            guard !fitting, let table, let column = notification.userInfo?["NSTableColumn"] as? NSTableColumn,
                  column === table.tableColumns.first else { return }
            firstWidth?.userResized(to: column.width)
            if let name = parent.autosaveName, let preferred = firstWidth?.preferred {
                UserDefaults.standard.set(Double(preferred), forKey: Self.preferredKey(name))
            }
        }

        // MARK: Data source and delegate

        func numberOfRows(in tableView: NSTableView) -> Int { rows.count }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
            guard let tableColumn, row < rows.count else { return nil }
            return parent.cell(rows[row], tableColumn.identifier.rawValue, tableView)
        }

        func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
            let view = tableView.makeView(withIdentifier: DataRowView.identifier, owner: nil) as? DataRowView ?? DataRowView()
            view.identifier = DataRowView.identifier
            view.striped = row % 2 == 1
            return view
        }

        func tableView(_ tableView: NSTableView, didAdd rowView: NSTableRowView, forRow row: Int) {
            (rowView as? DataRowView)?.striped = row % 2 == 1
        }

        func tableViewSelectionDidChange(_ notification: Notification) {
            guard !applyingSelection, let table else { return }
            let row = table.selectedRow
            let id = row >= 0 && row < rows.count ? rows[row].id : nil
            shownSelection = id
            parent.onSelect(id)
        }

        func tableView(_ tableView: NSTableView, sortDescriptorsDidChange oldDescriptors: [NSSortDescriptor]) {
            guard !applyingSort, let first = tableView.sortDescriptors.first, let key = first.key else { return }
            parent.onSort(DataTableSort(column: key, ascending: first.ascending))
        }

        @objc func doubleClick(_ sender: Any?) {
            guard let table, table.clickedRow >= 0, table.clickedRow < rows.count else { return }
            parent.onDoubleClick(rows[table.clickedRow])
        }

        func handle(_ key: DataTableKey) -> Bool {
            guard let table, table.selectedRow >= 0, table.selectedRow < rows.count else { return false }
            return parent.onKey(key, rows[table.selectedRow])
        }

        // MARK: Menu

        func menuNeedsUpdate(_ menu: NSMenu) {
            menu.removeAllItems()
            guard let table, table.clickedRow >= 0, table.clickedRow < rows.count else { return }
            let row = rows[table.clickedRow]
            if !table.selectedRowIndexes.contains(table.clickedRow) {
                table.selectRowIndexes(IndexSet(integer: table.clickedRow), byExtendingSelection: false)
            }
            for entry in parent.menu(row) {
                menu.addItem(Self.item(entry, target: self))
            }
        }

        static func item(_ entry: DataTableMenuEntry, target: Coordinator) -> NSMenuItem {
            guard !entry.isSeparator else { return .separator() }
            let item = NSMenuItem(title: entry.title, action: #selector(run(_:)), keyEquivalent: keyEquivalent(entry.key))
            item.keyEquivalentModifierMask = entry.key.isEmpty ? [] : entry.modifiers
            item.target = target
            item.isEnabled = entry.isEnabled
            item.toolTip = entry.help
            item.representedObject = DataTableMenuAction(entry.action)
            if let symbol = entry.symbol {
                item.image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)
            }
            return item
        }

        private static func keyEquivalent(_ key: String) -> String {
            key == "⌫" ? String(UnicodeScalar(UInt8(8))) : key
        }

        @objc func run(_ sender: NSMenuItem) {
            (sender.representedObject as? DataTableMenuAction)?.run()
        }
    }
}

/// Runs a context menu entry of a `DataTable`.
private final class DataTableMenuAction: NSObject {
    let run: @MainActor () -> Void

    init(_ run: @escaping @MainActor () -> Void) {
        self.run = run
    }
}

/// The table of `DataTable`: reports its keyboard commands.
final class DataNSTableView: NSTableView {
    var keyHandler: ((DataTableKey) -> Bool)?

    override func keyDown(with event: NSEvent) {
        if let key = Self.key(for: event), keyHandler?(key) == true { return }
        super.keyDown(with: event)
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if window?.firstResponder === self, let key = Self.key(for: event), key == .delete || key == .deleteTree,
           keyHandler?(key) == true {
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    @objc func copy(_ sender: Any?) {
        _ = keyHandler?(.copy)
    }

    static func key(for event: NSEvent) -> DataTableKey? {
        let flags = event.modifierFlags.intersection([.command, .option, .shift, .control])
        switch (event.keyCode, flags) {
        case (51, [.command]): return .delete
        case (51, [.command, .option]): return .deleteTree
        case (36, []), (76, []): return .open
        case (123, []): return .left
        case (124, []): return .right
        default: return nil
        }
    }
}

/// A row of `DataTable`: a stripe behind odd rows only, and the selection in the accent color.
final class DataRowView: NSTableRowView {
    static let identifier = NSUserInterfaceItemIdentifier("data.row")

    var striped = false {
        didSet { if striped != oldValue { needsDisplay = true } }
    }

    override var interiorBackgroundStyle: NSView.BackgroundStyle {
        isSelected ? .emphasized : .normal
    }

    override func drawBackground(in dirtyRect: NSRect) {
        guard striped else { return }
        NSColor.alternatingContentBackgroundColors.dropFirst().first?.setFill()
        NSBezierPath(roundedRect: bounds.insetBy(dx: 4, dy: 0), xRadius: 5, yRadius: 5).fill()
    }

    override func drawSelection(in dirtyRect: NSRect) {
        guard selectionHighlightStyle != .none else { return }
        NSColor(DS.Color.accent).setFill()
        NSBezierPath(roundedRect: bounds.insetBy(dx: 4, dy: 0), xRadius: 5, yRadius: 5).fill()
    }
}

/// A table header that paints the window background under its titles.
final class OpaqueTableHeaderView: NSTableHeaderView {
    override func draw(_ dirtyRect: NSRect) {
        NSColor.windowBackgroundColor.setFill()
        dirtyRect.fill()
        super.draw(dirtyRect)
    }
}

/// A text cell of a `DataTable`: one line with a style, optional monospaced digits, alignment
/// and truncation, recolored for selected rows.
final class DataTextCell: NSTableCellView {
    enum Style {
        case primary
        case secondary
        case tertiary
    }

    let label = NSTextField(labelWithString: "")
    private var style: Style = .primary
    private let monospacedDigits: Bool

    init(identifier: NSUserInterfaceItemIdentifier, alignment: NSTextAlignment = .left, monospacedDigits: Bool = false,
         truncation: NSLineBreakMode = .byTruncatingTail) {
        self.monospacedDigits = monospacedDigits
        super.init(frame: .zero)
        self.identifier = identifier
        label.translatesAutoresizingMaskIntoConstraints = false
        label.alignment = alignment
        label.lineBreakMode = truncation
        label.usesSingleLineMode = true
        label.cell?.truncatesLastVisibleLine = true
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        addSubview(label)
        textField = label
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) { nil }

    /// A reused cell of `identifier` in `table`, or a new one.
    static func make(_ table: NSTableView, _ identifier: String, alignment: NSTextAlignment = .left, monospacedDigits: Bool = false,
                     truncation: NSLineBreakMode = .byTruncatingTail) -> DataTextCell {
        let id = NSUserInterfaceItemIdentifier(identifier)
        return table.makeView(withIdentifier: id, owner: nil) as? DataTextCell
            ?? DataTextCell(identifier: id, alignment: alignment, monospacedDigits: monospacedDigits, truncation: truncation)
    }

    func configure(_ text: String, style: Style, help: String? = nil, weight: NSFont.Weight = .regular) {
        label.stringValue = text
        label.font = monospacedDigits
            ? .monospacedDigitSystemFont(ofSize: NSFont.systemFontSize, weight: weight)
            : .systemFont(ofSize: NSFont.systemFontSize, weight: weight)
        self.style = style
        toolTip = help
        recolor()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet { recolor() }
    }

    private func recolor() {
        if backgroundStyle == .emphasized {
            label.textColor = .alternateSelectedControlTextColor
            return
        }
        label.textColor = switch style {
        case .primary: .labelColor
        case .secondary: .secondaryLabelColor
        case .tertiary: .tertiaryLabelColor
        }
    }
}

/// A small rounded tag drawn in a table cell, such as `Deskhand가 시작함`. Narrower than its text,
/// it draws as a dot at its leading edge. Its colors resolve for the cell's appearance and turn
/// white on a selected row.
final class TagView: NSView {
    static let dotWidth: CGFloat = 10

    var text = "" {
        didSet {
            invalidateIntrinsicContentSize()
            needsDisplay = true
        }
    }
    var tint: NSColor = .controlAccentColor {
        didSet { needsDisplay = true }
    }
    var emphasized = false {
        didSet { needsDisplay = true }
    }

    private var attributes: [NSAttributedString.Key: Any] {
        [.font: NSFont.systemFont(ofSize: NSFont.smallSystemFontSize - 1, weight: .semibold),
         .foregroundColor: emphasized ? NSColor.alternateSelectedControlTextColor : tint]
    }

    private var textWidth: CGFloat { ceil((text as NSString).size(withAttributes: attributes).width) + 12 }

    override var intrinsicContentSize: NSSize {
        NSSize(width: textWidth, height: 16)
    }

    override func draw(_ dirtyRect: NSRect) {
        let ink = emphasized ? NSColor.alternateSelectedControlTextColor : tint
        guard bounds.width >= textWidth - 0.5 else {
            ink.setFill()
            let side: CGFloat = 7
            NSBezierPath(ovalIn: NSRect(x: 1.5, y: (bounds.height - side) / 2, width: side, height: side)).fill()
            return
        }
        let rect = bounds.insetBy(dx: 0.5, dy: 0.5)
        let fill = emphasized ? NSColor.alternateSelectedControlTextColor.withAlphaComponent(0.22) : tint.withAlphaComponent(0.14)
        fill.setFill()
        NSBezierPath(roundedRect: rect, xRadius: rect.height / 2, yRadius: rect.height / 2).fill()
        let size = (text as NSString).size(withAttributes: attributes)
        (text as NSString).draw(at: NSPoint(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2),
                                withAttributes: attributes)
    }
}
