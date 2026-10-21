import AppKit
import SwiftUI
@testable import DeskhandUI
import XCTest

final class TableMetricsTests: XCTestCase {
    func testTheDefaultTextSizeKeeps13PointTextIn24PointRows() {
        let metrics = DS.TableMetrics(.large)
        XCTAssertEqual(metrics.textSize, NSFont.systemFontSize)
        XCTAssertEqual(metrics.smallTextSize, NSFont.smallSystemFontSize)
        XCTAssertEqual(metrics.rowHeight, 24)
    }

    func testLargerTextSizesGrowTheTextAndTheRowsOnTheGrid() {
        var previous = DS.TableMetrics(.large)
        for size in [DynamicTypeSize.xLarge, .xxLarge, .xxxLarge, .accessibility1, .accessibility3, .accessibility5] {
            let metrics = DS.TableMetrics(size)
            XCTAssertGreaterThan(metrics.textSize, previous.textSize, "\(size)")
            XCTAssertGreaterThanOrEqual(metrics.rowHeight, previous.rowHeight, "\(size)")
            XCTAssertEqual(metrics.rowHeight.truncatingRemainder(dividingBy: 4), 0, "\(size)")
            XCTAssertGreaterThanOrEqual(metrics.rowHeight, metrics.textSize * 1.6, "\(size)")
            previous = metrics
        }
        XCTAssertLessThan(DS.TableMetrics(.small).textSize, NSFont.systemFontSize)
        XCTAssertEqual(DS.TableMetrics(.small).rowHeight, 24, "rows never shrink below 24 pt")
    }
}
