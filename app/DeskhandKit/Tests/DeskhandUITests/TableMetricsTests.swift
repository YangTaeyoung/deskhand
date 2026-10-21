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

    func testColumnWidthsScaleWithTheTextSizeAndSaveUnderTheirSize() {
        let large = DS.TableMetrics(.large)
        XCTAssertEqual(large.columnWidth(78), 78)
        XCTAssertEqual(large.autosaveName("processes"), "processes")
        let bigger = DS.TableMetrics(.xxxLarge)
        XCTAssertEqual(bigger.columnWidth(78), 101)
        XCTAssertEqual(bigger.baseWidth(bigger.columnWidth(120)), 120)
        XCTAssertEqual(bigger.autosaveName("processes"), "processes@130")
        XCTAssertEqual(DS.TableMetrics(.small).columnWidth(100), 90)
    }

    @MainActor
    func testATableAtALargerTextSizeWidensItsColumns() throws {
        struct Row: Identifiable, Equatable { let id: Int }
        func table(_ size: DynamicTypeSize) throws -> NSTableView {
            let view = NSHostingView(rootView: DataTable<Row>(
                columns: [DataTableColumn(id: "name", title: "이름", width: 200, minWidth: 100),
                          DataTableColumn(id: "memory", title: "메모리", width: 78, minWidth: 60),
                          DataTableColumn(id: "path", title: "경로", width: 200, minWidth: 120, fills: true)],
                rows: [Row(id: 1)], selection: nil, sort: nil, accessibilityLabel: "표", cell: { _, _, _ in NSView() })
                .environment(\.dynamicTypeSize, size))
            view.frame = NSRect(x: 0, y: 0, width: 1_200, height: 300)
            view.layoutSubtreeIfNeeded()
            return try XCTUnwrap(Self.find(NSTableView.self, in: view))
        }
        let regular = try table(.large)
        let larger = try table(.xxxLarge)
        func column(_ table: NSTableView, _ id: String) throws -> NSTableColumn {
            try XCTUnwrap(table.tableColumn(withIdentifier: NSUserInterfaceItemIdentifier(id)))
        }
        XCTAssertEqual(try column(regular, "memory").width, 78)
        XCTAssertEqual(try column(larger, "memory").width, 101)
        XCTAssertEqual(try column(larger, "memory").minWidth, 78)
        XCTAssertEqual(try column(larger, "name").minWidth, 130)
    }

    @MainActor
    private static func find<T: NSView>(_ type: T.Type, in view: NSView) -> T? {
        if let match = view as? T { return match }
        for child in view.subviews { if let match = find(type, in: child) { return match } }
        return nil
    }

    func testFileColumnsScaleTheirSavedWidthsAndSaveThemAtTheDefaultSize() {
        let bigger = DS.TableMetrics(.xxxLarge)
        XCTAssertEqual(FileTableView.width(.size, saved: nil, metrics: bigger), bigger.columnWidth(FileColumn.size.defaultWidth))
        XCTAssertEqual(FileTableView.width(.size, saved: 100, metrics: bigger), 130)
        XCTAssertEqual(FileTableView.savedWidth(130, metrics: bigger), 100)
        XCTAssertEqual(FileTableView.width(.kind, saved: 90, metrics: DS.TableMetrics(.large)), 90)
    }
}
