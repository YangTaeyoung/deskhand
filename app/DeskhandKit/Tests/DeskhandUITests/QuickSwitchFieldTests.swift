import AppKit
import SwiftUI
import XCTest
@testable import DeskhandUI

@MainActor
final class QuickSwitchFieldTests: XCTestCase {
    func testArrowsReturnAndEscapeBecomeKeys() {
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.moveUp(_:)), composing: false), .key(.up))
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.moveDown(_:)), composing: false), .key(.down))
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.insertNewline(_:)), composing: false), .key(.enter))
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.cancelOperation(_:)), composing: false), .key(.escape))
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.deleteBackward(_:)), composing: false), .pass)
    }

    func testReturnAndEscapeBelongToTheInputMethodWhileComposing() {
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.insertNewline(_:)), composing: true), .pass)
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.cancelOperation(_:)), composing: true), .pass)
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.moveDown(_:)), composing: true), .key(.down))
    }

    func testTabStaysInTheField() {
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.insertTab(_:)), composing: false), .swallow)
        XCTAssertEqual(QuickSwitchField.command(for: #selector(NSResponder.insertBacktab(_:)), composing: true), .swallow)
    }

    func testCoordinatorPassesReturnToAComposingInputMethod() {
        var keys: [QuickSwitchKey] = []
        let field = QuickSwitchField(text: .constant(""), placeholder: "", clearRequests: 0) { keys.append($0) }
        let coordinator = field.makeCoordinator()
        let editor = NSTextView()
        let control = NSTextField()

        editor.setMarkedText("ㅌ", selectedRange: NSRange(location: 1, length: 0), replacementRange: NSRange(location: NSNotFound, length: 0))
        XCTAssertTrue(editor.hasMarkedText())
        XCTAssertFalse(coordinator.control(control, textView: editor, doCommandBy: #selector(NSResponder.insertNewline(_:))))
        XCTAssertTrue(coordinator.control(control, textView: editor, doCommandBy: #selector(NSResponder.insertTab(_:))))
        XCTAssertEqual(keys, [])

        editor.unmarkText()
        XCTAssertTrue(coordinator.control(control, textView: editor, doCommandBy: #selector(NSResponder.insertNewline(_:))))
        XCTAssertEqual(keys, [.enter])
    }

    func testFieldGivesFocusBackWhenItLeaves() async throws {
        let window = NSWindow(contentRect: CGRect(x: 0, y: 0, width: 400, height: 200), styleMask: [.titled], backing: .buffered, defer: true)
        window.isReleasedWhenClosed = false
        let root = NSView(frame: window.contentLayoutRect)
        window.contentView = root
        let previous = NSTextField(frame: CGRect(x: 0, y: 0, width: 100, height: 24))
        root.addSubview(previous)
        XCTAssertTrue(window.makeFirstResponder(previous))

        let field = QuickSwitchField.KeyField(frame: CGRect(x: 0, y: 100, width: 200, height: 24))
        root.addSubview(field)
        try await waitUntil { field.currentEditor() != nil }
        XCTAssertTrue(field.previousResponder === previous)

        field.removeFromSuperview()
        XCTAssertTrue(previous.currentEditor() != nil)
    }

    func testFieldLeavesFocusAloneWhenSomethingElseTookIt() async throws {
        let window = NSWindow(contentRect: CGRect(x: 0, y: 0, width: 400, height: 200), styleMask: [.titled], backing: .buffered, defer: true)
        window.isReleasedWhenClosed = false
        let root = NSView(frame: window.contentLayoutRect)
        window.contentView = root
        let previous = NSTextField(frame: CGRect(x: 0, y: 0, width: 100, height: 24))
        let chosen = NSTextField(frame: CGRect(x: 120, y: 0, width: 100, height: 24))
        root.addSubview(previous)
        root.addSubview(chosen)
        window.makeFirstResponder(previous)
        let field = QuickSwitchField.KeyField(frame: CGRect(x: 0, y: 100, width: 200, height: 24))
        root.addSubview(field)
        try await waitUntil { field.currentEditor() != nil }

        window.makeFirstResponder(chosen)
        field.removeFromSuperview()
        XCTAssertTrue(chosen.currentEditor() != nil)
        XCTAssertNil(previous.currentEditor())
    }

    func testClearDropsComposingText() async throws {
        let window = NSWindow(contentRect: CGRect(x: 0, y: 0, width: 400, height: 200), styleMask: [.titled], backing: .buffered, defer: true)
        window.isReleasedWhenClosed = false
        let root = NSView(frame: window.contentLayoutRect)
        window.contentView = root
        let field = QuickSwitchField.KeyField(frame: CGRect(x: 0, y: 100, width: 200, height: 24))
        root.addSubview(field)
        try await waitUntil { field.currentEditor() != nil }
        let editor = try XCTUnwrap(field.currentEditor() as? NSTextView)
        editor.insertText("터미", replacementRange: NSRange(location: NSNotFound, length: 0))
        editor.setMarkedText("널", selectedRange: NSRange(location: 1, length: 0), replacementRange: NSRange(location: NSNotFound, length: 0))
        XCTAssertTrue(editor.hasMarkedText())

        field.clear()

        XCTAssertFalse(editor.hasMarkedText())
        XCTAssertEqual(editor.string, "")
        XCTAssertEqual(field.stringValue, "")
    }
}
