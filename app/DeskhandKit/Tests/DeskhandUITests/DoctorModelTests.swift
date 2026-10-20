import DeskhandClient
import XCTest
@testable import DeskhandUI

final class DoctorModelTests: XCTestCase {
    func testDoctorOrdersProblemsFirst() {
        let checks = ["pass", "fail", "skip", "warn", "pass", "fail"].enumerated().map {
            DoctorCheck(id: "c\($0.offset)", status: $0.element, detail: "")
        }
        XCTAssertEqual(DoctorModel.problemsFirst(checks).map(\.id), ["c1", "c5", "c3", "c0", "c2", "c4"])
    }

    func testFixFontHeuristic() {
        XCTAssertTrue(DoctorModel.looksLikeCommand(#"New-NetFirewallRule -DisplayName "Deskhand Agent""#))
        XCTAssertTrue(DoctorModel.looksLikeCommand("deskhand host repair 실험실 PC"))
        XCTAssertFalse(DoctorModel.looksLikeCommand("원격 컴퓨터에서 Windows 잠금을 해제하세요"))
    }
}
