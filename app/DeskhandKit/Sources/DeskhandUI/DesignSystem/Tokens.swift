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
        /// Background of the remote terminal and of its selected tab.
        public static let terminalBackground = SwiftUI.Color(nsColor: .textBackgroundColor)
    }

    /// Colors of script source, values and console text in the debugger. The text colors are system
    /// hues blended toward black (light) or white (dark) until they reach 4.5:1 against the text and
    /// window backgrounds of the appearance and the text background under every tint in `tints`.
    public enum Code {
        /// A color laid over the text background at an opacity.
        public struct Tint: Sendable {
            public let color: NSColor
            public let opacity: CGFloat

            public init(_ color: NSColor, _ opacity: CGFloat) {
                self.color = color
                self.opacity = opacity
            }

            public var swiftUI: SwiftUI.Color { SwiftUI.Color(nsColor: color).opacity(opacity) }
        }

        /// The line the paused frame is on.
        public static let currentLineTint = Tint(.systemGreen, 0.16)
        /// The line of a caller frame the person selected.
        public static let callerLineTint = Tint(.systemGray, 0.16)
        /// The line a paused exception was thrown on.
        public static let exceptionLineTint = Tint(.systemRed, 0.14)
        /// A location opened from the breakpoints or the console.
        public static let focusLineTint = Tint(.systemIndigo, 0.12)
        /// Selected console, variable and breakpoint rows.
        public static let selectionTint = Tint(.systemIndigo, 0.16)
        /// Console error rows and the exception row of the variables.
        public static let errorRowTint = Tint(.systemRed, 0.06)
        /// Console warning rows.
        public static let warningRowTint = Tint(.systemOrange, 0.08)

        /// JavaScript keywords and the literals `true`, `false`, `null`, `undefined`.
        public static let keyword = SwiftUI.Color(nsColor: keywordNS)
        /// String, template and regular expression literals and string values.
        public static let string = SwiftUI.Color(nsColor: stringNS)
        /// Numbers and bigints.
        public static let number = SwiftUI.Color(nsColor: numberNS)
        /// Source locations and other links in the console.
        public static let link = SwiftUI.Color(nsColor: linkNS)
        /// Text of console errors and thrown values.
        public static let errorText = SwiftUI.Color(nsColor: errorTextNS)
        /// Comments, `undefined` values and internal properties.
        public static let comment = SwiftUI.Color.secondary
        public static let currentLine = currentLineTint.swiftUI
        public static let callerLine = callerLineTint.swiftUI
        public static let exceptionLine = exceptionLineTint.swiftUI
        public static let focusLine = focusLineTint.swiftUI
        public static let selection = selectionTint.swiftUI
        public static let errorRow = errorRowTint.swiftUI
        public static let warningRow = warningRowTint.swiftUI
        /// Breakpoint markers in the gutter and the breakpoints list.
        public static let breakpoint = SwiftUI.Color(nsColor: .systemBlue)
        /// Background of the source and console panes.
        public static let background = SwiftUI.Color(nsColor: .textBackgroundColor)
        /// The gutter beside the source lines.
        public static let gutter = SwiftUI.Color(nsColor: .underPageBackgroundColor).opacity(0.5)

        /// Every tint code text sits on.
        public static let tints = [currentLineTint, callerLineTint, exceptionLineTint, focusLineTint, selectionTint, errorRowTint,
                                   warningRowTint]

        public static let keywordNS = readable(.systemPink, tints: tints)
        public static let stringNS = readable(.systemRed, tints: tints)
        public static let numberNS = readable(.systemBlue, tints: tints)
        public static let linkNS = readable(.systemIndigo, tints: tints)
        public static let errorTextNS = readable(.systemRed, tints: tints)

        /// The backgrounds code text sits on in `appearance`: the text and window backgrounds, and
        /// the text background under each of `tints`.
        static func backgrounds(in appearance: NSAppearance, tints: [Tint]) -> [NSColor] {
            var colors: [NSColor] = []
            appearance.performAsCurrentDrawingAppearance {
                let text = NSColor.textBackgroundColor.usingColorSpace(.sRGB) ?? .white
                let window = NSColor.windowBackgroundColor.usingColorSpace(.sRGB) ?? .white
                colors = [text, window]
                for tint in tints {
                    guard let shade = tint.color.usingColorSpace(.sRGB) else { continue }
                    colors.append(text.blended(withFraction: tint.opacity, of: shade) ?? text)
                }
            }
            return colors
        }

        /// `base` blended toward black (light) or white (dark) until it reaches 4.5:1 against every background.
        static func readable(_ base: NSColor, tints: [Tint] = [], ratio: Double = 4.5) -> NSColor {
            NSColor(name: nil) { appearance in
                let dark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                var resolved = base
                appearance.performAsCurrentDrawingAppearance {
                    resolved = base.usingColorSpace(.sRGB) ?? base
                }
                let backgrounds = backgrounds(in: appearance, tints: tints)
                func passes(_ color: NSColor) -> Bool {
                    backgrounds.allSatisfy { Terminal.contrast(color, $0) >= ratio }
                }
                if passes(resolved) { return resolved }
                let target: NSColor = dark ? .white : .black
                var low: CGFloat = 0
                var high: CGFloat = 1
                for _ in 0..<20 {
                    let mid = (low + high) / 2
                    let mixed = resolved.blended(withFraction: mid, of: target) ?? resolved
                    if passes(mixed) { high = mid } else { low = mid }
                }
                return (resolved.blended(withFraction: high, of: target) ?? resolved).usingColorSpace(.sRGB) ?? resolved
            }
        }
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

    /// Remote terminal colors and type. Every color is a system color or one mixed from it,
    /// resolved for the appearance the terminal draws in.
    public enum Terminal {
        /// Default text size of the terminal in points.
        public static let fontSize: CGFloat = 13
        public static let minimumFontSize: CGFloat = 9
        public static let maximumFontSize: CGFloat = 28

        /// SF Mono at `size`.
        public static func font(size: CGFloat) -> NSFont {
            NSFont.monospacedSystemFont(ofSize: size, weight: .regular)
        }

        /// Colors of a terminal in one appearance.
        public struct Palette {
            public let background: NSColor
            public let foreground: NSColor
            public let caret: NSColor
            public let selection: NSColor
            public let selectedText: NSColor
            /// ANSI colors 0–15: black, red, green, yellow, blue, magenta, cyan, white, then the bright variants.
            public let ansi: [NSColor]
        }

        /// The palette for `appearance`, with every color converted to sRGB in that appearance.
        @MainActor
        public static func palette(for appearance: NSAppearance) -> Palette {
            let dark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            var palette: Palette?
            appearance.performAsCurrentDrawingAppearance {
                palette = makePalette(dark: dark)
            }
            return palette ?? makePalette(dark: dark)
        }

        /// Contrast each ANSI color keeps against the background: at least 4.5:1 (WCAG AA), except
        /// black in the dark palette and white and bright white in the light one, which stay black,
        /// light gray and white.
        static let normalContrast: (light: Double, dark: Double) = (6.5, 4.8)
        static let brightContrast: (light: Double, dark: Double) = (4.6, 7.0)

        private static func makePalette(dark: Bool) -> Palette {
            func srgb(_ color: NSColor) -> NSColor { color.usingColorSpace(.sRGB) ?? color }
            let background = srgb(.textBackgroundColor)
            let foreground = srgb(.textColor)
            /// `color` blended toward black (light) or white (dark) until it reaches `ratio` against the background.
            func readable(_ color: NSColor, _ ratio: Double) -> NSColor {
                let base = srgb(color)
                let target: NSColor = dark ? .white : .black
                if contrast(base, background) >= ratio { return base }
                var low: CGFloat = 0
                var high: CGFloat = 1
                for _ in 0..<20 {
                    let mid = (low + high) / 2
                    let mixed = base.blended(withFraction: mid, of: target) ?? base
                    if contrast(mixed, background) >= ratio { high = mid } else { low = mid }
                }
                return srgb(base.blended(withFraction: high, of: target) ?? base)
            }
            func gray(_ white: CGFloat) -> NSColor { NSColor(white: white, alpha: 1) }
            let hues: [NSColor] = [.systemRed, .systemGreen, .systemYellow, .systemBlue, .systemPurple, .systemCyan]
            let normal: [NSColor]
            let bright: [NSColor]
            if dark {
                normal = [gray(0)] + hues.map { readable($0, normalContrast.dark) } + [gray(0.8)]
                bright = [readable(gray(0.5), 5.6)] + hues.map { readable(srgb($0).blended(withFraction: 0.3, of: .white) ?? $0, brightContrast.dark) }
                    + [gray(1)]
            } else {
                normal = [gray(0)] + hues.map { readable($0, normalContrast.light) } + [gray(0.78)]
                bright = [readable(gray(0.6), 5.6)] + hues.map { readable($0, brightContrast.light) } + [gray(1)]
            }
            return Palette(
                background: background,
                foreground: foreground,
                caret: srgb(NSColor(DS.Color.accent)),
                selection: srgb(.selectedTextBackgroundColor),
                selectedText: srgb(.selectedTextColor),
                ansi: (normal + bright).map(srgb))
        }

        /// WCAG contrast ratio of two colors.
        static func contrast(_ a: NSColor, _ b: NSColor) -> Double {
            let (l1, l2) = (luminance(a), luminance(b))
            return (max(l1, l2) + 0.05) / (min(l1, l2) + 0.05)
        }

        /// WCAG relative luminance of an sRGB color.
        static func luminance(_ color: NSColor) -> Double {
            let c = color.usingColorSpace(.sRGB) ?? color
            func channel(_ value: CGFloat) -> Double {
                let v = Double(value)
                return v <= 0.04045 ? v / 12.92 : pow((v + 0.055) / 1.055, 2.4)
            }
            return 0.2126 * channel(c.redComponent) + 0.7152 * channel(c.greenComponent) + 0.0722 * channel(c.blueComponent)
        }
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
