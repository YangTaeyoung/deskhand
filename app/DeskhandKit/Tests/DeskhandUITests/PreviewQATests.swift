import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// The preview's QA runs: the samples, starting a run, its events, and the cancel rules of
/// local-api.md §10.
final class PreviewQATests: XCTestCase {
    private let meeting = PreviewFixtures.meetingPCID
    private let path = "/Users/qa/fixture-smoke.yaml"

    /// A preview whose steps take `delay` / 700 ms of a second each.
    private func backend(delay: Duration = .milliseconds(70)) -> PreviewBackend {
        PreviewBackend(scenario: .standard, stepDelay: delay)
    }

    /// Polls the run until `condition` holds.
    private func wait(_ backend: PreviewBackend, _ runID: String, timeout: Duration = .seconds(8),
                      _ condition: @escaping (QARun) -> Bool) async throws -> QARun {
        let deadline = ContinuousClock.now + timeout
        while true {
            let run = try await backend.qaGet(runID: runID)
            if condition(run) { return run }
            guard ContinuousClock.now < deadline else {
                XCTFail("run \(runID) did not reach the condition; status \(run.status), step \(String(describing: run.currentStep))")
                throw CancellationError()
            }
            try await Task.sleep(for: .milliseconds(5))
        }
    }

    func testSamplesListNewestFirstWithEveryStatus() async throws {
        let page = try await PreviewBackend(scenario: .standard, stepDelay: .zero).qaList(hostID: nil, limit: 50, cursor: nil)
        XCTAssertEqual(page.runs.map(\.startedAt.date), page.runs.map(\.startedAt.date).sorted(by: >))
        XCTAssertEqual(Set(page.runs.compactMap(\.runStatus)), Set(QARunStatus.allCases))
        XCTAssertTrue(page.runs.allSatisfy { $0.steps == nil })
        let login = try await PreviewBackend(scenario: .standard, stepDelay: .zero).qaGet(runID: PreviewQASamples.loginRunID)
        XCTAssertEqual(login.steps?.first { $0.name == "비밀번호 입력" }?.params["value"], .string("***"))
    }

    func testListPagesWithACursor() async throws {
        let preview = PreviewBackend(scenario: .standard, stepDelay: .zero)
        let first = try await preview.qaList(hostID: PreviewFixtures.devPCID, limit: 4, cursor: nil)
        XCTAssertEqual(first.runs.count, 4)
        let next = try await preview.qaList(hostID: PreviewFixtures.devPCID, limit: 4, cursor: first.nextCursor)
        XCTAssertEqual(Set(first.runs.map(\.runID)).intersection(next.runs.map(\.runID)), [])
        XCTAssertNil(next.nextCursor)
    }

    func testRunPassesWithRisingRevsAndRefusesASecondRunOnThePC() async throws {
        let preview = backend()
        let events = preview.events(kinds: [EventKind.qaUpdated], hostID: nil)
        let run = try await preview.qaRun(QARunRequest(hostID: meeting, scenarioPath: path))
        do {
            _ = try await preview.qaRun(QARunRequest(hostID: meeting, scenarioPath: path))
            XCTFail("a second run on the same PC started")
        } catch let error as RPCErrorInfo {
            XCTAssertEqual(error.code, "IN_PROGRESS")
        }
        let done = try await wait(preview, run.runID) { !$0.isRunning }
        XCTAssertEqual(done.runStatus, .passed)
        XCTAssertNotNil(done.report)
        var revs: [Int] = []
        for try await event in events {
            guard let value = event.payload["run"], let seen = try? LiveBackend.decode(QARun.self, from: value), seen.runID == run.runID else { continue }
            revs.append(seen.rev)
            if !seen.isRunning { break }
        }
        XCTAssertEqual(revs, revs.sorted())
        XCTAssertEqual(Set(revs).count, revs.count)
    }

    func testScenarioProblemsAreRefusedWithTheirLine() async throws {
        let preview = backend()
        do {
            _ = try await preview.qaRun(QARunRequest(hostID: meeting, scenarioPath: "/Users/qa/checkout-broken.yaml"))
            XCTFail("a broken scenario started")
        } catch let error as RPCErrorInfo {
            XCTAssertEqual(error.code, "INVALID_ARGUMENT")
            XCTAssertEqual(QAScenarioProblem(message: error.message, path: "/Users/qa/checkout-broken.yaml")?.line, 14)
        }
    }

