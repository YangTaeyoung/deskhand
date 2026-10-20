import AppKit
import XCTest
@testable import DeskhandUI

final class PresentationTests: XCTestCase {
    func testRawValuesMatchDaemonStrings() {
        XCTAssertEqual(ConnectionState.allCases.map(\.rawValue),
                       ["offline", "connecting", "ready", "degraded", "reconnecting", "blocked"])
        XCTAssertEqual(CapabilityState.allCases.map(\.rawValue),
                       ["ready", "unavailable", "unsupported", "needs_user"])
        XCTAssertEqual(ControlOwner.allCases.map(\.rawValue), ["none", "human", "ai"])
    }

    func testDecodesDaemonStrings() throws {
        let decoded = try JSONDecoder().decode([CapabilityState].self, from: Data(#"["needs_user","ready"]"#.utf8))
        XCTAssertEqual(decoded, [.needsUser, .ready])
    }

    func testEveryStateHasKoreanTitle() {
        let titles = ConnectionState.allCases.map(\.title)
            + CapabilityState.allCases.map(\.title)
            + ControlOwner.allCases.map(\.title)
            + StepStatus.allCases.map(\.title)
        for title in titles {
            XCTAssertTrue(title.unicodeScalars.contains { (0xAC00...0xD7A3).contains($0.value) }, title)
        }
        XCTAssertEqual(Set(ConnectionState.allCases.map(\.title)).count, ConnectionState.allCases.count)
        XCTAssertEqual(Set(CapabilityState.allCases.map(\.title)).count, CapabilityState.allCases.count)
        XCTAssertEqual(Set(ControlOwner.allCases.map(\.title)).count, ControlOwner.allCases.count)
    }

    func testSymbolsExist() {
        let symbols = CapabilityState.allCases.map(\.symbol)
            + ControlOwner.allCases.map(\.symbol)
            + StepStatus.allCases.compactMap(\.symbol)
            + ErrorBanner.symbols
        for symbol in symbols {
            XCTAssertNotNil(NSImage(systemSymbolName: symbol, accessibilityDescription: nil), symbol)
        }
    }

    func testControlPillLabel() {
        XCTAssertEqual(ControlPill.label(owner: .ai, clientName: "Claude Code"), "AI 조작 중 · Claude Code")
        XCTAssertEqual(ControlPill.label(owner: .human, clientName: nil), "내가 조작 중")
        XCTAssertEqual(ControlPill.label(owner: .none, clientName: "Claude Code"), "관찰 중")
    }
}
