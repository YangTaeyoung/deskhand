@testable import DeskhandUI
import XCTest

@MainActor
final class InspectorAutoHideTests: XCTestCase {
    private func layout() -> WorkspaceLayout {
        WorkspaceLayout(defaults: UserDefaults(suiteName: "inspector-\(UUID())")!)
    }

    func testANarrowWorkspaceHidesTheInspectorAndAWideOneBringsItBack() {
        let layout = layout()
        layout.showsInspector = true
        layout.fit(width: 1200)
        XCTAssertTrue(layout.isInspectorVisible)
        layout.fit(width: 900)
        XCTAssertFalse(layout.isInspectorVisible, "content would be 580 pt beside a 320 pt inspector")
        XCTAssertTrue(layout.showsInspector, "the person's choice is kept")
        layout.fit(width: 980)
        XCTAssertFalse(layout.isInspectorVisible, "it comes back only past the margin")
        layout.fit(width: 1000)
        XCTAssertTrue(layout.isInspectorVisible)
    }

    func testShowingTheInspectorWhileNarrowOverridesTheAutoHide() {
        let layout = layout()
        layout.showsInspector = true
        layout.fit(width: 800)
        XCTAssertFalse(layout.isInspectorVisible)
        layout.toggleInspectorVisibility()
        XCTAssertTrue(layout.isInspectorVisible)
        layout.toggleInspectorVisibility()
        XCTAssertFalse(layout.isInspectorVisible)
        XCTAssertFalse(layout.showsInspector)
    }

    func testAHiddenInspectorStaysHidden() {
        let layout = layout()
        layout.showsInspector = false
        layout.fit(width: 1600)
        XCTAssertFalse(layout.isInspectorVisible)
        XCTAssertFalse(layout.inspectorAutoHidden)
    }
}
