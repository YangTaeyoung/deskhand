import AppKit
import SwiftUI

extension DS {
    /// Text size and row height of the AppKit tables for the text size setting: 13 pt text in
    /// 24 pt rows at the default size, both growing with larger sizes; rows stay on the 4-pt grid.
    public struct TableMetrics: Equatable, Sendable {
        public let scale: CGFloat

        public init(_ size: DynamicTypeSize) {
            scale = Self.scale(size)
        }

        /// How much larger than the default each text size draws.
        static func scale(_ size: DynamicTypeSize) -> CGFloat {
            switch size {
            case .xSmall: 0.85
            case .small: 0.9
            case .medium: 0.95
            case .large: 1
            case .xLarge: 1.1
            case .xxLarge: 1.2
            case .xxxLarge: 1.3
            case .accessibility1: 1.5
            case .accessibility2: 1.75
            case .accessibility3: 2
            case .accessibility4: 2.25
            case .accessibility5: 2.5
            @unknown default: 1
            }
        }

        public var textSize: CGFloat { (NSFont.systemFontSize * scale).rounded() }
        public var smallTextSize: CGFloat { (NSFont.smallSystemFontSize * scale).rounded() }
        /// A symbol beside row text.
        public var symbolSize: CGFloat { (12 * scale).rounded() }
        public var rowHeight: CGFloat { max(24, (24 * scale / 4).rounded(.up) * 4) }

        /// A column width given for the default text size, at this text size.
        public func columnWidth(_ width: CGFloat) -> CGFloat { (width * scale).rounded() }

        /// A column width drawn at this text size, at the default text size.
        public func baseWidth(_ width: CGFloat) -> CGFloat { width / scale }

        /// The name a table saves its column widths under at this text size; the default size
        /// keeps `name`, so each text size keeps its own widths.
        public func autosaveName(_ name: String) -> String {
            scale == 1 ? name : name + "@" + String(Int((scale * 100).rounded()))
        }
    }
}

/// A table whose cells take their text size from `metrics`.
@MainActor
protocol TextScaledTable: AnyObject {
    var metrics: DS.TableMetrics { get }
}

extension NSTableView {
    /// The text metrics of a `TextScaledTable`, or the default ones.
    @MainActor var textMetrics: DS.TableMetrics {
        (self as? TextScaledTable)?.metrics ?? DS.TableMetrics(.large)
    }
}
