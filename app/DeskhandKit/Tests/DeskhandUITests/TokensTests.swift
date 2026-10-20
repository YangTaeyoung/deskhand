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
