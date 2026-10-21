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
        table.usesAlternatingRowBackgroundColors = true
        table.rowHeight = 24
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
            tableColumn.minWidth = column.minWidth
            tableColumn.width = widths[column] ?? column.defaultWidth
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
        context.coordinator.update(self)
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
                table.usesAlternatingRowBackgroundColors = !rows.isEmpty
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

        /// Hides the columns the person turned off and those the pane is too narrow for.
        func applyColumnVisibility() {
            guard let table, let scroll else { return }
            let width = scroll.contentSize.width
            let auto = Set(FileColumn.allCases.filter { width > 0 && width < $0.collapseBelow })
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
            handlers.columnWidthChanged(kind, Double(column.width))
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
                cell.configure(item)
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
                cell.configure(text, hidden: item.isHidden)
                return cell
            }
        }

        func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
            let view = tableView.makeView(withIdentifier: FileRowView.identifier, owner: nil) as? FileRowView ?? FileRowView()
            view.identifier = FileRowView.identifier
            view.paneActive = parent.isActive
            return view
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

/// The table: keyboard commands, pane activation and the end of drags.
final class FileNSTableView: NSTableView {
    weak var coordinator: FileTableView.Coordinator?

    override func keyDown(with event: NSEvent) {
        if let command = FileTableCommand(event: event), coordinator?.handle(command) == true { return }
        super.keyDown(with: event)
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if window?.firstResponder === self, let command = FileTableCommand(event: event), command != .returnKey,
           coordinator?.handle(command) == true {
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    override func becomeFirstResponder() -> Bool {
        let became = super.becomeFirstResponder()
        if became {
            DispatchQueue.main.async { [weak self] in self?.coordinator?.parent.handlers.activate() }
        }
        return became
    }

    override func mouseDown(with event: NSEvent) {
        coordinator?.parent.handlers.activate()
        super.mouseDown(with: event)
    }

    override func draggingExited(_ sender: (any NSDraggingInfo)?) {
        super.draggingExited(sender)
        coordinator?.dropEnded()
    }

    override func draggingEnded(_ sender: any NSDraggingInfo) {
        super.draggingEnded(sender)
        coordinator?.dropEnded()
    }
}

/// A row whose selection uses the accent color while its pane is active and a gray otherwise.
final class FileRowView: NSTableRowView {
    static let identifier = NSUserInterfaceItemIdentifier("file.row")

    var paneActive = true {
        didSet {
            guard paneActive != oldValue else { return }
            needsDisplay = true
            for case let cell as NSTableCellView in subviews { cell.backgroundStyle = interiorBackgroundStyle }
        }
    }

    override var interiorBackgroundStyle: NSView.BackgroundStyle {
        isSelected && paneActive ? .emphasized : .normal
    }

    override func drawSelection(in dirtyRect: NSRect) {
        guard selectionHighlightStyle != .none else { return }
        let color = paneActive ? NSColor(DS.Color.accent) : NSColor.unemphasizedSelectedContentBackgroundColor
        color.setFill()
        NSBezierPath(roundedRect: bounds.insetBy(dx: 4, dy: 0), xRadius: 5, yRadius: 5).fill()
    }
}

/// Name column cell: kind symbol and the name truncated in the middle, with the full name as
/// tooltip. Becomes an editable field while renaming.
final class FileNameCell: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("file.name")

    let icon = NSImageView()
    let label = NSTextField(labelWithString: "")
    /// `미리보기` after the name of an item the preview daemon received.
    let previewTag = NSTextField(labelWithString: String.ui("files.preview.tag", "미리보기"))
    private var labelTrailing: NSLayoutConstraint?
    private var tagTrailing: NSLayoutConstraint?
    /// The row's context menu as VoiceOver actions, read when VoiceOver asks.
    var actionsProvider: (() -> [NSAccessibilityCustomAction])?
    private var item: FileItem?

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.symbolConfiguration = NSImage.SymbolConfiguration(pointSize: 13, weight: .regular)
        icon.imageScaling = .scaleProportionallyDown
        label.translatesAutoresizingMaskIntoConstraints = false
        label.lineBreakMode = .byTruncatingMiddle
        label.usesSingleLineMode = true
        label.cell?.truncatesLastVisibleLine = true
        label.font = .systemFont(ofSize: NSFont.systemFontSize)
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        previewTag.translatesAutoresizingMaskIntoConstraints = false
        previewTag.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        previewTag.textColor = DS.Status.warningTextNS
        previewTag.setContentCompressionResistancePriority(.required, for: .horizontal)
        previewTag.setContentHuggingPriority(.required, for: .horizontal)
        previewTag.isHidden = true
        addSubview(icon)
        addSubview(label)
        addSubview(previewTag)
        imageView = icon
        textField = label
        label.setAccessibilityCustomActions([])
        let labelTrailing = label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2)
        let tagTrailing = previewTag.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -2)
        self.labelTrailing = labelTrailing
        self.tagTrailing = tagTrailing
        NSLayoutConstraint.activate([
            icon.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            icon.centerYAnchor.constraint(equalTo: centerYAnchor),
            icon.widthAnchor.constraint(equalToConstant: 18),
            icon.heightAnchor.constraint(equalToConstant: 16),
            label.leadingAnchor.constraint(equalTo: icon.trailingAnchor, constant: 6),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
            labelTrailing,
            previewTag.leadingAnchor.constraint(equalTo: label.trailingAnchor, constant: 6),
            previewTag.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) { nil }

    override func accessibilityCustomActions() -> [NSAccessibilityCustomAction]? {
        actionsProvider?()
    }

    func configure(_ item: FileItem) {
        self.item = item
        label.stringValue = item.name
        label.isEditable = false
        label.isSelectable = false
        label.drawsBackground = false
        label.isBezeled = false
        icon.image = NSImage(systemSymbolName: item.fileKind.symbol, accessibilityDescription: item.kind)
        previewTag.isHidden = !item.isPreviewOnly
        labelTrailing?.isActive = !item.isPreviewOnly
        tagTrailing?.isActive = item.isPreviewOnly
        toolTip = item.isPreviewOnly
            ? item.name + "\n" + String.ui("files.preview.note", "미리보기에서는 이 Mac에 쓰지 않습니다. 받은 항목은 앱을 닫으면 사라집니다.")
            : item.name
        alphaValue = item.isHidden ? 0.55 : 1
        recolor()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet { recolor() }
    }

    private func recolor() {
        let emphasized = backgroundStyle == .emphasized
        label.textColor = emphasized ? .alternateSelectedControlTextColor : .labelColor
        previewTag.textColor = emphasized ? .alternateSelectedControlTextColor : DS.Status.warningTextNS
        if emphasized {
            icon.contentTintColor = .alternateSelectedControlTextColor
        } else {
            icon.contentTintColor = item?.isFolder == true ? NSColor(DS.Color.accent) : .secondaryLabelColor
        }
    }

    func beginEditing(delegate: any NSTextFieldDelegate) {
        label.isEditable = true
        label.isSelectable = true
        label.drawsBackground = true
        label.backgroundColor = .textBackgroundColor
        label.textColor = .textColor
        label.isBezeled = false
        label.focusRingType = .exterior
        label.lineBreakMode = .byClipping
        label.delegate = delegate
    }

    func endEditing(name: String) {
        label.delegate = nil
        label.isEditable = false
        label.isSelectable = false
        label.drawsBackground = false
        label.lineBreakMode = .byTruncatingMiddle
        label.stringValue = name
        recolor()
    }
}

/// Modified, size and kind cells: secondary text with tabular digits.
final class FileTextCell: NSTableCellView {
    let label = NSTextField(labelWithString: "")

    init(identifier: NSUserInterfaceItemIdentifier, alignment: NSTextAlignment) {
        super.init(frame: .zero)
        self.identifier = identifier
        label.translatesAutoresizingMaskIntoConstraints = false
        label.alignment = alignment
        label.lineBreakMode = .byTruncatingTail
        label.usesSingleLineMode = true
        label.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize + 1, weight: .regular)
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

    func configure(_ text: String, hidden: Bool) {
        label.stringValue = text
        alphaValue = hidden ? 0.55 : 1
        recolor()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet { recolor() }
    }

    private func recolor() {
        label.textColor = backgroundStyle == .emphasized ? .alternateSelectedControlTextColor : .secondaryLabelColor
    }
}
