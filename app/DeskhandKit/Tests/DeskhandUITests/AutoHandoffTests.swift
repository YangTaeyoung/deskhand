import XCTest
@testable import DeskhandUI

final class AutoHandoffTests: XCTestCase {
    func testIdleStartsOnlyAfterLastInputAndNoButtonsAreHeld() {
        var p = AutoHandoffPolicy()
        p.activity(at: 1, holding: true)
        XCTAssertFalse(p.shouldRelease(at: 20))
        p.activity(at: 20, holding: false)
        XCTAssertFalse(p.shouldRelease(at: 24.99))
        XCTAssertTrue(p.shouldRelease(at: 25))
        p.activity(at: 24, holding: false)
        XCTAssertFalse(p.shouldRelease(at: 25))
        XCTAssertTrue(p.shouldRelease(at: 29))
    }
    func testRemainingReflectsLastInputAndHeldKeys() {
        var p = AutoHandoffPolicy()
        p.activity(at: 10, holding: false)
        XCTAssertEqual(p.remaining(at: 11), 4)
        XCTAssertEqual(p.remaining(at: 20), 0)
        p.activity(at: 20, holding: true)
        XCTAssertNil(p.remaining(at: 30))
    }
    func testResetPreventsAStaleTimerFromReleasing() {
        var p = AutoHandoffPolicy()
        p.activity(at: 1, holding: false)
        p.reset()
        XCTAssertFalse(p.shouldRelease(at: 100))
    }
}
