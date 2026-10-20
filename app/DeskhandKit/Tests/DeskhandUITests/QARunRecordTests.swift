import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// Run and step merging by rev and index, run summaries, the cancel button states, variables and
/// scenario errors.
final class QARunRecordTests: XCTestCase {
    private let start = Timestamp(date: Date(timeIntervalSince1970: 1_800_000_000))

    private func run(rev: Int, status: QARunStatus = .running, current: Int? = nil, cancel: Bool = false, passed: Int = 0, failed: Int = 0,
                     blocked: Int = 0, notRun: Int = 0, total: Int = 4, error: QAErrorInfo? = nil, steps: [QAStep]? = nil) -> QARun {
        QARun(runID: "q-1", jobID: "j-1", hostID: "windows-dev", name: "fixture-smoke", scenarioPath: "/Users/qa/fixture-smoke.yaml",
              status: status, rev: rev, startedAt: start, stepsTotal: total, stepsPassed: passed, stepsFailed: failed, stepsBlocked: blocked,
              stepsNotRun: notRun, currentStep: current, cancelRequested: cancel, error: error, steps: steps)
    }

    private func step(_ index: Int, _ status: QAStepStatus, phase: QAStepPhase = .steps, name: String? = nil) -> QAStep {
        QAStep(index: index, phase: phase, name: name ?? "단계 \(index)", action: "assert_ui", line: index * 3, status: status)
    }

    private func fourSteps(_ statuses: [QAStepStatus]) -> [QAStep] {
        let phases: [QAStepPhase] = [.setup, .steps, .steps, .teardown]
        return statuses.enumerated().map { step($0.offset + 1, $0.element, phase: phases[$0.offset]) }
    }

    // MARK: Rev and index

    func testEventAppliesOnlyARunWithAHigherRev() {
        var record = QARunRecord(run(rev: 5, current: 2))
        XCTAssertEqual(record.apply(event: run(rev: 5, current: 3), step: nil), .ignored)
        XCTAssertEqual(record.apply(event: run(rev: 4, current: 1), step: nil), .ignored)
        XCTAssertEqual(record.run.currentStep, 2)
        XCTAssertEqual(record.apply(event: run(rev: 6, current: 3), step: nil), .applied)
        XCTAssertEqual(record.run.rev, 6)
        XCTAssertEqual(record.run.currentStep, 3)
    }

    func testEventStepReplacesTheStepWithTheSameIndex() {
        var record = QARunRecord(run(rev: 5, current: 2, steps: fourSteps([.pass, .running, .pending, .pending])))
        var finished = step(2, .fail)
        finished.message = "기대와 다릅니다"
        XCTAssertEqual(record.apply(event: run(rev: 6, current: 3), step: finished), .applied)
        XCTAssertEqual(record.steps?.map(\.index), [1, 2, 3, 4])
        XCTAssertEqual(record.steps?.map(\.stepStatus), [.pass, .fail, .pending, .pending])
        XCTAssertEqual(record.steps?[1].message, "기대와 다릅니다")
    }

    func testStepOfAnOlderEventIsIgnoredWithItsRun() {
        var record = QARunRecord(run(rev: 8, steps: fourSteps([.pass, .pass, .running, .pending])))
        XCTAssertEqual(record.apply(event: run(rev: 7), step: step(3, .pending)), .ignored)
        XCTAssertEqual(record.steps?[2].stepStatus, .running)
    }

    func testStepOfAnEventBeforeTheFirstReadSurvivesAnOlderRead() {
        var record = QARunRecord(run(rev: 5))
        XCTAssertNil(record.steps)
        XCTAssertEqual(record.apply(event: run(rev: 7, current: 3), step: step(3, .running)), .appliedWithGap)
        XCTAssertNil(record.steps, "steps stay hidden until a read lists them all")
        XCTAssertEqual(record.apply(read: run(rev: 6, current: 2, steps: fourSteps([.pass, .running, .pending, .pending]))), .applied)
        XCTAssertEqual(record.run.rev, 7)
        XCTAssertEqual(record.run.currentStep, 3)
        XCTAssertEqual(record.steps?.map(\.stepStatus), [.pass, .running, .running, .pending])
    }

