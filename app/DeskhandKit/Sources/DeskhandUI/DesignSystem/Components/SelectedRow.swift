import AppKit
import SwiftUI

extension DS {
    /// How rows draw while selected: the selection fill under white text in a window the person
    /// works in, the system's unemphasized selection in an inactive window. A chip on a selected
    /// row has no fill of its own — a light outline around white text on the selection fill — so
    /// its text reads like the row's text.
    public enum SelectedRow {
        /// Opacity of the white outline of a chip on a selected row.
        public static let chipOutlineOpacity: CGFloat = 0.7

        /// The fill of a selected AppKit row: `Status.selectionFillNS`, or the system's
        /// unemphasized selection when the row's window is shown and not key.
        @MainActor
        static func fill(in window: NSWindow?) -> NSColor {
            isActive(window) ? DS.Status.selectionFillNS : .unemphasizedSelectedContentBackgroundColor
        }

        /// The window is key, or not on screen (offscreen drawing), or there is none.
        @MainActor
        static func isActive(_ window: NSWindow?) -> Bool {
            guard let window else { return true }
            return window.isKeyWindow || !window.isVisible
        }
    }
}

extension View {
    /// The background of a small capsule chip: `tint` at the badge opacity, or on a selected row
    /// a light outline without a fill.
    func chipBackground(_ tint: Color, selected: Bool) -> some View {
        background(selected ? AnyShapeStyle(.clear) : AnyShapeStyle(tint.opacity(DS.Status.badgeOpacity)), in: Capsule())
            .overlay {
                if selected { Capsule().strokeBorder(DS.Color.onFill.opacity(DS.SelectedRow.chipOutlineOpacity), lineWidth: 1) }
            }
    }
}
