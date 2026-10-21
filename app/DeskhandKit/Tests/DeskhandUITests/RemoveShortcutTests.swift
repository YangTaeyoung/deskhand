import AppKit
import SwiftUI
@testable import DeskhandUI
import XCTest

@MainActor
final class RemoveShortcutTests: XCTestCase {
    func testCommandDeleteRemovesOnlyWhileTheSidebarHasFocusAndNoTextIsEdited() {
        XCTAssertEqual(RemoveHostShortcut.shortcut(sidebarFocused: true, editingText: false),
                       KeyboardShortcut(.delete, modifiers: .command))
        XCTAssertNil(RemoveHostShortcut.shortcut(sidebarFocused: false, editingText: false))
        XCTAssertNil(RemoveHostShortcut.shortcut(sidebarFocused: true, editingText: true))
        XCTAssertNil(RemoveHostShortcut.shortcut(sidebarFocused: false, editingText: true))
    }

    func testTextViewsAndFieldEditorsCountAsEditingText() {
        XCTAssertTrue(RemoveHostShortcut.isEditingText(NSTextView()))
        XCTAssertTrue(RemoveHostShortcut.isEditingText(NSTextField()))
        XCTAssertFalse(RemoveHostShortcut.isEditingText(NSTableView()))
        XCTAssertFalse(RemoveHostShortcut.isEditingText(NSView()))
        XCTAssertFalse(RemoveHostShortcut.isEditingText(nil))
    }

    func testTheSidebarFocusIsKeptInTheLayoutAndStartsOff() {
        let layout = WorkspaceLayout(defaults: UserDefaults(suiteName: "remove-shortcut-\(UUID())")!)
        XCTAssertFalse(layout.sidebarFocused)
        layout.sidebarFocused = true
        XCTAssertTrue(layout.sidebarFocused)
    }
}
