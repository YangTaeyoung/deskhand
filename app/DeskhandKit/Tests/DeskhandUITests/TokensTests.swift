import DeskhandClient
import AppKit
import SwiftUI
import XCTest
@testable import DeskhandUI

final class TokensTests: XCTestCase {
    func testSpacingScale() {
        let scale = [DS.Space.xs, DS.Space.s, DS.Space.m, DS.Space.l, DS.Space.xl, DS.Space.xxl]
        XCTAssertEqual(scale, [4, 8, 12, 16, 24, 32])
    }

    func testRadiusScale() {
        XCTAssertEqual([DS.Radius.s, DS.Radius.m, DS.Radius.l], [6, 10, 14])
    }

    func testSemanticColorsDistinct() throws {
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let appearance = try XCTUnwrap(NSAppearance(named: name))
            var resolved: [SRGB?] = []
            appearance.performAsCurrentDrawingAppearance {
                resolved = [DS.Color.human, DS.Color.ai, DS.Color.danger].map { try? SRGB($0) }
            }
            let human = try XCTUnwrap(resolved[0])
            let ai = try XCTUnwrap(resolved[1])
            let danger = try XCTUnwrap(resolved[2])
            XCTAssertGreaterThan(human.distance(to: ai), 0.2, "human vs ai in \(name.rawValue)")
            XCTAssertGreaterThan(human.distance(to: danger), 0.2, "human vs danger in \(name.rawValue)")
            XCTAssertGreaterThan(ai.distance(to: danger), 0.2, "ai vs danger in \(name.rawValue)")
        }
    }

    func testSemanticColorsUseSystemPalette() throws {
        let pairs: [(Color, NSColor, String)] = [
            (DS.Color.accent, .systemIndigo, "accent"),
            (DS.Color.human, .systemBlue, "human"),
            (DS.Color.ai, .systemPurple, "ai"),
            (DS.Color.danger, .systemRed, "danger"),
            (DS.Color.success, .systemGreen, "success"),
            (DS.Color.warning, .systemOrange, "warning"),
        ]
        for (token, system, label) in pairs {
            let resolved = try SRGB(token)
            let expected = try SRGB(system)
            XCTAssertLessThan(resolved.distance(to: expected), 0.01, label)
        }
    }

    func testCodeTextReachesFourAndAHalfToOneOnEveryBackgroundItSitsOn() throws {
        let colors: [(String, NSColor)] = [
            ("keyword", DS.Code.keywordNS), ("string", DS.Code.stringNS), ("number", DS.Code.numberNS),
            ("link", DS.Code.linkNS), ("error", DS.Code.errorTextNS),
        ]
        XCTAssertEqual(DS.Code.tints.count, 7)
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let appearance = try XCTUnwrap(NSAppearance(named: name))
            let backgrounds = DS.Code.backgrounds(in: appearance, tints: DS.Code.tints)
            XCTAssertEqual(backgrounds.count, 2 + DS.Code.tints.count * 2)
            for (label, color) in colors {
                var resolved = NSColor.black
                appearance.performAsCurrentDrawingAppearance { resolved = color.usingColorSpace(.sRGB) ?? color }
                for (index, background) in backgrounds.enumerated() {
                    let ratio = DS.Terminal.contrast(resolved, background)
                    XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(label) on background \(index) in \(name.rawValue): \(ratio)")
                }
            }
        }
    }

    /// Draws every code color as a swatch on the text and window backgrounds, bare and under each
    /// tint, and the keyword of a highlighted line on the current and exception lines, in an
    /// offscreen sRGB window as the snapshots draw; the drawn pixels reach 4.5:1.
    @MainActor
    func testDrawnCodeColorsReachFourAndAHalfToOneOnTheirDrawnBackgrounds() throws {
        let colors: [(String, Color)] = [
            ("keyword", DS.Code.keyword), ("string", DS.Code.string), ("number", DS.Code.number),
            ("link", DS.Code.link), ("error", DS.Code.errorText),
        ]
        let tints: [(String, Color?)] = [("bare", nil)] + [
            ("current line", DS.Code.currentLine), ("caller line", DS.Code.callerLine), ("exception line", DS.Code.exceptionLine),
            ("focus line", DS.Code.focusLine), ("selection", DS.Code.selection), ("error row", DS.Code.errorRow),
            ("warning row", DS.Code.warningRow),
        ]
        let bases: [(String, Color)] = [("text", DS.Code.background), ("window", DS.Color.surface)]
        let cell: CGFloat = 24
        let swatches = VStack(spacing: 0) {
            ForEach(Array(bases.enumerated()), id: \.offset) { _, base in
                ForEach(Array(tints.enumerated()), id: \.offset) { _, tint in
                    HStack(spacing: 0) {
                        ForEach(Array(colors.enumerated()), id: \.offset) { _, color in
                            ZStack {
                                Rectangle().fill(color.1).frame(width: cell / 2, height: cell / 2)
                            }
                            .frame(width: cell, height: cell)
                            .background(tint.1 ?? .clear)
                            .background(base.1)
                        }
                    }
                }
            }
        }
        let size = CGSize(width: cell * CGFloat(colors.count), height: cell * CGFloat(tints.count * bases.count))
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let bitmap = try Self.draw(swatches, size: size, appearance: name)
            let scale = CGFloat(bitmap.pixelsWide) / size.width
            for (b, base) in bases.enumerated() {
                for (t, tint) in tints.enumerated() {
                    for (c, color) in colors.enumerated() {
                        let row = CGFloat(b * tints.count + t)
                        let origin = CGPoint(x: CGFloat(c) * cell * scale, y: row * cell * scale)
                        let swatch = Self.pixel(bitmap, x: Int(origin.x + cell * scale / 2), y: Int(origin.y + cell * scale / 2))
                        let ground = Self.pixel(bitmap, x: Int(origin.x + 2), y: Int(origin.y + 2))
                        let ratio = DS.Terminal.contrast(swatch, ground)
                        XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(color.0) on \(tint.0) over \(base.0) in \(name.rawValue): \(ratio)")
                    }
                }
            }

            for tint in [DS.Code.currentLine, DS.Code.exceptionLine] {
                let line = Text(JavaScriptHighlighter.highlight("const"))
                    .font(.system(size: 48, weight: .heavy, design: .monospaced))
                    .frame(width: 200, height: 64)
                    .background(tint)
                    .background(DS.Code.background)
                let drawn = try Self.draw(line, size: CGSize(width: 200, height: 64), appearance: name)
                let ground = Self.pixel(drawn, x: 2, y: 2)
                var best = 0.0
                for y in stride(from: 0, to: drawn.pixelsHigh, by: 2) {
                    for x in stride(from: 0, to: drawn.pixelsWide, by: 2) {
                        best = max(best, DS.Terminal.contrast(Self.pixel(drawn, x: x, y: y), ground))
                    }
                }
                XCTAssertGreaterThanOrEqual(best, 4.5, "drawn keyword in \(name.rawValue)")
            }
        }
    }

    func testStatusTextReachesFourAndAHalfToOneOnEveryBackgroundItSitsOn() throws {
        let colors: [(String, NSColor, NSColor)] = [
            ("success", DS.Status.successTextNS, .systemGreen), ("danger", DS.Status.dangerTextNS, .systemRed),
            ("warning", DS.Status.warningTextNS, .systemOrange), ("accent", DS.Status.accentTextNS, .systemIndigo),
            ("neutral", DS.Status.neutralTextNS, .systemGray), ("human", DS.Status.humanTextNS, .systemBlue),
            ("ai", DS.Status.aiTextNS, .systemPurple),
        ]
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let appearance = try XCTUnwrap(NSAppearance(named: name))
            for (label, color, hue) in colors {
                var resolved = NSColor.black
                appearance.performAsCurrentDrawingAppearance { resolved = color.usingColorSpace(.sRGB) ?? color }
                let hues = label == "neutral" ? DS.Status.tintHues : [hue]
                let backgrounds = DS.Status.backgrounds(in: appearance, hues: hues)
                XCTAssertEqual(backgrounds.count, 3 * (3 + hues.count * 4 + DS.Status.neutralOpacities.count))
                for (index, background) in backgrounds.enumerated() {
                    let ratio = DS.Terminal.contrast(resolved, background)
                    XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(label) on background \(index) in \(name.rawValue): \(ratio)")
                }
            }
        }
    }

    /// Draws each status text color on the window, the text background and the run list's
    /// backdrop, bare and under its badge, box, band, hovered badge and neutral chip tints — the
    /// neutral gray also under every other hue's box and band — in an offscreen sRGB window; the
    /// drawn pixels reach 4.5:1. White on the selection, human, AI, danger and neutral fills does too.
    @MainActor
    func testDrawnStatusTextReachesFourAndAHalfToOneOnItsDrawnBackgrounds() throws {
        let colors: [(String, Color, Color)] = [
            ("success", DS.Status.successText, DS.Color.success), ("danger", DS.Status.dangerText, DS.Color.danger),
            ("warning", DS.Status.warningText, DS.Color.warning), ("accent", DS.Status.accentText, DS.Color.accent),
            ("neutral", DS.Status.neutralText, DS.Color.neutral), ("human", DS.Status.humanText, DS.Color.human),
            ("ai", DS.Status.aiText, DS.Color.ai),
        ]
        let fills: [(String, Color)] = [("selection", DS.Status.selectionFill), ("human", DS.Color.humanFill),
                                        ("ai", DS.Color.aiFill), ("danger", DS.Color.dangerFill),
                                        ("neutral", DS.Color.neutralFill)]
        let bases: [(String, Color, Color?)] = [("window", DS.Color.surface, nil), ("text", DS.Code.background, nil),
                                                ("list", DS.Color.surface, DS.Status.listBackground)]
        let layers: [(String, (Color) -> [Color])] = [
            ("bare", { _ in [] }), ("badge", { [$0.opacity(DS.Status.badgeOpacity)] }), ("box", { [$0.opacity(DS.Status.boxOpacity)] }),
            ("band", { [$0.opacity(DS.Status.bandOpacity)] }),
            ("hovered badge", { [DS.Color.neutral.opacity(0.08), $0.opacity(DS.Status.badgeOpacity)] }),
        ] + DS.Status.neutralOpacities.map { opacity in ("neutral \(opacity)", { _ in [DS.Color.neutral.opacity(opacity)] }) }
            + [DS.Color.success, DS.Color.danger, DS.Color.warning, DS.Color.accent, DS.Color.human, DS.Color.ai].flatMap { tint in
                [("box of another hue", { _ in [tint.opacity(DS.Status.boxOpacity)] }),
                 ("band of another hue", { _ in [tint.opacity(DS.Status.bandOpacity)] })]
            }
        let cell: CGFloat = 24
        let rows = bases.count * layers.count
        let swatches = VStack(spacing: 0) {
            ForEach(Array(bases.enumerated()), id: \.offset) { _, base in
                ForEach(Array(layers.enumerated()), id: \.offset) { _, layer in
                    HStack(spacing: 0) {
                        ForEach(Array(colors.enumerated()), id: \.offset) { _, color in
                            let fills = layer.1(color.2)
                            ZStack {
                                base.1
                                if let over = base.2 { over }
                                ForEach(Array(fills.enumerated()), id: \.offset) { _, fill in fill }
                                Rectangle().fill(color.1).frame(width: cell / 2, height: cell / 2)
                            }
                            .frame(width: cell, height: cell)
                        }
                        ForEach(Array(fills.enumerated()), id: \.offset) { _, fill in
                            ZStack {
                                fill.1
                                Rectangle().fill(DS.Color.onFill).frame(width: cell / 2, height: cell / 2)
                            }
                            .frame(width: cell, height: cell)
                        }
                    }
                }
            }
        }
        let size = CGSize(width: cell * CGFloat(colors.count + fills.count), height: cell * CGFloat(rows))
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let bitmap = try Self.draw(swatches, size: size, appearance: name)
            let scale = CGFloat(bitmap.pixelsWide) / size.width
            for row in 0..<rows {
                for column in 0..<(colors.count + fills.count) {
                    let origin = CGPoint(x: CGFloat(column) * cell * scale, y: CGFloat(row) * cell * scale)
                    let swatch = Self.pixel(bitmap, x: Int(origin.x + cell * scale / 2), y: Int(origin.y + cell * scale / 2))
                    let ground = Self.pixel(bitmap, x: Int(origin.x + 2), y: Int(origin.y + 2))
                    let ratio = DS.Terminal.contrast(swatch, ground)
                    let label = column >= colors.count ? "white on the \(fills[column - colors.count].0) fill" : colors[column].0
                    let base = bases[row / layers.count].0
                    let layer = layers[row % layers.count].0
                    if layer.hasSuffix("of another hue"), label != "neutral" { continue }
                    XCTAssertGreaterThanOrEqual(ratio, column >= colors.count ? 4.6 : 4.5,
                                                "\(label) on \(layer) over \(base) in \(name.rawValue): \(ratio)")
                }
            }
        }
    }

    /// White text on the selection fill and the chips of a selected row — the debugger's role
    /// badge, a SwiftUI chip and the AppKit tag of the tables — reach 4.5:1 against what is drawn
    /// behind them inside the chip: the brightest against the darkest pixel inside each chip,
    /// read from an offscreen sRGB window, in both appearances.
    @MainActor
    func testTextAndChipsOnSelectedRowsReachFourAndAHalfToOne() throws {
        struct Tag: NSViewRepresentable {
            func makeNSView(context: Context) -> TagView {
                let tag = TagView()
                tag.text = "Deskhand가 시작함"
                tag.tint = DS.Status.accentTextNS
                tag.emphasized = true
                return tag
            }
            func updateNSView(_ view: TagView, context: Context) {}
        }
        let renderer = DebugTarget(id: "p", role: DebugTargetRole.renderer.rawValue, type: "page", url: "app://a", title: "a")
        let samples: [(String, AnyView, CGSize)] = [
            ("role badge", AnyView(RoleBadge(target: renderer, inverted: true, fixedWidth: true)), CGSize(width: 48, height: 16)),
            ("chip", AnyView(Text("검사기 열림").font(.system(.caption, weight: .semibold)).foregroundStyle(DS.Color.onFill)
                .frame(width: 80, height: 16).chipBackground(DS.Color.accent, selected: true)), CGSize(width: 80, height: 16)),
            ("table tag", AnyView(Tag().frame(width: 104, height: 16)), CGSize(width: 104, height: 16)),
            ("secondary text", AnyView(Text("C:\\Program Files\\node.exe").font(.system(.subheadline, design: .monospaced))
                .foregroundStyle(DS.Color.onFill)), CGSize(width: 200, height: 16)),
        ]
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            for sample in samples {
                let size = CGSize(width: sample.2.width + 16, height: sample.2.height + 16)
                let drawn = try Self.draw(sample.1.frame(width: size.width, height: size.height).background(DS.Status.selectionFill),
                                          size: size, appearance: name)
                let scale = CGFloat(drawn.pixelsWide) / size.width
                let inset = (x: (8 + sample.2.width * 0.15) * scale, y: (8 + 4) * scale)
                var pixels: [NSColor] = []
                for y in stride(from: Int(inset.y), to: drawn.pixelsHigh - Int(inset.y), by: 1) {
                    for x in stride(from: Int(inset.x), to: drawn.pixelsWide - Int(inset.x), by: 1) {
                        pixels.append(Self.pixel(drawn, x: x, y: y))
                    }
                }
                let luminance = DS.Terminal.luminance
                let text = try XCTUnwrap(pixels.max { luminance($0) < luminance($1) })
                let ground = try XCTUnwrap(pixels.min { luminance($0) < luminance($1) })
                let ratio = DS.Terminal.contrast(text, ground)
                XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(sample.0) on the selection in \(name.rawValue): \(ratio)")
            }
        }
        XCTAssertTrue(DS.SelectedRow.isActive(nil))
        let offscreen = NSWindow(contentRect: .zero, styleMask: [.borderless], backing: .buffered, defer: true)
        XCTAssertTrue(DS.SelectedRow.isActive(offscreen), "a window that is not shown draws the active selection")
        XCTAssertEqual(DS.SelectedRow.fill(in: nil), DS.Status.selectionFillNS)
    }

    /// Each connection state's status dot on a selected sidebar row — increased background
    /// prominence over the accent selection — reaches 3:1 against the selection, in both appearances.
    @MainActor
    func testStatusDotOnASelectedSidebarRowReachesThreeToOne() throws {
        let size = CGSize(width: 24, height: 24)
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            for state in [ConnectionState.offline, .connecting, .ready, .degraded, .reconnecting, .blocked] {
                let dot = StatusDot(state: state)
                    .environment(\.backgroundProminence, .increased)
                    .frame(width: size.width, height: size.height)
                    .background(DS.Color.accent)
                let drawn = try Self.draw(dot, size: size, appearance: name)
                let scale = CGFloat(drawn.pixelsWide) / size.width
                let ground = Self.pixel(drawn, x: 1, y: 1)
                var best = 0.0
                for y in Int(8 * scale)..<Int(16 * scale) {
                    for x in Int(8 * scale)..<Int(16 * scale) {
                        best = max(best, DS.Terminal.contrast(Self.pixel(drawn, x: x, y: y), ground))
                    }
                }
                XCTAssertGreaterThanOrEqual(best, 3, "\(state) dot on the selected row in \(name.rawValue): \(best)")
            }
        }
    }

    /// Each Doctor status chip — its word and symbol in the status text color on the status tint —
    /// reaches 4.5:1 inside the chip over the window background, in both appearances.
    @MainActor
    func testDoctorStatusChipsReachFourAndAHalfToOne() throws {
        let size = CGSize(width: 96, height: 34)
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            for status in DoctorStatus.allCases {
                let chip = StatusChip(title: status.title, symbol: status.symbol, tint: status.tint, text: status.textColor)
                    .frame(width: 80, height: 18)
                    .frame(width: size.width, height: size.height)
                    .background(DS.Color.surface)
                let drawn = try Self.draw(chip, size: size, appearance: name)
                let scale = CGFloat(drawn.pixelsWide) / size.width
                var pixels: [NSColor] = []
                for y in Int(12 * scale)..<Int(22 * scale) {
                    for x in Int(16 * scale)..<Int(80 * scale) { pixels.append(Self.pixel(drawn, x: x, y: y)) }
                }
                let text = try XCTUnwrap(pixels.max { DS.Terminal.luminance($0) < DS.Terminal.luminance($1) })
                let ground = try XCTUnwrap(pixels.min { DS.Terminal.luminance($0) < DS.Terminal.luminance($1) })
                let ratio = DS.Terminal.contrast(text, ground)
                XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(status) chip in \(name.rawValue): \(ratio)")
            }
        }
    }

    /// The pixel at `x`, `y` of an sRGB bitmap as an sRGB color, read from its stored components.
    static func pixel(_ bitmap: NSBitmapImageRep, x: Int, y: Int) -> NSColor {
        var components = [Int](repeating: 0, count: max(4, bitmap.samplesPerPixel))
        bitmap.getPixel(&components, atX: x, y: y)
        return NSColor(srgbRed: CGFloat(components[0]) / 255, green: CGFloat(components[1]) / 255, blue: CGFloat(components[2]) / 255, alpha: 1)
    }

    /// `view` drawn at `size` in an offscreen sRGB window of `appearance`, as sRGB pixels.
    @MainActor
    static func draw(_ view: some View, size: CGSize, appearance: NSAppearance.Name) throws -> NSBitmapImageRep {
        NSApplication.shared.setActivationPolicy(.accessory)
        let window = NSWindow(contentRect: CGRect(origin: CGPoint(x: -32_000, y: -32_000), size: size), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.colorSpace = .sRGB
        window.appearance = NSAppearance(named: appearance)
        defer { window.contentView = nil }
        let host = NSHostingView(rootView: view.frame(width: size.width, height: size.height))
        host.frame = CGRect(origin: .zero, size: size)
        window.contentView = host
        host.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.05))
        host.layoutSubtreeIfNeeded()
        let cached = try XCTUnwrap(host.bitmapImageRepForCachingDisplay(in: host.bounds))
        host.cacheDisplay(in: host.bounds, to: cached)
        return try XCTUnwrap(cached.converting(to: .sRGB, renderingIntent: .default))
    }
}

/// sRGB components of a resolved color, for comparisons in tests.
struct SRGB {
    let r: Double
    let g: Double
    let b: Double

    init(_ color: Color) throws {
        try self.init(NSColor(color))
    }

    init(_ color: NSColor) throws {
        let srgb = try XCTUnwrap(color.usingColorSpace(.sRGB))
        r = srgb.redComponent
        g = srgb.greenComponent
        b = srgb.blueComponent
    }

    func distance(to other: SRGB) -> Double {
        ((r - other.r) * (r - other.r) + (g - other.g) * (g - other.g) + (b - other.b) * (b - other.b)).squareRoot()
    }
}
