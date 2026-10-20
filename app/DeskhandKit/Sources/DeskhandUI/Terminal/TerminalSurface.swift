import AppKit
import SwiftTerm
import SwiftUI

/// The SwiftTerm view of one terminal session, drawn in SF Mono with the design system's
/// terminal palette for its current appearance. Keyboard input, size changes, titles, the
/// working directory and pastes go to the session through the callbacks; ⌘C copies the
/// selection, ⌘V hands the clipboard text to `onPaste`, and the context menu adds 스크롤백 지우기.
final class TerminalSurfaceView: SwiftTerm.TerminalView {
    /// Bytes the person typed, for a data frame (`0x20`).
    var onInput: (ArraySlice<UInt8>) -> Void = { _ in }
    /// The terminal's columns and rows after its frame or font changed.
    var onResize: (TerminalSize) -> Void = { _ in }
    /// A title the shell set with OSC 0 or 2.
    var onTitle: (String) -> Void = { _ in }
    /// The working directory the shell reported with OSC 7.
    var onDirectory: (String?) -> Void = { _ in }
    /// Clipboard text the person pasted; the session sends it with `sendPaste` after any confirmation.
    var onPaste: (String) -> Void = { _ in }

    /// Lines kept above the screen.
    static let scrollbackLines = 10_000

    init(size: TerminalSize, fontSize: CGFloat) {
        super.init(frame: .zero, font: DS.Terminal.font(size: fontSize),
                   options: TerminalOptions(cols: size.cols, rows: size.rows, cursorStyle: .steadyBlock,
                                            scrollback: Self.scrollbackLines))
        terminalDelegate = self
        optionAsMetaKey = true
        allowMouseReporting = true
        applyTheme()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    /// Current columns and rows of the emulator.
    var size: TerminalSize {
        let dims = getTerminal().getDims()
        return TerminalSize(cols: dims.cols, rows: dims.rows)
    }

    var fontSize: CGFloat {
        get { font.pointSize }
        set {
            guard newValue != font.pointSize else { return }
            font = DS.Terminal.font(size: newValue)
            onResize(size)
        }
    }

    /// Feeds bytes from the shell to the emulator.
    func feed(_ data: Data) {
        guard !data.isEmpty else { return }
        feed(byteArray: [UInt8](data)[...])
    }

    /// Clears the screen, the scrollback and the modes, before the agent replays the scrollback.
    func resetForReplay() {
        feed(text: "\u{1b}c")
        clearScrollback()
    }

    /// Sends pasted text: line breaks become carriage returns, wrapped in bracketed-paste
    /// markers when the shell turned bracketed paste on.
    func sendPaste(_ text: String) {
        let normalized = PasteRequest.normalized(text)
        guard !normalized.isEmpty else { return }
        if getTerminal().bracketedPasteMode {
            send(txt: "\u{1b}[200~" + normalized + "\u{1b}[201~")
        } else {
            send(txt: normalized)
        }
    }

    /// The whole buffer as text: scrollback and screen, one line per row.
    func bufferText() -> String {
        String(decoding: getTerminal().getBufferAsData(), as: UTF8.self)
    }

    /// The surface of a stand-in session, which its container draws as an image over the
    /// terminal background.
    var isStandIn = false

    /// The surface drawn into a bitmap: the text over transparent default-background cells.
    func renderedImage() -> NSBitmapImageRep? {
        guard bounds.width > 0, bounds.height > 0, let rep = bitmapImageRepForCachingDisplay(in: bounds) else { return nil }
        cacheDisplay(in: bounds, to: rep)
        return rep
    }

    // MARK: Theme

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        applyTheme()
    }

    private func applyTheme() {
        let palette = DS.Terminal.palette(for: effectiveAppearance)
        nativeForegroundColor = palette.foreground
        nativeBackgroundColor = palette.background
        caretColor = palette.caret
        selectedTextBackgroundColor = palette.selection
        selectedTextForegroundColor = palette.selectedText
        installColors(palette.ansi.map(Self.terminalColor))
    }

