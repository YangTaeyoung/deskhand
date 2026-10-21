import AppKit
@testable import DeskhandUI
import SwiftUI
import XCTest

final class TableStripeTests: XCTestCase {
    /// The files table stripes its odd rows and leaves the space below the last row unstriped,
    /// in both appearances.
    @MainActor
    func testFilesTableStripesRowsOnlyAndLeavesTheEmptySpacePlain() throws {
        let rows = [FileItem(name: "a.log", path: "/a.log", isDirectory: false, size: 1),
                    FileItem(name: "b.log", path: "/b.log", isDirectory: false, size: 1)]
        let table = FileTableView(side: .local, rows: rows, selection: [], sort: FileSort(), isActive: true, renameRequest: nil,
                                  widths: [:], hiddenColumns: [], accessibilityLabel: "files", handlers: FileTableHandlers())
        let size = CGSize(width: 320, height: 400)
        let row = DS.TableMetrics(.large).rowHeight
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let drawn = try TokensTests.draw(table, size: size, appearance: name)
            let scale = CGFloat(drawn.pixelsWide) / size.width
            func sample(_ y: CGFloat) -> NSColor { TokensTests.pixel(drawn, x: Int(size.width / 2 * scale), y: Int(y * scale)) }
            let bottom = size.height - row / 2
            XCTAssertEqual(sample(bottom), sample(bottom - row), "no stripe below the last row in \(name.rawValue)")
            XCTAssertEqual(sample(bottom - 2 * row), sample(bottom - 3 * row), "no stripe below the last row in \(name.rawValue)")
            let plain = sample(bottom)
            var runs: [(color: NSColor, length: Int)] = []
            for y in stride(from: CGFloat(0), to: size.height, by: 1) {
                let color = TokensTests.pixel(drawn, x: Int(9 * scale), y: Int(y * scale))
                if let last = runs.last, last.color == color { runs[runs.count - 1].length += 1 } else { runs.append((color, 1)) }
            }
            let bands = runs.indices.dropFirst().dropLast().filter { runs[$0 - 1].color == plain && runs[$0 + 1].color == plain }
            XCTAssertTrue(bands.contains { runs[$0].length >= Int(row) - 2 },
                          "the second row is a full-height stripe between plain rows in \(name.rawValue)")
        }
    }
}