    func testReadOfTheSameOrNewerRevReplacesTheSteps() {
        var record = QARunRecord(run(rev: 6, steps: fourSteps([.pass, .running, .pending, .pending])))
        XCTAssertEqual(record.apply(read: run(rev: 9, status: .failed, failed: 1, steps: fourSteps([.pass, .fail, .notRun, .pass]))), .applied)
        XCTAssertEqual(record.run.runStatus, .failed)
        XCTAssertEqual(record.steps?.map(\.stepStatus), [.pass, .fail, .notRun, .pass])
        XCTAssertEqual(record.apply(read: run(rev: 9, status: .failed, failed: 1, steps: fourSteps([.pass, .fail, .notRun, .pass]))), .ignored)
    }

    func testEventAfterMissedRevsReportsAGap() {
        var record = QARunRecord(run(rev: 5))
        XCTAssertEqual(record.apply(event: run(rev: 6), step: nil), .applied)
        XCTAssertEqual(record.apply(event: run(rev: 9), step: nil), .appliedWithGap)
    }

    func testListedRunReplacesOnlyAnOlderSummaryAndLeavesStepEventsOfItsRevsToApply() {
        var record = QARunRecord(run(rev: 6, current: 2, steps: fourSteps([.pass, .running, .pending, .pending])))
        XCTAssertEqual(record.apply(listed: run(rev: 5, current: 1)), .ignored)
        XCTAssertEqual(record.apply(listed: run(rev: 8, current: 3)), .applied)
        XCTAssertEqual(record.run.currentStep, 3)
        XCTAssertNil(record.run.steps)
        XCTAssertEqual(record.apply(event: run(rev: 7, current: 3), step: step(2, .pass)), .applied)
        XCTAssertEqual(record.apply(event: run(rev: 8, current: 3), step: step(3, .running)), .applied)
        XCTAssertEqual(record.run.rev, 8)
        XCTAssertEqual(record.steps?.map(\.stepStatus), [.pass, .pass, .running, .pending])
    }

    func testListReplyThatBeatsItsEventsStillTakesTheirSteps() {
        var record = QARunRecord(run(rev: 3, current: 1, steps: fourSteps([.running, .pending, .pending, .pending])))
        record.apply(listed: run(rev: 6, current: 2, passed: 1))
        XCTAssertEqual(record.apply(event: run(rev: 4, current: 1), step: step(1, .pass)), .applied)
        XCTAssertEqual(record.apply(event: run(rev: 5, current: 2), step: step(2, .running)), .applied)
        XCTAssertEqual(record.apply(event: run(rev: 5, current: 2), step: step(2, .running)), .ignored)
        XCTAssertEqual(record.run.rev, 6)
        XCTAssertEqual(record.run.stepsPassed, 1)
        XCTAssertEqual(record.steps?.map(\.stepStatus), [.pass, .running, .pending, .pending])
    }

    // MARK: Summaries

    func testSummaryOfEachFinishedStatus() {
        XCTAssertEqual(QARunVerdict(run(rev: 9, status: .passed, passed: 4)).title, "모든 단계를 통과했습니다")
        XCTAssertEqual(QARunVerdict(run(rev: 9, status: .failed, passed: 2, failed: 1, notRun: 1)).title, "1개 단계가 실패했습니다")
        XCTAssertEqual(QARunVerdict(run(rev: 9, status: .blocked, passed: 1, blocked: 2, notRun: 1)).title, "2개 단계를 실행하지 못했습니다")
        XCTAssertEqual(QARunVerdict(run(rev: 9, status: .cancelled, passed: 2, blocked: 1, notRun: 1, steps: nil)).title, "취소했습니다")
        let restart = QARunVerdict(run(rev: 9, status: .error, passed: 1, blocked: 1, notRun: 2,
                                       error: QAErrorInfo(code: "EXECUTION_OUTCOME_UNKNOWN", message: "데몬이 다시 시작했습니다")))
        XCTAssertEqual(restart.title, "데몬이 다시 시작돼 실행이 중단되었습니다")
        XCTAssertEqual(restart.tone, .error)
        let timeout = QARunVerdict(run(rev: 9, status: .failed, passed: 1, failed: 1, notRun: 2,
                                       error: QAErrorInfo(code: "TIMEOUT", message: "실행 제한 시간(10분)이 지났습니다")))
        XCTAssertEqual(timeout.title, "실행 제한 시간이 지났습니다")
        XCTAssertEqual(timeout.detail, "실행 제한 시간(10분)이 지났습니다")
    }

