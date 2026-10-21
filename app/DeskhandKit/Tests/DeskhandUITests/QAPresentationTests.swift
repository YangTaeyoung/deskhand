import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// Wording of cancelled runs and blocked steps, step counts with cancel-stopped steps, units,
/// JSON layout of UI snapshots, and highlights of failed text assertions.
final class QAPresentationTests: XCTestCase {
    private let start = Timestamp(date: Date(timeIntervalSince1970: 1_800_000_000))

    private func step(_ index: Int, _ phase: QAStepPhase, _ status: QAStepStatus, action: String = "assert_ui", code: String? = nil) -> QAStep {
        QAStep(index: index, phase: phase, name: "단계 \(index)", action: action, status: status,
               error: code.map { QAErrorInfo(code: $0, message: "데몬 문장") })
    }

    private func cancelled(_ steps: [QAStep]) -> QARun {
        QARun(runID: "q-1", jobID: "j-1", hostID: "h", name: "n", status: .cancelled, rev: 9, startedAt: start, stepsTotal: steps.count,
              cancelRequested: true, steps: steps)
    }

    // MARK: Cancelled runs

    func testCancelledRunWhoseTeardownRanSaysItRanToTheEnd() {
        let steps = [step(1, .setup, .pass), step(2, .steps, .blocked), step(3, .steps, .notRun), step(4, .teardown, .pass)]
        XCTAssertEqual(QARunVerdict(cancelled(steps), steps: steps).detail,
                       "진행 중이던 단계를 멈추고 남은 단계를 건너뛴 뒤 정리 단계를 끝까지 실행했습니다.")
    }

    func testCancelledRunWhoseTeardownWasStoppedSaysSo() {
        let steps = [step(1, .setup, .pass), step(2, .steps, .blocked), step(3, .teardown, .blocked), step(4, .teardown, .notRun)]
        XCTAssertEqual(QARunVerdict(cancelled(steps), steps: steps).detail,
                       "진행 중이던 단계를 멈추고 남은 단계를 건너뛰었고, 정리 단계도 멈췄습니다. PC에 앱이나 파일이 남았는지 확인하세요.")
        let teardownOnly = [step(1, .steps, .pass), step(2, .teardown, .blocked)]
        XCTAssertEqual(QARunVerdict(cancelled(teardownOnly), steps: teardownOnly).detail,
                       "준비·본 단계는 모두 실행했고, 정리 단계 중에 취소해 정리 단계를 끝까지 실행하지 않았습니다. PC에 앱이나 파일이 남았는지 확인하세요.")
    }

    func testCancelledRunWithoutStepsOrTeardownMakesNoTeardownClaim() {
        XCTAssertEqual(QARunVerdict(cancelled([])).detail, "진행 중이던 단계를 멈추고 남은 단계를 건너뛰었습니다.")
        let noTeardown = [step(1, .steps, .blocked), step(2, .steps, .notRun)]
        XCTAssertEqual(QARunVerdict(cancelled(noTeardown), steps: noTeardown).detail, "진행 중이던 단계를 멈추고 남은 단계를 건너뛰었습니다.")
    }

    // MARK: Blocked steps

    func testInputStepBlockedSaysTheDaemonCannotSendInputYet() {
        let reason = QAFormat.blockedReason(step(1, .steps, .blocked, action: "click", code: "CAPABILITY_UNSUPPORTED"))
        XCTAssertEqual(reason.title, "Deskhand 데몬이 아직 마우스 입력을 보내지 못해 실행하지 않았습니다")
        XCTAssertEqual(reason.next, "ui_act(invoke/set_value)로 바꿔 쓰세요.")
        XCTAssertEqual(QAFormat.blockedReason(step(1, .steps, .blocked, action: "type", code: "CAPABILITY_UNSUPPORTED")).title,
                       "Deskhand 데몬이 아직 키보드 입력을 보내지 못해 실행하지 않았습니다")
        XCTAssertFalse(reason.title.contains("이 PC"))
    }

