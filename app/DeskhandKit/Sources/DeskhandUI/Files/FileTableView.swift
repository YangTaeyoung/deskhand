import AppKit
import SwiftUI

/// Keyboard commands a pane's table handles while it has focus.
public enum FileTableCommand: Sendable, Equatable {
    /// Return: rename in the remote pane, open a folder in the local pane.
    case returnKey
    /// ⌘↓.
    case open
    /// ⌘↑.
    case up
    /// ⌘[.
    case back
    /// ⌘].
    case forward
    /// ⌘⌫.
    case trash
    /// ⌥⌘⌫.
    case deletePermanently
    /// ⌘I.
    case info
    /// ⌥⌘C.
    case copyPath
    /// ⇧⌘N.
    case newFolder
    /// ⌘R.
    case refresh
    /// ⌘F.
    case search

    init?(event: NSEvent) {
        let flags = event.modifierFlags.intersection([.command, .option, .shift, .control])
        let key = event.charactersIgnoringModifiers?.lowercased() ?? ""
        switch (event.keyCode, flags) {
        case (36, []), (76, []): self = .returnKey
        case (125, [.command]): self = .open
        case (126, [.command]): self = .up
        case (51, [.command]): self = .trash
        case (51, [.command, .option]): self = .deletePermanently
        default:
            switch (key, flags) {
            case ("[", [.command]): self = .back
            case ("]", [.command]): self = .forward
            case ("i", [.command]): self = .info
            case ("c", [.command, .option]), ("ç", [.command, .option]): self = .copyPath
            case ("n", [.command, .shift]): self = .newFolder
            case ("r", [.command]): self = .refresh
            case ("f", [.command]): self = .search
            default: return nil
            }
        }
    }
}

/// What a pane's table reports to the pane.
@MainActor
struct FileTableHandlers {
    /// The pane took the keyboard or a click.
    var activate: () -> Void = {}
    /// Double-click on a row.
    var open: (FileItem) -> Void = { _ in }
    var selectionChanged: (Set<String>) -> Void = { _ in }
    var sortChanged: (FileSort) -> Void = { _ in }
    /// Context menu for the clicked rows; empty for the folder background.
    var menu: ([FileItem]) -> [FileMenuEntry] = { _ in [] }
    /// Returns whether the command was handled.
    var command: (FileTableCommand, [FileItem]) -> Bool = { _, _ in false }
    /// The table is drawing rows close to the last one.
    var nearEnd: () -> Void = {}
    /// The table began renaming the requested row.
    var renameStarted: () -> Void = {}
    var commitRename: (FileItem, String) -> Void = { _, _ in }
    var dragWriter: (FileItem) -> (any NSPasteboardWriting)? = { _ in nil }
    var canDrop: (FileDropPayload, FileItem?) -> Bool = { _, _ in false }
    var dropHover: (FileDropHover?) -> Void = { _ in }
    /// Payload, target folder row (nil for the open folder), and whether ⌘ asked for a move.
    var drop: (FileDropPayload, FileItem?, Bool) -> Void = { _, _, _ in }
    var columnWidthChanged: (FileColumn, Double) -> Void = { _, _ in }
    var columnHiddenChanged: (FileColumn, Bool) -> Void = { _, _ in }
}

