import AppKit
import SwiftTerm
import SwiftUI

/// The SwiftTerm view of one terminal session, drawn in D2Coding with the design system's
/// terminal palette for its current appearance. Keyboard input, size changes, titles, the
/// working directory and pastes go to the session through the callbacks; ⌘C copies the
/// selection, ⌘V hands the clipboard text to `onPaste` while the shell accepts input, and the
/// context menu adds 스크롤백 지우기. ⌃C while the input method composes discards the
/// composition before sending ETX. Remote clipboard writes (OSC 52) apply only when
/// `allowsClipboardWrite` says so.
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
    /// Whether pasting is offered: true while the shell accepts input.
    var acceptsPaste: () -> Bool = { true }
    /// Whether the remote program may put text on this Mac's clipboard with OSC 52.
    var allowsClipboardWrite: () -> Bool = { false }
    /// The clipboard paste reads and OSC 52 writes.
    var pasteboard = NSPasteboard.general

    /// Lines kept above the screen.
    static let scrollbackLines = 10_000

    init(size: TerminalSize, fontSize: CGFloat) {
        super.init(frame: .zero, font: Self.font(size: fontSize),
                   options: TerminalOptions(cols: size.cols, rows: size.rows, cursorStyle: .steadyBlock,
                                            scrollback: Self.scrollbackLines))
        minimumTextContrast = 4.5
        terminalDelegate = self
        optionAsMetaKey = false
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
            font = Self.font(size: newValue)
            onResize(size)
        }
    }

    /// One full-width Korean coding face keeps Latin and Hangul on the same cell grid.
    static func font(size: CGFloat) -> NSFont {
        DS.Terminal.font(size: size)
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

    /// Sends pasted text without escape sequences and control characters: line breaks become
    /// carriage returns, wrapped in bracketed-paste markers when the shell turned bracketed
    /// paste on.
    func sendPaste(_ text: String) {
        let normalized = PasteRequest.normalized(PasteRequest.sanitize(text).text)
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

    // MARK: Keyboard

    /// ⌃C while the input method composes: discards the composition, then sends ETX.
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection([.command, .control, .option, .shift])
        if flags == .control, event.charactersIgnoringModifiers?.lowercased() == "c", hasMarkedText() {
            inputContext?.discardMarkedText()
            unmarkText()
            send([0x03])
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    // MARK: Clipboard and menu

    override func paste(_ sender: Any) {
        guard acceptsPaste(), let text = pasteboard.string(forType: .string), !text.isEmpty else { return }
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
            acceptsPaste() && pasteboard.string(forType: .string)?.isEmpty == false
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

    /// Opens http and https links.
    func requestOpenLink(source: SwiftTerm.TerminalView, link: String, params: [String: String]) {
        guard let url = URL(string: link), let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https" else { return }
        NSWorkspace.shared.open(url)
    }

    /// Puts OSC 52 text on the clipboard when `allowsClipboardWrite` allows it; ignores it otherwise.
    func clipboardCopy(source: SwiftTerm.TerminalView, content: Data) {
        guard allowsClipboardWrite(), let text = String(data: content, encoding: .utf8) else { return }
        pasteboard.clearContents()
        pasteboard.setString(text, forType: .string)
    }
}

/// Clipboard text waiting for the paste confirmation, without escape sequences and control
/// characters.
public struct PasteRequest: Identifiable, Equatable, Sendable {
    public let id = UUID()
    /// The terminal session the text goes to.
    public let sessionID: UUID
    /// The text to send.
    public let text: String
    /// Escape sequences and lone control characters removed from the clipboard text.
    public let removedControls: Int

    public init(sessionID: UUID, clipboard: String) {
        self.sessionID = sessionID
        (text, removedControls) = Self.sanitize(clipboard)
    }

    /// The text's lines; a trailing line break does not start another line.
    public var lines: [String] {
        var lines = text.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline).map(String.init)
        if lines.count > 1, lines.last?.isEmpty == true { lines.removeLast() }
        return lines
    }

    public var lineCount: Int { lines.count }

    /// Characters of the text, line breaks included.
    public var characterCount: Int { text.count }

    /// True when the text holds a line break.
    public static func needsConfirmation(_ text: String) -> Bool {
        text.contains(where: \.isNewline)
    }

    /// `text` with each line break (CR LF, LF or CR) as one carriage return, as a terminal sends Return.
    public static func normalized(_ text: String) -> String {
        text.replacingOccurrences(of: "\r\n", with: "\r").replacingOccurrences(of: "\n", with: "\r")
    }

    /// `text` without escape sequences (CSI, OSC, DCS, SOS, PM, APC, escapes with intermediate
    /// bytes such as charset designations, and two-character escapes, in their 7-bit and 8-bit
    /// forms), C0 controls other than TAB, CR and LF, DEL and C1 controls, with the number of
    /// sequences and lone control characters removed.
    public static func sanitize(_ text: String) -> (text: String, removed: Int) {
        var output = String.UnicodeScalarView()
        var removed = 0
        var scalars = text.unicodeScalars[...]
        /// Drops scalars up to and including a string terminator: BEL, ESC \ or 0x9C.
        func skipString() {
            while let scalar = scalars.popFirst() {
                switch scalar.value {
                case 0x07, 0x9C: return
                case 0x1B where scalars.first == "\\":
                    scalars.removeFirst()
                    return
                default: continue
                }
            }
        }
        /// Drops CSI parameters and intermediates up to and including the final byte.
        func skipCSI() {
            while let scalar = scalars.popFirst() {
                if (0x40...0x7E).contains(scalar.value) { return }
            }
        }
        while let scalar = scalars.popFirst() {
            switch scalar.value {
            case 0x09, 0x0A, 0x0D:
                output.append(scalar)
            case 0x1B:
                removed += 1
                guard let next = scalars.popFirst() else { break }
                switch next.value {
                case 0x5B: skipCSI()
                case 0x5D, 0x50, 0x58, 0x5E, 0x5F: skipString()
                case 0x20...0x2F:
                    while let scalar = scalars.popFirst(), (0x20...0x2F).contains(scalar.value) {}
                default: break
                }
            case 0x9B:
                removed += 1
                skipCSI()
            case 0x90, 0x98, 0x9D, 0x9E, 0x9F:
                removed += 1
                skipString()
            case 0x00..<0x20, 0x7F, 0x80..<0xA0:
                removed += 1
            default:
                output.append(scalar)
            }
        }
        return (String(output), removed)
    }
}
