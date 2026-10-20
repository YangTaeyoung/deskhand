import AppKit
import SwiftUI

/// The palette's search field: a borderless text field that takes focus when it appears, reports
/// the text including what the input method is still composing, and turns ↑ ↓ ↩ esc and
/// ⌘1–⌘9 into `QuickSwitchKey`s.
struct QuickSwitchField: NSViewRepresentable {
    @Binding var text: String
    let placeholder: String
    let onKey: (QuickSwitchKey) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> KeyField {
        let field = KeyField()
        field.isBordered = false
        field.drawsBackground = false
        field.focusRingType = .none
        field.font = .systemFont(ofSize: 20, weight: .regular)
        field.placeholderString = placeholder
        field.lineBreakMode = .byTruncatingTail
        field.cell?.isScrollable = true
        field.cell?.wraps = false
        field.delegate = context.coordinator
        field.stringValue = text
        field.onKey = { key in context.coordinator.parent.onKey(key) }
        field.setAccessibilityLabel(placeholder)
        return field
    }

    func updateNSView(_ field: KeyField, context: Context) {
        context.coordinator.parent = self
        if field.currentEditor() == nil, field.stringValue != text {
            field.stringValue = text
        } else if let editor = field.currentEditor() as? NSTextView, !editor.hasMarkedText(), editor.string != text {
            editor.string = text
        }
    }

    @MainActor
    final class Coordinator: NSObject, NSTextFieldDelegate {
        var parent: QuickSwitchField

        init(_ parent: QuickSwitchField) {
            self.parent = parent
        }

        func controlTextDidChange(_ notification: Notification) {
            let editor = notification.userInfo?["NSFieldEditor"] as? NSTextView
            let field = notification.object as? NSTextField
            parent.text = editor?.string ?? field?.stringValue ?? ""
        }

        func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
            let key: QuickSwitchKey? = switch selector {
            case #selector(NSResponder.moveUp(_:)): .up
            case #selector(NSResponder.moveDown(_:)): .down
            case #selector(NSResponder.insertNewline(_:)): .enter
            case #selector(NSResponder.cancelOperation(_:)): .escape
            default: nil
            }
            guard let key else { return false }
            if textView.hasMarkedText(), key == .enter || key == .escape { return false }
            parent.onKey(key)
            return true
        }
    }

    /// A text field that becomes first responder once it is in a window and handles ⌘1–⌘9
    /// before the menus do.
    final class KeyField: NSTextField {
        var onKey: (QuickSwitchKey) -> Void = { _ in }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window, window.isKeyWindow || window.canBecomeKey else { return }
            DispatchQueue.main.async { [weak self] in
                guard let self, let window = self.window else { return }
                window.makeFirstResponder(self)
            }
        }

        override func performKeyEquivalent(with event: NSEvent) -> Bool {
            let flags = event.modifierFlags.intersection([.command, .shift, .option, .control])
            if flags == [.command], let digit = event.charactersIgnoringModifiers.flatMap(Int.init), (1...9).contains(digit),
               currentEditor() != nil {
                onKey(.commandDigit(digit))
                return true
            }
            return super.performKeyEquivalent(with: event)
        }
    }
}