/// A pane's file list: an `NSTableView` with name, modified, size and kind columns, sorting,
/// multiple selection, type-select, context menus, inline rename, drag and drop (⌘ moves, as in
/// Finder; a plain or ⌥ drag copies), middle-truncated names with the full name as tooltip, and
/// persisted column widths.
/// Narrow panes hide the kind and then the modified column.
struct FileTableView: NSViewRepresentable {
    let side: FileSide
    let rows: [FileItem]
    let selection: Set<String>
    let sort: FileSort
    /// The pane the keyboard acts on: its selection draws in the accent color.
    let isActive: Bool
    let renameRequest: String?
    let widths: [FileColumn: Double]
    let hiddenColumns: Set<FileColumn>
    let accessibilityLabel: String
    let handlers: FileTableHandlers

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }

    func makeNSView(context: Context) -> NSScrollView {
        let coordinator = context.coordinator
        let table = FileNSTableView()
        table.coordinator = coordinator
        table.style = .inset
        table.usesAlternatingRowBackgroundColors = false
        let metrics = DS.TableMetrics(context.environment.dynamicTypeSize)
        table.metrics = metrics
        table.rowHeight = metrics.rowHeight
        table.intercellSpacing = NSSize(width: 8, height: 0)
        table.allowsMultipleSelection = true
        table.allowsEmptySelection = true
        table.allowsColumnReordering = false
        table.allowsColumnResizing = true
        table.allowsTypeSelect = true
        table.columnAutoresizingStyle = .firstColumnOnlyAutoresizingStyle
        table.focusRingType = .none
        for column in FileColumn.allCases {
            let tableColumn = NSTableColumn(identifier: NSUserInterfaceItemIdentifier(column.rawValue))
            tableColumn.title = column.title
            tableColumn.minWidth = metrics.columnWidth(column.minWidth)
            tableColumn.width = Self.width(column, saved: widths[column], metrics: metrics)
            tableColumn.resizingMask = column == .name ? [.autoresizingMask, .userResizingMask] : .userResizingMask
            tableColumn.sortDescriptorPrototype = NSSortDescriptor(key: column.rawValue, ascending: column != .modified && column != .size)
            if column == .size { tableColumn.headerCell.alignment = .right }
            table.addTableColumn(tableColumn)
        }
        table.sortDescriptors = [NSSortDescriptor(key: sort.key.rawValue, ascending: sort.ascending)]
        let headerMenu = NSMenu()
        headerMenu.delegate = coordinator
        headerMenu.autoenablesItems = false
        coordinator.headerMenu = headerMenu
        table.headerView?.menu = headerMenu
        let menu = NSMenu()
        menu.delegate = coordinator
        menu.autoenablesItems = false
        table.menu = menu
        table.registerForDraggedTypes([.fileURL, .deskhandRemoteItem])
        table.setDraggingSourceOperationMask([.copy, .move, .generic], forLocal: true)
        table.setDraggingSourceOperationMask(side == .local ? .copy : [], forLocal: false)
        table.draggingDestinationFeedbackStyle = .regular
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
        coordinator.table = table
        coordinator.scroll = scroll
        NotificationCenter.default.addObserver(coordinator, selector: #selector(Coordinator.frameChanged(_:)),
                                               name: NSView.frameDidChangeNotification, object: scroll)
        NotificationCenter.default.addObserver(coordinator, selector: #selector(Coordinator.columnResized(_:)),
                                               name: NSTableView.columnDidResizeNotification, object: table)
        coordinator.rows = rows
        table.reloadData()
        coordinator.update(self, force: true)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        let metrics = DS.TableMetrics(context.environment.dynamicTypeSize)
        if let table = scroll.documentView as? FileNSTableView, table.metrics != metrics {
            table.apply(metrics)
            context.coordinator.rescaleColumns(metrics)
        }
        context.coordinator.update(self)
    }

    /// A column's width at the text size of `metrics`: its saved width, else its default, both
    /// kept at the default text size.
    static func width(_ column: FileColumn, saved: Double?, metrics: DS.TableMetrics) -> CGFloat {
        metrics.columnWidth(CGFloat(saved ?? column.defaultWidth))
    }

    /// A width drawn at the text size of `metrics`, as it is saved: at the default text size.
    static func savedWidth(_ width: CGFloat, metrics: DS.TableMetrics) -> Double {
        Double(metrics.baseWidth(width).rounded())
    }

    static func dismantleNSView(_ scroll: NSScrollView, coordinator: Coordinator) {
        NotificationCenter.default.removeObserver(coordinator)
    }

    // MARK: Coordinator

    @MainActor
    final class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate, NSMenuDelegate, NSTextFieldDelegate {
        var parent: FileTableView
        var rows: [FileItem] = []
        weak var table: FileNSTableView?
        weak var scroll: NSScrollView?
        weak var headerMenu: NSMenu?
        private var applyingSelection = false
        private var applyingSort = false
        private var applyingColumns = false
        private var lastMove = false
        private var lastHover: FileDropHover?
        private var nearEndPending = false
        private var editing: FileItem?
        private var autoHidden: Set<FileColumn> = []

        init(_ parent: FileTableView) {
            self.parent = parent
        }

        private var handlers: FileTableHandlers { parent.handlers }

        func update(_ new: FileTableView, force: Bool = false) {
            let old = parent
            parent = new
            guard let table else { return }
            if force || rows != new.rows {
                rows = new.rows
                if editing == nil { table.reloadData() }
            }
            if force || old.isActive != new.isActive {
                table.enumerateAvailableRowViews { rowView, _ in
                    (rowView as? FileRowView)?.paneActive = new.isActive
                }
            }
            let desired = IndexSet(rows.indices.filter { new.selection.contains(rows[$0].path) })
            if desired != table.selectedRowIndexes {
                applyingSelection = true
                table.selectRowIndexes(desired, byExtendingSelection: false)
                applyingSelection = false
            }
            let descriptor = NSSortDescriptor(key: new.sort.key.rawValue, ascending: new.sort.ascending)
            if table.sortDescriptors.first.map({ $0.key != descriptor.key || $0.ascending != descriptor.ascending }) ?? true {
                applyingSort = true
                table.sortDescriptors = [descriptor]
                applyingSort = false
            }
            if force || old.hiddenColumns != new.hiddenColumns { applyColumnVisibility() }
            if let path = new.renameRequest, editing == nil, let row = rows.firstIndex(where: { $0.path == path }) {
                DispatchQueue.main.async { [weak self] in
                    self?.handlers.renameStarted()
                    self?.beginEditing(row: row)
                }
            }
        }

        // MARK: Columns

        @objc func frameChanged(_ notification: Notification) {
            applyColumnVisibility()
        }

        /// Sets every column's minimum and width for text drawn at `metrics`.
        func rescaleColumns(_ metrics: DS.TableMetrics) {
            guard let table else { return }
            applyingColumns = true
            for column in table.tableColumns {
                guard let kind = FileColumn(rawValue: column.identifier.rawValue) else { continue }
                column.minWidth = metrics.columnWidth(kind.minWidth)
                column.width = FileTableView.width(kind, saved: parent.widths[kind], metrics: metrics)
            }
            applyingColumns = false
            applyColumnVisibility()
        }

        /// Hides the columns the person turned off and those the pane is too narrow for.
        func applyColumnVisibility() {
            guard let table, let scroll else { return }
            let width = scroll.contentSize.width
            let auto = Set(FileColumn.allCases.filter { width > 0 && width < table.metrics.columnWidth($0.collapseBelow) })
            let hidden = parent.hiddenColumns.union(auto)
            let changed = auto != autoHidden || table.tableColumns.contains { column in
                FileColumn(rawValue: column.identifier.rawValue).map { hidden.contains($0) != column.isHidden } ?? false
            }
            autoHidden = auto
            applyingColumns = true
            if changed {
                for column in table.tableColumns {
                    guard let kind = FileColumn(rawValue: column.identifier.rawValue), kind != .name else { continue }
                    column.isHidden = hidden.contains(kind)
                }
            }
            if width > 0 { table.sizeToFit() }
            applyingColumns = false
        }

        @objc func columnResized(_ notification: Notification) {
            guard !applyingColumns, let column = notification.userInfo?["NSTableColumn"] as? NSTableColumn,
                  let kind = FileColumn(rawValue: column.identifier.rawValue), kind != .name else { return }
            handlers.columnWidthChanged(kind, FileTableView.savedWidth(column.width, metrics: table?.metrics ?? DS.TableMetrics(.large)))
        }

        @objc func toggleColumn(_ sender: NSMenuItem) {
            guard let raw = sender.representedObject as? String, let column = FileColumn(rawValue: raw) else { return }
            handlers.columnHiddenChanged(column, !parent.hiddenColumns.contains(column))
        }

        // MARK: Data source

        func numberOfRows(in tableView: NSTableView) -> Int {
            rows.count
        }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
            guard let tableColumn, let column = FileColumn(rawValue: tableColumn.identifier.rawValue), row < rows.count else { return nil }
            let item = rows[row]
            if row >= rows.count - 40, !nearEndPending {
                nearEndPending = true
                DispatchQueue.main.async { [weak self] in
                    self?.nearEndPending = false
                    self?.handlers.nearEnd()
                }
            }
            switch column {
            case .name:
                let cell = tableView.makeView(withIdentifier: FileNameCell.identifier, owner: nil) as? FileNameCell ?? FileNameCell()
                cell.configure(item, metrics: tableView.textMetrics)
                cell.actionsProvider = { [weak self] in self?.accessibilityActions(for: item) ?? [] }
                return cell
            case .modified, .size, .kind:
                let identifier = NSUserInterfaceItemIdentifier("file.\(column.rawValue)")
                let cell = tableView.makeView(withIdentifier: identifier, owner: nil) as? FileTextCell
                    ?? FileTextCell(identifier: identifier, alignment: column == .size ? .right : .left)
                let text = switch column {
                case .modified: item.modifiedText
                case .size: item.sizeText
                default: item.kind
                }
                cell.configure(text, hidden: item.isHidden, metrics: tableView.textMetrics)
                return cell
            }
        }

        func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
            let view = tableView.makeView(withIdentifier: FileRowView.identifier, owner: nil) as? FileRowView ?? FileRowView()
            view.identifier = FileRowView.identifier
            view.paneActive = parent.isActive
            view.striped = row % 2 == 1
            return view
        }

        func tableView(_ tableView: NSTableView, didAdd rowView: NSTableRowView, forRow row: Int) {
            (rowView as? FileRowView)?.striped = row % 2 == 1
        }

        func tableView(_ tableView: NSTableView, typeSelectStringFor tableColumn: NSTableColumn?, row: Int) -> String? {
            row < rows.count ? rows[row].name : nil
        }

        func tableViewSelectionDidChange(_ notification: Notification) {
            guard !applyingSelection, let table else { return }
            let paths = Set(table.selectedRowIndexes.compactMap { $0 < rows.count ? rows[$0].path : nil })
            handlers.selectionChanged(paths)
        }

        func tableView(_ tableView: NSTableView, sortDescriptorsDidChange oldDescriptors: [NSSortDescriptor]) {
            guard !applyingSort, let first = tableView.sortDescriptors.first, let key = first.key.flatMap(FileSortKey.init(rawValue:)) else { return }
            handlers.sortChanged(FileSort(key: key, ascending: first.ascending))
        }

        @objc func doubleClick(_ sender: Any?) {
            guard let table, table.clickedRow >= 0, table.clickedRow < rows.count else { return }
            handlers.open(rows[table.clickedRow])
        }

        /// Items the keyboard acts on: the selected rows in row order.
        var selectedItems: [FileItem] {
            guard let table else { return [] }
            return table.selectedRowIndexes.compactMap { $0 < rows.count ? rows[$0] : nil }
        }

        func handle(_ command: FileTableCommand) -> Bool {
            if command == .returnKey, parent.side == .remote, selectedItems.count == 1, let row = table?.selectedRow, row >= 0 {
                beginEditing(row: row)
                return true
            }
            return handlers.command(command, selectedItems)
        }

        // MARK: Menus

        func menuNeedsUpdate(_ menu: NSMenu) {
            menu.removeAllItems()
            if menu === headerMenu {
                for column in FileColumn.allCases where column != .name {
                    let item = NSMenuItem(title: column.title, action: #selector(toggleColumn(_:)), keyEquivalent: "")
                    item.target = self
                    item.representedObject = column.rawValue
                    item.state = parent.hiddenColumns.contains(column) ? .off : .on
                    menu.addItem(item)
                }
                return
            }
            guard let table else { return }
            var targets: [FileItem] = []
            let clicked = table.clickedRow
            if clicked >= 0, clicked < rows.count {
                if table.selectedRowIndexes.contains(clicked) {
                    targets = selectedItems
                } else {
                    table.selectRowIndexes(IndexSet(integer: clicked), byExtendingSelection: false)
                    targets = [rows[clicked]]
                }
            }
            handlers.activate()
            for entry in handlers.menu(targets) {
                menu.addItem(Self.menuItem(entry, target: self))
            }
        }

        static func menuItem(_ entry: FileMenuEntry, target: Coordinator) -> NSMenuItem {
            guard !entry.isSeparator else { return .separator() }
            let item = NSMenuItem(title: entry.title, action: #selector(runMenuAction(_:)), keyEquivalent: keyEquivalent(entry.key))
            item.keyEquivalentModifierMask = entry.key.isEmpty ? [] : entry.modifiers
            item.target = target
            item.isEnabled = entry.isEnabled
            item.representedObject = MenuAction(entry.action)
            if let symbol = entry.symbol {
                item.image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)
            }
            return item
        }

        private static func keyEquivalent(_ key: String) -> String {
            switch key {
            case "⌫": String(UnicodeScalar(UInt8(8)))
            case "↩": "\r"
            case "↓": String(Character(UnicodeScalar(UInt32(NSDownArrowFunctionKey))!))
            default: key
            }
        }

        @objc func runMenuAction(_ sender: NSMenuItem) {
            (sender.representedObject as? MenuAction)?.run()
        }

        /// The context menu of `item` as VoiceOver actions.
        private func accessibilityActions(for item: FileItem) -> [NSAccessibilityCustomAction] {
            handlers.menu([item]).filter { !$0.isSeparator && $0.isEnabled }.map { entry in
                let action = entry.action
                return NSAccessibilityCustomAction(name: entry.title.replacingOccurrences(of: "…", with: "")) {
                    MainActor.assumeIsolated { action() }
                    return true
                }
            }
        }

        // MARK: Rename

        func beginEditing(row: Int) {
            guard parent.side == .remote, let table, row < rows.count else { return }
            table.scrollRowToVisible(row)
            guard let cell = table.view(atColumn: 0, row: row, makeIfNecessary: true) as? FileNameCell else { return }
            let item = rows[row]
            editing = item
            cell.beginEditing(delegate: self)
            table.window?.makeFirstResponder(cell.label)
            if let editor = cell.label.currentEditor() {
                let name = item.name as NSString
                let ext = FileKind.pathExtension(of: item.name)
                let length = !item.isDirectory && !ext.isEmpty ? name.length - (ext as NSString).length - 1 : name.length
                editor.selectedRange = NSRange(location: 0, length: max(0, length))
            }
        }

        func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
            guard commandSelector == #selector(NSResponder.cancelOperation(_:)), let field = control as? NSTextField else { return false }
            finishEditing(field, commit: false)
            return true
        }

        func controlTextDidEndEditing(_ notification: Notification) {
            guard let field = notification.object as? NSTextField, editing != nil else { return }
            finishEditing(field, commit: true)
        }

        private func finishEditing(_ field: NSTextField, commit: Bool) {
            guard let item = editing else { return }
            editing = nil
            let name = field.stringValue
            field.abortEditing()
            (field.superview as? FileNameCell)?.endEditing(name: commit ? name : item.name)
            table?.window?.makeFirstResponder(table)
            if commit, name != item.name { handlers.commitRename(item, name) }
            table?.reloadData()
        }

        // MARK: Drag and drop

        func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> (any NSPasteboardWriting)? {
            row < rows.count ? handlers.dragWriter(rows[row]) : nil
        }

        func tableView(_ tableView: NSTableView, validateDrop info: any NSDraggingInfo, proposedRow row: Int,
                       proposedDropOperation dropOperation: NSTableView.DropOperation) -> NSDragOperation {
            guard (info.draggingSource as? NSTableView) !== tableView,
                  let payload = FileDropPayload(pasteboard: info.draggingPasteboard) else {
                hover(nil)
                return []
            }
            var folder: FileItem?
            if dropOperation == .on, row >= 0, row < rows.count, rows[row].isFolder {
                folder = rows[row]
            } else {
                tableView.setDropRow(-1, dropOperation: .on)
            }
            guard handlers.canDrop(payload, folder) else {
                hover(nil)
                return []
            }
            let mask = info.draggingSourceOperationMask
            let move = NSEvent.modifierFlags.contains(.command) && !mask.intersection([.move, .generic]).isEmpty
            lastMove = move
            hover(FileDropHover(folder: folder, move: move, count: payload.count))
            if move { return mask.contains(.move) ? .move : .generic }
            if mask.contains(.copy) { return .copy }
            return []
        }

        func tableView(_ tableView: NSTableView, acceptDrop info: any NSDraggingInfo, row: Int,
                       dropOperation: NSTableView.DropOperation) -> Bool {
            guard let payload = FileDropPayload(pasteboard: info.draggingPasteboard) else { return false }
            let folder = dropOperation == .on && row >= 0 && row < rows.count && rows[row].isFolder ? rows[row] : nil
            let move = lastMove
            hover(nil)
            handlers.drop(payload, folder, move)
            return true
        }

        func dropEnded() {
            hover(nil)
        }

        private func hover(_ value: FileDropHover?) {
            guard value != lastHover else { return }
            lastHover = value
            handlers.dropHover(value)
        }
    }
}

/// Runs a context menu entry.
private final class MenuAction: NSObject {
    let run: @MainActor () -> Void

    init(_ run: @escaping @MainActor () -> Void) {
        self.run = run
    }
}
