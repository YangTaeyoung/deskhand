import AppKit
import SwiftUI

/// What a key does in the console input: ↩ evaluates, ⇧↩ breaks the line, ↑ on the first line and
/// ↓ on the last line walk the history; other cases keep the text view's behavior.
enum ConsoleInputKeys {
    enum Key: Equatable {
        case newline
        case up
        case down
    }

    enum Action: Equatable {
        case submit
        case insertNewline
        case previous
        case next
        case standard
        case ignore
    }

    /// `caret` is a UTF-16 offset into `text`.
    static func action(for key: Key, text: String, caret: Int, shift: Bool) -> Action {
        let utf16 = Array(text.utf16)
        let caret = min(max(0, caret), utf16.count)
        let newline = UInt16(UnicodeScalar("\n").value)
        switch key {
        case .newline:
            if shift { return .insertNewline }
            return text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? .ignore : .submit
        case .up:
            return utf16[..<caret].contains(newline) ? .standard : .previous
        case .down:
            return utf16[caret...].contains(newline) ? .standard : .next
        }
    }
}

/// The console's expression field: a plain monospaced text view without smart substitutions that
/// grows to six lines, evaluates on ↩, breaks lines on ⇧↩ and walks the history with ↑ and ↓.
/// `focusChanged` reports when it gains and loses the keyboard.
struct ConsoleInput: NSViewRepresentable {
    @Binding var text: String
    @Binding var height: CGFloat
    let submit: (String) -> Void
    let previous: (String) -> String?
    let next: () -> String?
    var focusChanged: (Bool) -> Void = { _ in }
    var enabled = true

    static let lineHeight: CGFloat = 16
    static let maximumLines = 6

    /// Space above and below the text.
    static let inset: CGFloat = DS.Space.xs

    /// The field's height for `lines` lines of text, between one and six.
    static func height(lines: Int) -> CGFloat {
        CGFloat(max(1, min(maximumLines, lines))) * lineHeight + inset * 2
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        let textView = ConsoleTextView()
        textView.delegate = context.coordinator
        textView.onFocusChange = { [weak coordinator = context.coordinator] focused in coordinator?.parent.focusChanged(focused) }
        textView.isRichText = false
        textView.importsGraphics = false
        textView.allowsUndo = true
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.isAutomaticSpellingCorrectionEnabled = false
        textView.isContinuousSpellCheckingEnabled = false
        textView.isGrammarCheckingEnabled = false
        textView.isAutomaticLinkDetectionEnabled = false
        textView.smartInsertDeleteEnabled = false
        textView.drawsBackground = false
        textView.font = NSFont.monospacedSystemFont(ofSize: DebugFormat.monoSize, weight: .regular)
        textView.textColor = .textColor
        textView.insertionPointColor = NSColor(DS.Color.accent)
        textView.textContainerInset = NSSize(width: 0, height: ConsoleInput.inset)
        textView.textContainer?.lineFragmentPadding = 0
        textView.isVerticallyResizable = true
        textView.isHorizontallyResizable = false
        textView.autoresizingMask = [.width]
        textView.textContainer?.widthTracksTextView = true
        textView.setAccessibilityLabel(String.ui("debugger.console.input.a11y", "평가할 식"))
        textView.string = text
        scroll.documentView = textView
        context.coordinator.textView = textView
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.parent = self
        guard let textView = context.coordinator.textView else { return }
        if textView.string != text {
            textView.string = text
            textView.setSelectedRange(NSRange(location: (text as NSString).length, length: 0))
        }
        textView.isEditable = enabled
        context.coordinator.measure()
    }

    final class ConsoleTextView: NSTextView {
        var onFocusChange: ((Bool) -> Void)?

        override var acceptsFirstResponder: Bool { isEditable }

        override func becomeFirstResponder() -> Bool {
            let became = super.becomeFirstResponder()
            if became { onFocusChange?(true) }
            return became
        }

        override func resignFirstResponder() -> Bool {
            let resigned = super.resignFirstResponder()
            if resigned { onFocusChange?(false) }
            return resigned
        }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            if window == nil { onFocusChange?(false) }
        }
    }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: ConsoleInput
        weak var textView: NSTextView?

        init(_ parent: ConsoleInput) {
            self.parent = parent
        }

        func textDidChange(_ notification: Notification) {
            guard let textView else { return }
            parent.text = textView.string
            measure()
        }

        /// Reports the height of the text, between one and six lines.
        func measure() {
            guard let textView, let container = textView.textContainer, let layout = textView.layoutManager else { return }
            layout.ensureLayout(for: container)
            var lines = 0
            var index = 0
            while index < layout.numberOfGlyphs {
                var range = NSRange()
                layout.lineFragmentRect(forGlyphAt: index, effectiveRange: &range)
                index = max(index + 1, NSMaxRange(range))
                lines += 1
            }
            if textView.string.hasSuffix("\n") { lines += 1 }
            let height = ConsoleInput.height(lines: lines)
            if abs(parent.height - height) > 0.5 {
                let binding = parent.$height
                DispatchQueue.main.async { binding.wrappedValue = height }
            }
        }

        func textView(_ textView: NSTextView, doCommandBy selector: Selector) -> Bool {
            let key: ConsoleInputKeys.Key
            switch selector {
            case #selector(NSResponder.insertNewline(_:)): key = .newline
            case #selector(NSResponder.insertLineBreak(_:)), #selector(NSResponder.insertNewlineIgnoringFieldEditor(_:)):
                textView.insertText("\n", replacementRange: textView.selectedRange())
                return true
            case #selector(NSResponder.moveUp(_:)): key = .up
            case #selector(NSResponder.moveDown(_:)): key = .down
            default: return false
            }
            let shift = NSApp.currentEvent?.modifierFlags.contains(.shift) ?? false
            switch ConsoleInputKeys.action(for: key, text: textView.string, caret: textView.selectedRange().location, shift: shift) {
            case .submit:
                let expression = textView.string
                parent.submit(expression)
                replace(with: "", in: textView)
                return true
            case .insertNewline:
                textView.insertText("\n", replacementRange: textView.selectedRange())
                return true
            case .previous:
                if let value = parent.previous(textView.string) { replace(with: value, in: textView) }
                return true
            case .next:
                if let value = parent.next() { replace(with: value, in: textView) }
                return true
            case .ignore:
                return true
            case .standard:
                return false
            }
        }

        private func replace(with value: String, in textView: NSTextView) {
            textView.string = value
            textView.setSelectedRange(NSRange(location: (value as NSString).length, length: 0))
            parent.text = value
            measure()
        }
    }
}
