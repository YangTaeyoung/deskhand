import AppKit
import XCTest
@testable import DeskhandUI

@MainActor
final class TerminalSurfaceTests: XCTestCase {
    private func surface() -> (TerminalSurfaceView, () -> [UInt8]) {
        let surface = TerminalSurfaceView(size: .standard, fontSize: 13)
        var sent: [UInt8] = []
        surface.onInput = { sent.append(contentsOf: $0) }
        return (surface, { sent })
    }

    private func key(_ characters: String, ignoringModifiers: String, flags: NSEvent.ModifierFlags, keyCode: UInt16) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0, context: nil,
                         characters: characters, charactersIgnoringModifiers: ignoringModifiers, isARepeat: false,
                         keyCode: keyCode)!
    }

    /// Delivers `event` as AppKit does: key equivalents first, then `keyDown`.
    private func press(_ event: NSEvent, on surface: TerminalSurfaceView) {
        if !surface.performKeyEquivalent(with: event) { surface.keyDown(with: event) }
    }

    // MARK: Paste sanitizing

    func testPasteSanitizerRemovesEscapeSequencesAndControls() {
        let raw = "ls\u{1b}[201~ -la\u{1b}]0;title\u{07}\u{1b}[31mred\u{1b}[0m\u{7f}\u{08}\u{9b}2J\tx\r\ny\n"
        let (text, removed) = PasteRequest.sanitize(raw)
        XCTAssertEqual(text, "ls -lared\tx\r\ny\n")
        XCTAssertEqual(removed, 7)
        XCTAssertEqual(PasteRequest.sanitize("Get-Process").removed, 0)
    }

    func testRequestKeepsTheSanitizedTextAndItsTotals() {
        let request = PasteRequest(sessionID: UUID(), clipboard: "echo 가\u{1b}[1m나\u{1b}[0m\nexit\n")
        XCTAssertEqual(request.text, "echo 가나\nexit\n")
        XCTAssertEqual(request.removedControls, 2)
        XCTAssertEqual(request.lineCount, 2)
        XCTAssertEqual(request.characterCount, 13)
    }

    // MARK: Palette

    private func luminance(_ color: NSColor) -> Double {
        let c = color.usingColorSpace(.sRGB)!
        func channel(_ v: CGFloat) -> Double {
            let v = Double(v)
            return v <= 0.04045 ? v / 12.92 : pow((v + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * channel(c.redComponent) + 0.7152 * channel(c.greenComponent) + 0.0722 * channel(c.blueComponent)
    }

    private func contrast(_ a: NSColor, _ b: NSColor) -> Double {
        let (l1, l2) = (luminance(a), luminance(b))
        return (max(l1, l2) + 0.05) / (min(l1, l2) + 0.05)
    }

    func testEveryANSIColorReadsAgainstTheBackgroundInBothAppearances() throws {
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let palette = DS.Terminal.palette(for: try XCTUnwrap(NSAppearance(named: name)))
            XCTAssertEqual(palette.ansi.count, 16)
            let ratios = palette.ansi.map { contrast($0, palette.background) }
            print("contrast \(name.rawValue): " + ratios.enumerated().map { String(format: "%d=%.2f", $0.offset, $0.element) }.joined(separator: " ")
                  + String(format: " fg=%.2f", contrast(palette.foreground, palette.background)))
            for (index, ratio) in ratios.enumerated() {
                XCTAssertGreaterThanOrEqual(ratio, 4.5, "ANSI \(index) in \(name.rawValue)")
            }
        }
    }

    // MARK: Keyboard

    func testComposingHangulSendsOnlyTheCommittedText() {
        let (surface, sent) = surface()
        let none = NSRange(location: NSNotFound, length: 0)
        surface.setMarkedText("ㄱ", selectedRange: NSRange(location: 1, length: 0), replacementRange: none)
        surface.setMarkedText("그", selectedRange: NSRange(location: 1, length: 0), replacementRange: none)
        XCTAssertTrue(sent().isEmpty)
        XCTAssertTrue(surface.hasMarkedText())
        surface.insertText("그", replacementRange: none)
        XCTAssertEqual(sent(), Array("그".utf8))
        XCTAssertFalse(surface.hasMarkedText())
    }

    func testControlCWhileComposingDiscardsTheMarkedTextThenSendsETX() {
        let (surface, sent) = surface()
        surface.setMarkedText("ㄱ", selectedRange: NSRange(location: 1, length: 0),
                              replacementRange: NSRange(location: NSNotFound, length: 0))
        press(key("\u{3}", ignoringModifiers: "c", flags: .control, keyCode: 8), on: surface)
        XCTAssertFalse(surface.hasMarkedText())
        XCTAssertEqual(sent(), [0x03])
    }

    func testOptionIsMetaOnlyWhenTurnedOn() {
        let (surface, sent) = surface()
        XCTAssertFalse(surface.optionAsMetaKey)
        press(key("∫", ignoringModifiers: "b", flags: .option, keyCode: 11), on: surface)
        XCTAssertNotEqual(sent().first, 0x1B)
        surface.optionAsMetaKey = true
        let before = sent().count
        press(key("∫", ignoringModifiers: "b", flags: .option, keyCode: 11), on: surface)
        XCTAssertEqual(Array(sent().dropFirst(before)), [0x1B, UInt8(ascii: "b")])
    }

    // MARK: Clipboard

    func testRemoteClipboardWriteNeedsPermission() {
        let (surface, _) = surface()
        let pasteboard = NSPasteboard(name: NSPasteboard.Name("TerminalSurfaceTests-\(UUID().uuidString)"))
        defer { pasteboard.releaseGlobally() }
        surface.pasteboard = pasteboard
        pasteboard.clearContents()
        pasteboard.setString("before", forType: .string)
        var allowed = false
        surface.allowsClipboardWrite = { allowed }
        surface.clipboardCopy(source: surface, content: Data("from remote".utf8))
        XCTAssertEqual(pasteboard.string(forType: .string), "before")
        allowed = true
        surface.clipboardCopy(source: surface, content: Data("from remote".utf8))
        XCTAssertEqual(pasteboard.string(forType: .string), "from remote")
    }

    func testPasteIsUnavailableWhileTheShellDoesNotAcceptInput() {
        let (surface, _) = surface()
        let pasteboard = NSPasteboard(name: NSPasteboard.Name("TerminalSurfaceTests-\(UUID().uuidString)"))
        defer { pasteboard.releaseGlobally() }
        surface.pasteboard = pasteboard
        pasteboard.clearContents()
        pasteboard.setString("dir", forType: .string)
        let item = NSMenuItem(title: "붙여넣기", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        var accepts = false
        surface.acceptsPaste = { accepts }
        XCTAssertFalse(surface.validateUserInterfaceItem(item))
        accepts = true
        XCTAssertTrue(surface.validateUserInterfaceItem(item))
    }
}