    func testStepBlockedByAnUnreadyCapabilitySaysItIsUnavailableNow() {
        let reason = QAFormat.blockedReason(step(1, .steps, .blocked, action: "assert_ui", code: "CAPABILITY_UNSUPPORTED"))
        XCTAssertEqual(reason.title, "이 PC의 UI 자동화 기능을 지금 쓸 수 없어 실행하지 않았습니다")
        XCTAssertEqual(reason.next, "PC의 UI 자동화 상태를 고친 뒤 다시 실행하세요.")
    }

    func testEveryBlockedReasonGivesANextActionAndNeverSaysSkipped() {
        let codes: [String?] = [nil, "CONTROL_HELD_BY_HUMAN", "CONTROL_PREEMPTED", "CAPABILITY_UNSUPPORTED", "OFFLINE", "AUTH_REQUIRED",
                                "SESSION_LOCKED", "DISPLAY_UNAVAILABLE", "ELEVATION_REQUIRED", "EXECUTION_OUTCOME_UNKNOWN", "SOMETHING_NEW"]
        for code in codes {
            let reason = QAFormat.blockedReason(step(1, .steps, .blocked, code: code))
            XCTAssertNotNil(reason.next, String(describing: code))
            XCTAssertFalse(reason.title.contains("건너뛰"), String(describing: code))
        }
        XCTAssertEqual(QAFormat.blockedReason(step(1, .steps, .blocked, code: "SOMETHING_NEW")).title, "실행하지 못했습니다")
    }

    // MARK: Counts

    func testCancelStoppedStepsAreCountedApartFromBlockedOnes() {
        let steps = [step(1, .setup, .pass), step(2, .steps, .blocked, code: "CONTROL_HELD_BY_HUMAN"), step(3, .steps, .blocked),
                     step(4, .steps, .notRun), step(5, .teardown, .fail)]
        XCTAssertTrue(QAFormat.isCancelStopped(steps[2]))
        XCTAssertFalse(QAFormat.isCancelStopped(steps[1]))
        let counts = QAStepCounts(run: cancelled(steps), steps: steps)
        XCTAssertEqual(counts, QAStepCounts(passed: 1, failed: 1, blocked: 1, cancelStopped: 1, notRun: 1))
        let summary = QARun(runID: "q-2", jobID: "j", hostID: "h", name: "n", status: .blocked, rev: 3, startedAt: start, stepsPassed: 2,
                            stepsFailed: 0, stepsBlocked: 2, stepsNotRun: 1)
        XCTAssertEqual(QAStepCounts(run: summary, steps: nil), QAStepCounts(passed: 2, failed: 0, blocked: 2, cancelStopped: 0, notRun: 1))
    }

    func testNotRunMessagesOfThePreviewFollowTheDaemonsWording() {
        XCTAssertEqual(PreviewQAFixtures.notRunMessage(index: 7, name: "대답이 끝날 때까지 기다리기", status: .fail),
                       "7단계(대답이 끝날 때까지 기다리기)의 결과(실패) 때문에 실행하지 않았습니다")
        XCTAssertEqual(PreviewQAFixtures.notRunMessage(index: 4, name: "설정 열기", status: .blocked),
                       "4단계(설정 열기)의 결과(차단) 때문에 실행하지 않았습니다")
        XCTAssertEqual(PreviewQAFixtures.notRunMessage(index: 2, name: "a", status: .notRun), "2단계(a)의 결과(실행 안 함) 때문에 실행하지 않았습니다")
        XCTAssertEqual(PreviewQAFixtures.notRunMessage(index: 2, name: "a", status: .pass), "2단계(a)의 결과(통과) 때문에 실행하지 않았습니다")
    }

