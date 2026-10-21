@testable import DeskhandUI
import XCTest

@MainActor
final class MenuCommandTests: XCTestCase {
    func testFindAndRefreshApplyToTheTabsThatHaveThem() {
        XCTAssertEqual(WorkspaceTab.allCases.filter(WorkspaceLayout.canFind), [.files, .processes, .logs, .debugger, .qa])
        XCTAssertEqual(WorkspaceTab.allCases.filter(WorkspaceLayout.canRefresh), [.files, .processes, .logs, .debugger])
    }

    func testEachFindRequestIsNewAndRecentForHalfASecond() {
        let layout = WorkspaceLayout(defaults: UserDefaults(suiteName: "menu-\(UUID())")!)
        let before = layout.findRequest
        XCTAssertFalse(before.isRecent())
        layout.requestFind()
        XCTAssertNotEqual(layout.findRequest, before)
        XCTAssertTrue(layout.findRequest.isRecent())
        XCTAssertFalse(layout.findRequest.isRecent(now: .now.addingTimeInterval(1)))
        let refresh = layout.refreshRequests
        layout.requestRefresh()
        XCTAssertEqual(layout.refreshRequests, refresh + 1)
    }
}
