import AppKit
import SwiftUI

/// The table: keyboard commands, pane activation and the end of drags.
final class FileNSTableView: NSTableView, TextScaledTable {
    weak var coordinator: FileTableView.Coordinator?
    var metrics = DS.TableMetrics(.large)

    /// Takes `metrics` for its rows and cells; redraws the rows when they change.
    func apply(_ metrics: DS.TableMetrics) {
        guard metrics != self.metrics else { return }
        self.metrics = metrics
        rowHeight = metrics.rowHeight
        reloadData()
    }

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

    func configure(_ item: FileItem, metrics: DS.TableMetrics) {
        self.item = item
        label.font = .systemFont(ofSize: metrics.textSize)
        previewTag.font = .systemFont(ofSize: metrics.smallTextSize, weight: .semibold)
        icon.symbolConfiguration = NSImage.SymbolConfiguration(pointSize: metrics.textSize, weight: .regular)
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

    func configure(_ text: String, hidden: Bool, metrics: DS.TableMetrics) {
        label.font = .monospacedDigitSystemFont(ofSize: metrics.smallTextSize + 1, weight: .regular)
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
