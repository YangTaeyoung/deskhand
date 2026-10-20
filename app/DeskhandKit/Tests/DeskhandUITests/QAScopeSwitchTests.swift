import AppKit
import SwiftUI
import XCTest
@testable import DeskhandUI

/// Segments of the QA list scope are as wide as their own labels, and a long computer name is cut
/// at the name's maximum width.
@MainActor
final class QAScopeSwitchTests: XCTestCase {
    private func width(_ name: String) -> CGFloat {
        let view = NSHostingView(rootView: QAScopeSwitch(hostName: name, showsAllHosts: .constant(false)))
        return view.fittingSize.width
    }

    func testSegmentsFollowTheirLabelsAndALongNameIsCut() {
        let short = width("개발 PC")
        let medium = width("디자인 워크스테이션")
        let long = width("본사 7층 QA 랩 윈도우 11 테스트 워크스테이션 (아주 긴 이름)")
        XCTAssertGreaterThan(medium, short, "the name segment grows with its name")
        XCTAssertLessThan(short, 200, "short labels give a narrow switch")
        XCTAssertLessThanOrEqual(long - short, QAScopeSwitch.maximumNameWidth, "a long name stops at the maximum width")
        XCTAssertEqual(width("본사 7층 QA 랩 윈도우 11 테스트 워크스테이션 (아주 긴 이름) 그리고 더 길게"), long, accuracy: 0.5,
                       "past the maximum the width stays")
    }
}
