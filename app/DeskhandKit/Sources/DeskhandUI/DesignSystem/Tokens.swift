import AppKit
import SwiftUI

/// Deskhand design tokens. Screen code takes colors, spacing, radii and fonts from here only.
public enum DS {
    /// Semantic colors. Every value is a system color, so light, dark and Increase Contrast adapt.
    public enum Color {
        /// App accent: selection, prominent buttons, focus.
        public static let accent = SwiftUI.Color.indigo
        /// A human holds input control.
        public static let human = SwiftUI.Color.blue
        /// An AI client holds input control.
        public static let ai = SwiftUI.Color.purple
        /// Fill under white text for human control: `human` deepened so the text stays legible.
        public static let humanFill = human.mix(with: .black, by: 0.12)
        /// Fill under white text for AI control: `ai` deepened so the text stays legible.
        public static let aiFill = ai.mix(with: .black, by: 0.22)
        /// Nobody holds control; the screen is only watched.
        public static let observe = SwiftUI.Color.secondary
        /// Errors, destructive actions and the emergency stop.
        public static let danger = SwiftUI.Color.red
        public static let success = SwiftUI.Color.green
        public static let warning = SwiftUI.Color.orange
        /// Inactive or unsupported states that carry no alarm.
        public static let neutral = SwiftUI.Color.gray
        /// Window content background.
        public static let surface = SwiftUI.Color(nsColor: .windowBackgroundColor)
        /// Panels and cards that sit one level above `surface`.
        public static let surfaceRaised = SwiftUI.Color(nsColor: .underPageBackgroundColor)
        /// Hairlines and card borders.
        public static let separator = SwiftUI.Color(nsColor: .separatorColor)
        /// Backdrop around the remote screen and behind it before the first frame: a near
        /// black that keeps dark edges of the remote screen distinguishable.
        public static let screenBackdrop = SwiftUI.Color(white: 0.07)
        /// Dims the remote screen under state messages.
        public static let scrim = SwiftUI.Color.black.opacity(0.6)
        /// Background of panels, capsules and toasts drawn over the remote screen.
        public static let overlayPanel = SwiftUI.Color(white: 0.12).opacity(0.92)
        /// Borders of panels and capsules on the remote screen stage.
        public static let overlayHairline = SwiftUI.Color.white.opacity(0.14)
        /// Faint fills on the stage: the loading placeholder, command boxes.
        public static let overlayWash = SwiftUI.Color.white.opacity(0.06)
        /// Spinners and marks drawn directly on the stage.
        public static let overlayForeground = SwiftUI.Color.white.opacity(0.85)
        /// Shadow under cards on the stage.
        public static let overlayShadow = SwiftUI.Color.black.opacity(0.4)
        /// Text and symbols on `humanFill`, `aiFill` and other saturated fills.
        public static let onFill = SwiftUI.Color.white
    }

    /// Spacing scale in points. Layout uses only these values.
    public enum Space {
        public static let xs: CGFloat = 4
        public static let s: CGFloat = 8
        public static let m: CGFloat = 12
        public static let l: CGFloat = 16
        public static let xl: CGFloat = 24
        public static let xxl: CGFloat = 32
    }

    /// Corner radii: `s` for small controls, `m` for banners and rows, `l` for cards and panels.
    public enum Radius {
        public static let s: CGFloat = 6
        public static let m: CGFloat = 10
        public static let l: CGFloat = 14
    }

    /// Text styles. All are system text styles, so they follow the user's text size setting.
    public enum Font {
        /// Hero titles of empty states, full-window errors and the workspace header (22 pt semibold).
        public static let display = SwiftUI.Font.title.weight(.semibold)
        /// Screen and sheet titles (17 pt semibold).
        public static let title = SwiftUI.Font.title2.weight(.semibold)
        /// Section headers and emphasized row text (13 pt bold).
        public static let headline = SwiftUI.Font.headline
        /// Default reading text (13 pt).
        public static let body = SwiftUI.Font.body
        /// Secondary metadata, chips and pills (11 pt).
        public static let caption = SwiftUI.Font.subheadline
        /// Paths, IDs, hashes and command text (12 pt monospaced).
        public static let mono = SwiftUI.Font.system(.callout, design: .monospaced)
    }
}