    func testPreviewMessagesWriteSizesWithTheAppsFormatter() {
        let push = PreviewQAFixtures.Spec(phase: .setup, name: "올리기", action: "push", line: 1, params: [:], ms: 0)
        XCTAssertEqual(PreviewQAFixtures.defaultMessage(push, .pass), "파일 1개(2.4 MB)를 보냈습니다")
        let collect = PreviewQAFixtures.Spec(phase: .steps, name: "모으기", action: "collect", line: 1, params: [:], ms: 0)
        XCTAssertEqual(PreviewQAFixtures.defaultMessage(collect, .pass), "C:\\deskhand-qa\\fixture.log의 2.1 KB를 모았습니다")
    }

    // MARK: JSON

    func testPrettyJSONKeepsTheDaemonsKeyOrderWithStandardSpacing() throws {
        let text = #"{"window":{"id":"0x2A","title":"a {b} \"c\""},"nodes":[{"role":"edit","children":[]},{}],"truncated":false}"#
        let pretty = try XCTUnwrap(QAFormat.prettyJSON(text))
        XCTAssertEqual(pretty, """
            {
              "window": {
                "id": "0x2A",
                "title": "a {b} \\"c\\""
              },
              "nodes": [
                {
                  "role": "edit",
                  "children": []
                },
                {}
              ],
              "truncated": false
            }
            """)
        XCTAssertNil(QAFormat.prettyJSON("{not json"))
    }

    // MARK: Assertions

    func testFailedExactTextHighlightsFromTheFirstDifference() {
        let actual = "Bot: echo 안녕 Q"
        let ranges = QAFormat.highlights(kind: "text", expected: .string("Bot: echo 안녕 QA"), actual: .string(actual), passed: false)
        XCTAssertEqual(ranges.map { String(actual[$0]) }, [])
        let other = "Bot: echo 하이"
        let changed = QAFormat.highlights(kind: "text", expected: .string("Bot: echo 안녕"), actual: .string(other), passed: false)
        XCTAssertEqual(changed.map { String(other[$0]) }, ["하이"])
        XCTAssertEqual(QAFormat.highlights(kind: "text", expected: .string("a"), actual: .string("b"), passed: true), [])
    }

    func testFailedNotContainsHighlightsEveryOccurrence() {
        let actual = "error: disk\nwarning\nerror: net"
        let ranges = QAFormat.highlights(kind: "not_contains", expected: .string("error"), actual: .string(actual), passed: false)
        XCTAssertEqual(ranges.count, 2)
        XCTAssertTrue(ranges.allSatisfy { actual[$0] == "error" })
        XCTAssertEqual(QAFormat.expectedText(.string("error"), kind: "not_contains"), "없어야 함: error")
    }

    // MARK: Step list

    func testStepListPutsContextAboveTheFocusedStepAndKeepsAShortListAtItsTop() {
        let steps = (1...12).map { step($0, $0 <= 2 ? .setup : ($0 == 12 ? .teardown : .steps), .pass) }
        let groups: [(phase: QAStepPhase, steps: [QAStep])] = QAStepPhase.allCases.map { phase in (phase, steps.filter { $0.stepPhase == phase }) }
        let height = StepList.headerHeight + StepList.rowHeight(steps[0]) * 7
        XCTAssertEqual(StepList.scrollTarget(for: 4, in: groups, height: height), StepList.rowID(2), "the step's phase title shows above it")
        XCTAssertEqual(StepList.scrollTarget(for: 10, in: groups, height: height), StepList.rowID(7))
        XCTAssertEqual(StepList.scrollTarget(for: 12, in: groups, height: height), StepList.rowID(10))
        XCTAssertEqual(StepList.scrollTarget(for: 3, in: groups, height: height), StepList.rowID(1))
        XCTAssertEqual(StepList.scrollTarget(for: 6, in: groups, height: height), StepList.headerID(.steps), "a close phase title starts the view")
        XCTAssertNil(StepList.scrollTarget(for: 99, in: groups, height: height))
        XCTAssertEqual(StepList.scrollTarget(for: 10, in: groups, height: StepList.contentHeight(groups)), StepList.headerID(.setup),
                       "a list that fits stays at its top")
    }
}
