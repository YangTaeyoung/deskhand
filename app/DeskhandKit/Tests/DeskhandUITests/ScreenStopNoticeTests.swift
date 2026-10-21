import DeskhandClient
import DeskhandMedia
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

@MainActor
final class ScreenStopNoticeTests: XCTestCase {
    private let dev = PreviewFixtures.hosts().first { $0.hostID == PreviewFixtures.devPCID }!
    private let reasons: [PauseReason] = [
        .noViewer, .session0, .rdpOutputSuppressed, .secureDesktop, .sessionLocked, .sessionDisconnected,
        .accessLost, .noOutput, .noEncoder, .other("capture_failed"),
    ]

    /// The header's quality, keyboard, statistics and display controls work while the PC is
    /// connected or reconnecting, and not while it is offline, blocked or still connecting.
    func testStreamSettingsNeedAConnectedPC() {
        var host = dev
        for state in ConnectionState.allCases {
            host.state = state.rawValue
            let expected = [ConnectionState.ready, .degraded, .reconnecting].contains(state)
            XCTAssertEqual(ScreenAvailability(host: host).allowsStreamSettings, expected, "\(state)")
        }
        host.state = ConnectionState.ready.rawValue
        host.session?.locked = true
        XCTAssertTrue(ScreenAvailability(host: host).allowsStreamSettings, "a locked session keeps its connection")
    }

    func testEveryReasonExplainsItselfWithoutTheRawCode() {
        for reason in reasons {
            let notice = ScreenStopNotice.paused(reason, host: dev, display: 0)
            XCTAssertFalse(notice.title.isEmpty, reason.rawValue)
            XCTAssertFalse(notice.message.isEmpty, reason.rawValue)
            XCTAssertFalse(notice.title.contains(reason.rawValue), reason.rawValue)
            XCTAssertFalse(notice.message.contains(reason.rawValue), reason.rawValue)
            XCTAssertEqual(notice.code, reason.rawValue)
            XCTAssertEqual(notice.codeLabel, "사유 코드")
        }
    }

    func testAFailedAttachJoinsTheMessageAndTheFixAsSentencesWithRetry() {
        let error = OperationError(code: "OFFLINE", message: "개발 PC에 연결되어 있지 않습니다")
        let notice = ScreenStopNotice.failed(error)
        XCTAssertEqual(notice.message, "개발 PC에 연결되어 있지 않습니다. PC가 켜져 있고 같은 네트워크나 VPN에 있는지 확인하세요.")
        XCTAssertEqual(notice.actions, [.retry("다시 시도")])
        XCTAssertEqual(notice.codeLabel, "오류 코드")
        XCTAssertEqual(notice.code, "OFFLINE")
        XCTAssertFalse(ScreenStopNotice.isUnsupported(error))
        XCTAssertTrue(ScreenStopNotice.isUnsupported(OperationError(code: "CAPABILITY_UNSUPPORTED", message: "화면 스트림은 아직 지원하지 않습니다")))
    }

    func testDisconnectedAndSuppressedSessionsOfferTheConsoleCommand() {
        let session = dev.session!.sessionID
        for reason in [PauseReason.sessionDisconnected, .rdpOutputSuppressed] {
            XCTAssertEqual(ScreenStopNotice.paused(reason, host: dev, display: 0).command, "tscon \(session) /dest:console")
        }
        XCTAssertNil(ScreenStopNotice.paused(.secureDesktop, host: dev, display: 0).command)
    }

    func testEachReasonOffersTheActionThatFixesIt() {
        XCTAssertEqual(ScreenStopNotice.paused(.session0, host: dev, display: 0).actions, [.diagnose])
        XCTAssertEqual(ScreenStopNotice.paused(.noEncoder, host: dev, display: 0).actions, [.retry("다시 시도")])
        XCTAssertEqual(ScreenStopNotice.paused(.accessLost, host: dev, display: 0).actions, [])
        XCTAssertEqual(ScreenStopNotice.paused(.secureDesktop, host: dev, display: 0).actions, [])
        XCTAssertEqual(ScreenStopNotice.paused(.noOutput, host: dev, display: 1).actions,
                       [.showDisplay(index: 0, title: "디스플레이 1"), .retry("다시 시도")])
    }

    func testMissingDisplayOffersTheOtherDisplays() {
        let error = OperationError(code: "DISPLAY_UNAVAILABLE", message: "요청한 디스플레이가 이 PC에 없습니다")
        let notice = ScreenStopNotice.displayUnavailable(error, host: dev, display: 1)
        XCTAssertEqual(notice.actions, [.showDisplay(index: 0, title: "디스플레이 1"), .retry("다시 시도")])
        XCTAssertEqual(notice.codeLabel, "오류 코드")
        XCTAssertEqual(notice.code, "DISPLAY_UNAVAILABLE")
        XCTAssertTrue(notice.message.contains("디스플레이 2"))
    }
}
