import AppKit
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

    func testContractCheckIDsHaveKoreanTitles() {
        for id in ["ssh_reachable", "agent_version", "quic_connect", "fingerprint", "session", "firewall_rule"] {
            let title = DoctorCheck(id: id, status: "pass", detail: "").title
            XCTAssertNotEqual(title, id)
            XCTAssertTrue(title.unicodeScalars.contains { (0xAC00...0xD7A3).contains($0.value) }, title)
        }
        XCTAssertEqual(DoctorCheck(id: "future_check", status: "pass", detail: "").title, "future_check")
    }

    func testPreviewDoctorUsesContractIDs() {
        let ids = Set(["ssh_reachable", "agent_version", "quic_connect", "fingerprint", "session", "firewall_rule"])
        for host in [PreviewFixtures.devPCID, PreviewFixtures.labID, PreviewFixtures.qaLaptopID] {
            for check in PreviewFixtures.doctor(hostID: host) {
                XCTAssertTrue(ids.contains(check.id), check.id)
            }
        }
    }

    func testScreenSymbolsExist() {
        let symbols = DoctorStatus.allCases.map(\.symbol) + [
            "pc", "desktopcomputer", "server.rack", "display", "stethoscope", "wrench.and.screwdriver.fill",
            "desktopcomputer.trianglebadge.exclamationmark", "moon.zzz.fill", "wifi.exclamationmark",
            "arrow.triangle.2.circlepath", "bolt.horizontal", "bolt.horizontal.circle", "doc.text.magnifyingglass",
            "doc.on.doc", "sidebar.left", "plus.circle", "info.circle", "gearshape.fill", "ellipsis", "trash",
            "exclamationmark.triangle.fill", "checkmark",
        ]
        for symbol in symbols {
            XCTAssertNotNil(NSImage(systemSymbolName: symbol, accessibilityDescription: nil), symbol)
        }
    }
}