    func testSummaryOfARunningRunCountsFinishedStepsAndNamesTheCurrentOne() {
        let steps = fourSteps([.pass, .running, .pending, .pending])
        let verdict = QARunVerdict(run(rev: 4, current: 2, passed: 1, steps: steps), steps: steps)
        XCTAssertEqual(verdict.tone, .running)
        XCTAssertEqual(verdict.title, "실행 중 · 4단계 중 1단계 끝남")
        XCTAssertEqual(verdict.detail, "지금 단계: 단계 2")
        let cancelling = QARunVerdict(run(rev: 5, current: 2, cancel: true, passed: 1), steps: steps)
        XCTAssertEqual(cancelling.title, "취소하는 중")
    }

    func testFinishedRunWhoseCancelArrivedDuringTeardownSaysTheRunEnded() {
        let verdict = QARunVerdict(run(rev: 12, status: .passed, cancel: true, passed: 4))
        XCTAssertEqual(verdict.title, "모든 단계를 통과했습니다")
        XCTAssertEqual(verdict.cancelNote, "취소를 요청했지만 실행이 끝났습니다")
        XCTAssertEqual(verdict.cancelDetail, "취소가 도착했을 때 멈출 단계가 없었습니다. 실행 결과는 각 단계의 결과를 따릅니다.")
        XCTAssertFalse(verdict.cancelDetail?.contains("정리 단계") ?? true)
        XCTAssertNil(QARunVerdict(run(rev: 12, status: .cancelled, cancel: true, passed: 2, blocked: 1, notRun: 1)).cancelNote)
        XCTAssertNil(QARunVerdict(run(rev: 12, status: .passed, passed: 4)).cancelNote)
    }

    func testPassedRunWithAFailedOptionalStepSaysOnlyTheRequiredStepsPassed() {
        let verdict = QARunVerdict(run(rev: 9, status: .passed, passed: 3, failed: 1))
        XCTAssertEqual(verdict.tone, .passed)
        XCTAssertEqual(verdict.title, "필수 단계를 모두 통과했습니다")
        XCTAssertEqual(verdict.detail, "선택 단계 1개는 통과하지 못했습니다. 선택 단계는 실행 결과에 들어가지 않습니다.")
    }

    // MARK: Cancel

    func testCancelIsHiddenOnceTheRunHasEnded() {
        for status in [QARunStatus.passed, .failed, .blocked, .cancelled, .error] {
            XCTAssertEqual(QACancelState(run: run(rev: 9, status: status, cancel: true), phase: .teardown, progress: QACancelProgress()), .hidden)
        }
    }

    func testFirstCancelSendsThenStopsUntilTheRunReachesTeardown() {
        var progress = QACancelProgress()
        XCTAssertEqual(QACancelState(run: run(rev: 3, current: 2), phase: .steps, progress: progress), .available)
        progress.firstSent = true
        progress.inFlight = true
        XCTAssertEqual(QACancelState(run: run(rev: 3, current: 2), phase: .steps, progress: progress), .sending)
        progress.inFlight = false
        XCTAssertEqual(QACancelState(run: run(rev: 4, current: 2, cancel: true), phase: .steps, progress: progress), .stopping)
        XCTAssertEqual(QACancelState(run: run(rev: 4, current: 2), phase: .steps, progress: progress), .stopping)
    }

    func testSecondCancelIsOfferedOnlyWhileTeardownRuns() {
        let progress = QACancelProgress(firstSent: true)
        XCTAssertEqual(QACancelState(run: run(rev: 6, current: 3, cancel: true), phase: .steps, progress: progress), .stopping)
        XCTAssertEqual(QACancelState(run: run(rev: 7, current: 4, cancel: true), phase: .teardown, progress: progress), .teardownStoppable)
        XCTAssertTrue(QACancelState.teardownStoppable.canSend)
        XCTAssertFalse(QACancelState.stopping.canSend)
    }

    func testFirstCancelThatArrivesDuringTeardownOffersStoppingTheTeardown() {
        var progress = QACancelProgress(firstSent: true, inFlight: true)
        XCTAssertEqual(QACancelState(run: run(rev: 7, current: 4), phase: .teardown, progress: progress), .sending)
        progress.inFlight = false
        XCTAssertEqual(QACancelState(run: run(rev: 8, current: 4, cancel: true), phase: .teardown, progress: progress), .teardownStoppable)
    }

