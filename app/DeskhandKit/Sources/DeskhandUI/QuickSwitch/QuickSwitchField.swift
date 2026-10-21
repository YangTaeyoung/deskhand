import AppKit
import SwiftUI

/// The palette's search field: a borderless text field that takes focus when it appears and
/// gives it back to the view that had it when it goes away, reports the text including what the
/// input method is still composing, and turns ↑ ↓ ↩ esc into `QuickSwitchKey`s. Tab and ⇧Tab
/// stay in the field. A change of `clearRequests` empties the field, composition included.
struct QuickSwitchField: NSViewRepresentable {
    @Binding var text: String
    let placeholder: String
    let clearRequests: Int
    let onKey: (QuickSwitchKey) -> Void

    /// What the field does with an editing command.
    enum Command: Equatable {
        case key(QuickSwitchKey)
        /// Handled by doing nothing, so focus stays in the field.
        case swallow
        /// Left to the text system.
        case pass
    }

    /// The command for `selector`; while the input method composes, ↩ and esc belong to it.
    static func command(for selector: Selector, composing: Bool) -> Command {
        switch selector {
        case #selector(NSResponder.moveUp(_:)): return .key(.up)
        case #selector(NSResponder.moveDown(_:)): return .key(.down)
        case #selector(NSResponder.insertNewline(_:)): return composing ? .pass : .key(.enter)
        case #selector(NSResponder.cancelOperation(_:)): return composing ? .pass : .key(.escape)
        case #selector(NSResponder.insertTab(_:)), #selector(NSResponder.insertBacktab(_:)): return .swallow
        default: return .pass
        }
    }

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
        field.setAccessibilityLabel(placeholder)
        context.coordinator.clears = clearRequests
        return field
    }

    static func dismantleNSView(_ field: KeyField, coordinator: Coordinator) {
        field.restoreFocus()
    }

    func updateNSView(_ field: KeyField, context: Context) {
        let coordinator = context.coordinator
        coordinator.parent = self
        if clearRequests != coordinator.clears {
            coordinator.clears = clearRequests
            field.clear()
            return
        }
        if field.currentEditor() == nil, field.stringValue != text {
            field.stringValue = text
        } else if let editor = field.currentEditor() as? NSTextView, !editor.hasMarkedText(), editor.string != text {
            editor.string = text
        }
    }

    @MainActor
    final class Coordinator: NSObject, NSTextFieldDelegate {
        var parent: QuickSwitchField
        var clears = 0

        init(_ parent: QuickSwitchField) {
            self.parent = parent
        }

        func controlTextDidChange(_ notification: Notification) {
            let editor = notification.userInfo?["NSFieldEditor"] as? NSTextView
            let field = notification.object as? NSTextField
            parent.text = editor?.string ?? field?.stringValue ?? ""
        }

        func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
            switch QuickSwitchField.command(for: selector, composing: textView.hasMarkedText()) {
            case .key(let key):
                parent.onKey(key)
                return true
            case .swallow:
                return true
            case .pass:
                return false
            }
        }
    }

    /// A text field that becomes first responder once it is in a window, remembering what was
    /// first responder before, and hands focus back to it when it leaves the window while it
    /// still has focus.
    final class KeyField: NSTextField {
        private(set) weak var previousResponder: NSResponder?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window, window.isKeyWindow || window.canBecomeKey else { return }
            previousResponder = Self.owner(of: window.firstResponder)
            DispatchQueue.main.async { [weak self] in
                guard let self, let window = self.window else { return }
                window.makeFirstResponder(self)
            }
        }

        override func viewWillMove(toSuperview newSuperview: NSView?) {
            if newSuperview == nil { restoreFocus() }
            super.viewWillMove(toSuperview: newSuperview)
        }

        override func viewWillMove(toWindow newWindow: NSWindow?) {
            if newWindow == nil { restoreFocus() }
            super.viewWillMove(toWindow: newWindow)
        }

        /// Makes the remembered responder first responder again while this field has focus, or
        /// when the window itself holds focus because AppKit took it from this field as it left.
        func restoreFocus() {
            guard let window, hasFocus(in: window) || window.firstResponder === window else { return }
            let previous = previousResponder
            previousResponder = nil
            window.makeFirstResponder(previous.flatMap { ($0 as? NSView)?.window === window ? $0 : nil })
        }

        /// Empties the field and drops text the input method is composing.
        func clear() {
            if let editor = currentEditor() as? NSTextView {
                editor.inputContext?.discardMarkedText()
                editor.unmarkText()
                editor.string = ""
            }
            stringValue = ""
        }

        private func hasFocus(in window: NSWindow) -> Bool {
            let responder = window.firstResponder
            return responder === self || (responder != nil && responder === currentEditor())
        }

        /// The view a responder stands for: the text field a field editor edits, otherwise itself.
        private static func owner(of responder: NSResponder?) -> NSResponder? {
            if let editor = responder as? NSTextView, editor.isFieldEditor, let field = editor.delegate as? NSResponder {
                return field
            }
            return responder
        }
    }
}
