import AppKit
@testable import DeskhandUI
import XCTest

@MainActor
final class WindowVisibilityTests: XCTestCase {
    func testAWindowThatIsNotOnScreenReportsItselfHiddenSoPollingPauses() async throws {
        NSApplication.shared.setActivationPolicy(.accessory)
        let window = NSWindow(contentRect: CGRect(x: -32_000, y: -32_000, width: 200, height: 100), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        let view = WindowVisibilityReader.VisibilityView()
        var reports: [Bool] = []
        view.onChange = { reports.append($0) }
        window.contentView = view
        try await waitUntil { !reports.isEmpty }
        XCTAssertEqual(reports, [false])
        window.contentView = nil
    }
}
