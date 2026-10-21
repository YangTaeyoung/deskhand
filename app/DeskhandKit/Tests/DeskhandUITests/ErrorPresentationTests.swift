import DeskhandClient
@testable import DeskhandUI
import XCTest

final class ErrorPresentationTests: XCTestCase {
    func testACallTheDaemonDidNotAnswerIsATimeoutWithADaemonRemedy() {
        let error = OperationError(ClientError.timeout(method: "host.list"))
        XCTAssertEqual(error.code, "TIMEOUT")
        XCTAssertEqual(error.message, "데몬이 제때 답하지 않았습니다")
        XCTAssertEqual(error.fix, "데몬이 바쁘거나 멈췄을 수 있습니다. 잠시 뒤 다시 시도하고, 계속되면 설정 › 일반에서 데몬을 다시 시작하세요.")
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