    private static func terminalColor(_ color: NSColor) -> SwiftTerm.Color {
        let rgb = color.usingColorSpace(.sRGB) ?? color
        func component(_ value: CGFloat) -> UInt16 { UInt16((min(max(value, 0), 1) * 65535).rounded()) }
        return SwiftTerm.Color(red: component(rgb.redComponent), green: component(rgb.greenComponent),
                               blue: component(rgb.blueComponent))
    }

    // MARK: Clipboard and menu

    override func paste(_ sender: Any) {
        guard let text = NSPasteboard.general.string(forType: .string), !text.isEmpty else { return }
        onPaste(text)
    }

    @objc func clearScrollbackItem(_ sender: Any?) {
        clearScrollback()
    }

    override func validateUserInterfaceItem(_ item: NSValidatedUserInterfaceItem) -> Bool {
        switch item.action {
        case #selector(clearScrollbackItem(_:)):
            true
        case #selector(paste(_:)):
            NSPasteboard.general.string(forType: .string)?.isEmpty == false
        case #selector(copy(_:)), #selector(selectAll(_:)):
            super.validateUserInterfaceItem(item)
        default:
            false
        }
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        let menu = NSMenu()
        menu.addItem(withTitle: String.ui("terminal.menu.copy", "복사하기"), action: #selector(copy(_:)), keyEquivalent: "c")
        menu.addItem(withTitle: String.ui("terminal.menu.paste", "붙여넣기"), action: #selector(paste(_:)), keyEquivalent: "v")
        menu.addItem(withTitle: String.ui("terminal.menu.selectAll", "모두 선택"), action: #selector(selectAll(_:)), keyEquivalent: "a")
        menu.addItem(.separator())
        menu.addItem(withTitle: String.ui("terminal.clearScrollback", "스크롤백 지우기"), action: #selector(clearScrollbackItem(_:)),
                     keyEquivalent: "l")
        for item in menu.items { item.target = self }
        return menu
    }
}

extension TerminalSurfaceView: @preconcurrency TerminalViewDelegate {
    func sizeChanged(source: SwiftTerm.TerminalView, newCols: Int, newRows: Int) {
        onResize(TerminalSize(cols: newCols, rows: newRows))
    }

    func setTerminalTitle(source: SwiftTerm.TerminalView, title: String) {
        onTitle(title)
    }

    func hostCurrentDirectoryUpdate(source: SwiftTerm.TerminalView, directory: String?) {
        onDirectory(directory)
    }

    func send(source: SwiftTerm.TerminalView, data: ArraySlice<UInt8>) {
        onInput(data)
    }

    func scrolled(source: SwiftTerm.TerminalView, position: Double) {}

    func rangeChanged(source: SwiftTerm.TerminalView, startY: Int, endY: Int) {}

    /// Opens web links only; file paths name files on the remote computer.
    func requestOpenLink(source: SwiftTerm.TerminalView, link: String, params: [String: String]) {
        guard let url = URL(string: link), let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https" else { return }
        NSWorkspace.shared.open(url)
    }

    func clipboardCopy(source: SwiftTerm.TerminalView, content: Data) {
        guard let text = String(data: content, encoding: .utf8) else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}

/// Clipboard text waiting for the multi-line paste confirmation.
public struct PasteRequest: Identifiable, Equatable, Sendable {
    public let id = UUID()
    /// The terminal session the text goes to.
    public let sessionID: UUID
    public let text: String

    public init(sessionID: UUID, text: String) {
        self.sessionID = sessionID
        self.text = text
    }

    /// The text's lines; a trailing line break does not start another line.
    public var lines: [String] {
        var lines = text.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline).map(String.init)
        if lines.count > 1, lines.last?.isEmpty == true { lines.removeLast() }
        return lines
    }

    public var lineCount: Int { lines.count }

    /// True when the text holds a line break.
    public static func needsConfirmation(_ text: String) -> Bool {
        text.contains(where: \.isNewline)
    }

    /// `text` with each line break (CR LF, LF or CR) as one carriage return, as a terminal sends Return.
    public static func normalized(_ text: String) -> String {
        text.replacingOccurrences(of: "\r\n", with: "\r").replacingOccurrences(of: "\n", with: "\r")
    }
}