    func testFirstCancelStopsTheStepSkipsTheRestAndRunsTeardown() async throws {
        let preview = backend()
        let run = try await preview.qaRun(QARunRequest(hostID: meeting, scenarioPath: path))
        _ = try await wait(preview, run.runID) { ($0.currentStep ?? 0) >= 3 }
        let taken = try await preview.qaCancel(runID: run.runID)
        XCTAssertTrue(taken.cancelRequested)
        XCTAssertTrue(taken.isRunning)
        let done = try await wait(preview, run.runID) { !$0.isRunning }
        let steps = try XCTUnwrap(done.steps)
        XCTAssertEqual(done.runStatus, .cancelled)
        XCTAssertEqual(steps.filter { $0.stepStatus == .blocked }.map(\.message), ["취소로 멈췄습니다"])
        XCTAssertTrue(steps.filter { $0.stepPhase != .teardown && $0.stepStatus == .notRun }.allSatisfy { $0.message == "취소로 실행하지 않았습니다" })
        XCTAssertEqual(steps.last?.stepStatus, .pass, "teardown runs after the first cancel")
        let again = try await preview.qaCancel(runID: run.runID)
        XCTAssertEqual(again.runStatus, .cancelled)
    }

    func testSecondCancelDuringTeardownStopsTeardown() async throws {
        let preview = backend(delay: .milliseconds(280))
        let run = try await preview.qaRun(QARunRequest(hostID: meeting, scenarioPath: path))
        _ = try await wait(preview, run.runID) { ($0.currentStep ?? 0) >= 2 }
        _ = try await preview.qaCancel(runID: run.runID)
        _ = try await wait(preview, run.runID) { $0.currentStep == 12 && $0.steps?.last?.stepStatus == .running }
        _ = try await preview.qaCancel(runID: run.runID)
        let done = try await wait(preview, run.runID) { !$0.isRunning }
        XCTAssertEqual(done.runStatus, .cancelled)
        XCTAssertEqual(done.steps?.last?.stepStatus, .blocked)
    }

    func testFirstCancelDuringTeardownLetsTheRunEndByItsSteps() async throws {
        let preview = backend(delay: .milliseconds(140))
        let run = try await preview.qaRun(QARunRequest(hostID: meeting, scenarioPath: path))
        _ = try await wait(preview, run.runID) { $0.currentStep == 12 && $0.steps?.last?.stepStatus == .running }
        _ = try await preview.qaCancel(runID: run.runID)
        let done = try await wait(preview, run.runID) { !$0.isRunning }
        XCTAssertEqual(done.runStatus, .passed)
        XCTAssertTrue(done.cancelRequested)
        XCTAssertEqual(QARunVerdict(done).cancelNote, "취소를 요청했지만 실행이 끝났습니다")
    }

    func testCancelOfARunThatEndedOtherwiseIsRefused() async throws {
        let preview = PreviewBackend(scenario: .standard, stepDelay: .zero)
        do {
            _ = try await preview.qaCancel(runID: PreviewQASamples.failedRunID)
            XCTFail("a finished run was cancelled")
        } catch let error as RPCErrorInfo {
            XCTAssertEqual(error.code, "INVALID_ARGUMENT")
        }
    }

    func testRunOnAPersonControlledPCBlocksControlSteps() async throws {
        let preview = backend()
        let run = try await preview.qaRun(QARunRequest(hostID: PreviewFixtures.stagingID, scenarioPath: path))
        let done = try await wait(preview, run.runID) { !$0.isRunning }
        XCTAssertEqual(done.runStatus, .blocked)
        let launch = done.steps?.first { $0.action == "launch" }
        XCTAssertEqual(launch?.stepStatus, .blocked)
        XCTAssertEqual(launch?.error?.code, "CONTROL_HELD_BY_HUMAN")
    }

    func testEvidenceOfTheSamplesReadsAsImagesAndText() async throws {
        let preview = PreviewBackend(scenario: .standard, stepDelay: .zero)
        let failed = try await preview.qaGet(runID: PreviewQASamples.failedRunID)
        let evidence = try XCTUnwrap(failed.steps?.first { $0.index == 7 }?.evidence)
        XCTAssertEqual(evidence.map(\.kind), ["screenshot", "ui_tree"])
        let png = try await preview.evidenceData(path: evidence[0].path, maxBytes: QAEvidenceStore.imageLimit)
        XCTAssertEqual(Array(png.prefix(4)), [0x89, 0x50, 0x4E, 0x47])
        let tree = try await preview.evidenceData(path: evidence[1].path, maxBytes: QAEvidenceStore.textLimit)
        let read = QAEvidenceStore.text(tree, limit: QAEvidenceStore.textLimit, json: true)
        XCTAssertFalse(read.truncated)
        XCTAssertTrue(read.text.hasPrefix("{\n  \"window\": {"), "the daemon's key order stays")
        let thumbnail = try XCTUnwrap(QAEvidenceStore.decodeImage(png, maxPixels: QAEvidenceStore.thumbnailPixels))
        XCTAssertEqual(max(thumbnail.image.width, thumbnail.image.height), 480)
        XCTAssertEqual(thumbnail.pixels, CGSize(width: 1600, height: 1000))
    }
}