    func testSecondCancelDuringTeardownShowsTheTeardownStopping() {
        var progress = QACancelProgress(firstSent: true, secondSent: true, inFlight: true)
        XCTAssertEqual(QACancelState(run: run(rev: 8, current: 4, cancel: true), phase: .teardown, progress: progress), .sendingTeardownStop)
        progress.inFlight = false
        XCTAssertEqual(QACancelState(run: run(rev: 9, current: 4, cancel: true), phase: .teardown, progress: progress), .stoppingTeardown)
        XCTAssertFalse(QACancelState.stoppingTeardown.canSend)
    }

    func testCancelStaysOnTeardownBetweenTeardownSteps() {
        let steps = [step(1, .pass, phase: .setup), step(2, .blocked), step(3, .pass, phase: .teardown), step(4, .pending, phase: .teardown)]
        let record = QARunRecord(run(rev: 9, current: nil, cancel: true, steps: steps))
        XCTAssertTrue(record.inTeardown)
        let progress = QACancelProgress(firstSent: true)
        XCTAssertEqual(QACancelState(run: record.run, phase: record.inTeardown ? .teardown : record.currentPhase, progress: progress),
                       .teardownStoppable)
        XCTAssertFalse(QARunRecord(run(rev: 3, current: 2, steps: fourSteps([.pass, .running, .pending, .pending]))).inTeardown)
    }

    // MARK: Variables

    func testSecretVariablesAreMaskedAndNotRemembered() {
        var draft = QAVarsDraft()
        draft.rows = [QAVarRow(name: "qa_root", value: "C:\\qa"), QAVarRow(name: "secret_password", value: "hunter2"),
                      QAVarRow(name: "  ", value: "")]
        XCTAssertEqual(draft.rows.map(\.isSecret), [false, true, false])
        XCTAssertEqual(draft.vars(), ["qa_root": "C:\\qa", "secret_password": "hunter2"])
        XCTAssertEqual(draft.remembered().map(\.name), ["qa_root", "secret_password"])
        XCTAssertEqual(draft.remembered().map(\.value), ["C:\\qa", ""])
        XCTAssertEqual(draft.display(draft.rows[1]), "***")
        XCTAssertEqual(draft.display(draft.rows[0]), "C:\\qa")
    }

    func testInvalidAndRepeatedNamesBlockTheVariables() {
        var draft = QAVarsDraft()
        draft.rows = [QAVarRow(name: "2fast", value: "x"), QAVarRow(name: "message", value: "a"), QAVarRow(name: "message", value: "b"),
                      QAVarRow(name: "ok_name", value: "")]
        XCTAssertEqual(draft.issue(for: draft.rows[0]), .invalidName)
        XCTAssertEqual(draft.issue(for: draft.rows[2]), .repeated)
        XCTAssertNil(draft.issue(for: draft.rows[1]))
        XCTAssertNil(draft.issue(for: draft.rows[3]))
        XCTAssertNil(draft.vars())
        draft.rows.removeFirst()
        draft.rows.remove(at: 1)
        XCTAssertEqual(draft.vars(), ["message": "a", "ok_name": ""])
    }

    func testSecretWithoutAValueBlocksTheVariablesUntilItIsEntered() {
        var draft = QAVarsDraft(rows: QAVarsDraft(rows: [QAVarRow(name: "secret_password", value: "hunter2")]).remembered())
        XCTAssertEqual(draft.issue(for: draft.rows[0]), .missingSecret)
        XCTAssertNil(draft.vars())
        draft.rows[0].value = "hunter2"
        XCTAssertNil(draft.issue(for: draft.rows[0]))
        XCTAssertEqual(draft.vars(), ["secret_password": "hunter2"])
    }

    // MARK: Scenario errors

    func testScenarioErrorSplitsFileLineAndMessage() {
        let path = "/Users/qa/시나리오: 결제.yaml"
        let problem = QAScenarioProblem(message: "\(path):14: 모르는 단계 종류 asert_ui입니다. assert_ui처럼 쓰세요", path: path)
        XCTAssertEqual(problem, QAScenarioProblem(file: path, line: 14, message: "모르는 단계 종류 asert_ui입니다. assert_ui처럼 쓰세요"))
        XCTAssertEqual(QAScenarioProblem(message: "scenario:3: name이 없습니다. 시나리오 이름을 name에 쓰세요", path: nil)?.line, 3)
        XCTAssertNil(QAScenarioProblem(message: "실행할 PC가 없습니다. qa.run의 host_id나 시나리오의 host를 주세요", path: path))
    }
}
