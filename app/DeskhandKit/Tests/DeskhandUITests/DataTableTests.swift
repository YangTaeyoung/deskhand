import XCTest
@testable import DeskhandUI

final class DataTableTests: XCTestCase {
    func testFirstColumnShrinksOnlyAsFarAsNeededAndGrowsBackToItsPreferredWidth() {
        let width = FirstColumnWidth(preferred: 380, minimum: 140)
        XCTAssertEqual(width.width(available: 600), 380)
        XCTAssertEqual(width.width(available: 214), 214)
        XCTAssertEqual(width.width(available: 100), 140)
        XCTAssertEqual(width.width(available: 380), 380)
        XCTAssertEqual(width.width(available: 1_000), 380)
    }

    func testADragSetsThePreferredWidthAtLeastTheMinimum() {
        var width = FirstColumnWidth(preferred: 380, minimum: 140)
        width.userResized(to: 300)
        XCTAssertEqual(width.preferred, 300)
        XCTAssertEqual(width.width(available: 600), 300)
        XCTAssertEqual(width.width(available: 250), 250)
        width.userResized(to: 60)
        XCTAssertEqual(width.preferred, 140)
        width.userResized(to: 520)
        XCTAssertEqual(width.width(available: 900), 520)
        XCTAssertEqual(width.width(available: 400), 400)
    }
}
