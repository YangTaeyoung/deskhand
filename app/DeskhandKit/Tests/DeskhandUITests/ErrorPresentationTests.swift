import DeskhandClient
@testable import DeskhandUI
import XCTest

final class ErrorPresentationTests: XCTestCase {
    @MainActor
    func testACallTheDaemonDidNotAnswerIsATimeoutWithADaemonRemedy() {
        let error = OperationError(ClientError.timeout(method: "host.list"))
        XCTAssertEqual(error.code, "TIMEOUT")
        XCTAssertEqual(error.message, "이 Mac의 Deskhand 데몬이 제때 답하지 않았습니다")
        XCTAssertEqual(error.fix, "데몬이 바쁘거나 멈췄을 수 있습니다. 잠시 뒤 다시 시도하고, 계속되면 설정 › 일반에서 데몬을 다시 시작하세요.")
        XCTAssertEqual(OperationError(ClientError.daemonUnavailable(logPath: "/tmp/d.log")).message, "이 Mac의 Deskhand 데몬이 답하지 않습니다")
        XCTAssertEqual(AppStore.failure(ClientError.daemonUnavailable(logPath: "/tmp/d.log")).message,
                       "이 Mac의 Deskhand 데몬이 5초 안에 답하지 않았습니다")
    }

    /// The detail line of a failure: the daemon's message, then the remedy of
    /// its code, and the message alone for a code without one.
    func testAListFailureKeepsTheDaemonMessageAndTheRemedyOfItsCode() {
        XCTAssertEqual(OperationError(code: "CAPABILITY_UNSUPPORTED", message: "에이전트가 debug.list를 지원하지 않습니다").sentences,
                       "에이전트가 debug.list를 지원하지 않습니다. 원격 PC의 에이전트를 최신 버전으로 업데이트한 뒤 다시 시도하세요.")
        XCTAssertEqual(OperationError(code: "INTERNAL", message: "디버그 기록을 열지 못했습니다").sentences, "디버그 기록을 열지 못했습니다.")
        XCTAssertEqual(OperationError(ClientError.timeout(method: "qa.list")).sentences,
                       "이 Mac의 Deskhand 데몬이 제때 답하지 않았습니다. 데몬이 바쁘거나 멈췄을 수 있습니다. 잠시 뒤 다시 시도하고, 계속되면 설정 › 일반에서 데몬을 다시 시작하세요.")
        XCTAssertEqual(OperationError(code: "OFFLINE", message: "PC가 오프라인입니다").sentences,
                       "PC가 오프라인입니다. PC가 켜져 있고 같은 네트워크나 VPN에 있는지 확인하세요.")
    }

    func testOnlyCodesThatNeedAChangeSkipTheSameInputRetry() {
        for code in ["ELEVATION_REQUIRED", "PERMISSION_DENIED", "CAPABILITY_UNSUPPORTED"] {
            XCTAssertTrue(OperationError(code: code, message: "x").needsChangeBeforeRetry, code)
        }
        for code in ["OFFLINE", "TIMEOUT", "INTERNAL", "NOT_FOUND"] {
            XCTAssertFalse(OperationError(code: code, message: "x").needsChangeBeforeRetry, code)
        }
        XCTAssertFalse(OperationError(message: "x").needsChangeBeforeRetry)
    }

    func testATimeoutTheDaemonReportsPointsAtThePC() {
        let error = OperationError(RPCErrorInfo(message: "에이전트가 10초 안에 답하지 않았습니다", code: "TIMEOUT"))
        XCTAssertEqual(error.fix, "PC가 바쁘거나 연결이 느릴 수 있습니다. 잠시 뒤 다시 시도하고, 계속되면 진단을 실행하세요.")
    }

    func testSentencesEndEachPartWithPunctuationAndSkipEmptyParts() {
        XCTAssertEqual(String.sentences("PC가 응답하지 않습니다", nil, "  ", "다시 시도하세요."), "PC가 응답하지 않습니다. 다시 시도하세요.")
        XCTAssertEqual(String.sentences("끝났나요?", "확인하세요"), "끝났나요? 확인하세요.")
        XCTAssertEqual(OperationError(code: "NOT_FOUND", message: "파일이 없습니다").sentences,
                       "파일이 없습니다. 목록을 새로 고친 뒤 다시 시도하세요.")
    }
}
