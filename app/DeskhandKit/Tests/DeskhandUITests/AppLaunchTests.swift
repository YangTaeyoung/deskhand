import XCTest
@testable import DeskhandUI

final class AppLaunchTests: XCTestCase {
    func testPreviewScenarioFromArgumentsAndEnvironment() {
        XCTAssertNil(AppLaunch.previewScenario(arguments: ["Deskhand"], environment: [:]))
        XCTAssertEqual(AppLaunch.previewScenario(arguments: ["Deskhand", "--preview"], environment: [:]), .standard)
        XCTAssertEqual(AppLaunch.previewScenario(arguments: ["Deskhand", "--preview", "empty"], environment: [:]), .empty)
        XCTAssertEqual(AppLaunch.previewScenario(arguments: ["Deskhand", "--preview=daemon-unavailable"], environment: [:]), .daemonUnavailable)
        XCTAssertEqual(AppLaunch.previewScenario(arguments: ["Deskhand", "--preview", "-NSDocumentRevisionsDebugMode"], environment: [:]), .standard)
        XCTAssertEqual(AppLaunch.previewScenario(arguments: ["Deskhand"], environment: ["DESKHAND_PREVIEW": "1"]), .standard)
        XCTAssertEqual(AppLaunch.previewScenario(arguments: ["Deskhand"], environment: ["DESKHAND_PREVIEW": "empty"]), .empty)
        XCTAssertNil(AppLaunch.previewScenario(arguments: ["Deskhand"], environment: ["DESKHAND_PREVIEW": "0"]))
    }
}
