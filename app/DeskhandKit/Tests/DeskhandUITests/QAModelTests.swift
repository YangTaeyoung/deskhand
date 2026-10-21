import DeskhandClient
import Foundation
import Synchronization
import XCTest
@testable import DeskhandUI

/// QA backend whose replies the test sets, that records its calls and lets the test send events and
/// reconnects.
final class FakeQABackend: QABackend, EventsBackend {
    struct State {
        var pages: [String: QARunPage] = [:]
        var runs: [String: QARun] = [:]
        var cancelReply: Result<QARun, RPCErrorInfo>?
        var runReply: Result<QARun, RPCErrorInfo>?
        var exportReply: Result<ArtifactExport, RPCErrorInfo>?
        var listFailure: RPCErrorInfo?
        var listDelay: Duration?
        var calls: [String] = []
        var runRequests: [QARunRequest] = []
        var events: [AsyncThrowingStream<DaemonEvent, Error>.Continuation] = []
        var reconnects: [AsyncStream<Void>.Continuation] = []
    }

    let state = Mutex(State())

    func calls(_ prefix: String) -> [String] { state.withLock { $0.calls.filter { $0.hasPrefix(prefix) } } }

    func send(_ event: DaemonEvent) {
        for continuation in state.withLock({ $0.events }) { continuation.yield(event) }
    }

    func reconnect() {
        for continuation in state.withLock({ $0.reconnects }) { continuation.yield() }
    }

    var subscribed: Bool { state.withLock { !$0.events.isEmpty && !$0.reconnects.isEmpty } }

    func qaRun(_ request: QARunRequest) async throws -> QARun {
        let reply = state.withLock { s in
            s.calls.append("qa.run")
            s.runRequests.append(request)
            return s.runReply
        }
        switch reply {
        case .success(let run): return run
        case .failure(let error): throw error
        case nil: throw RPCErrorInfo(message: "no reply", code: "INTERNAL")
        }
    }

    func qaGet(runID: String) async throws -> QARun {
        let run = state.withLock { s in
            s.calls.append("qa.get " + runID)
            return s.runs[runID]
        }
        guard let run else { throw RPCErrorInfo(message: "없음", code: "NOT_FOUND") }
        return run
    }

    func qaList(hostID: String?, limit: Int, cursor: String?) async throws -> QARunPage {
        let key = (hostID ?? "*") + (cursor.map { "@" + $0 } ?? "")
        let (page, failure, delay) = state.withLock { s in
            s.calls.append("qa.list " + key)
            return (s.pages[key] ?? QARunPage(runs: []), s.listFailure, s.listDelay)
        }
        if let delay { try await Task.sleep(for: delay) }
        if let failure { throw failure }
        return page
    }

    func qaCancel(runID: String) async throws -> QARun {
        let reply = state.withLock { s in
            s.calls.append("qa.cancel " + runID)
            return s.cancelReply
        }
        switch reply {
        case .success(let run): return run
        case .failure(let error): throw error
        case nil: throw RPCErrorInfo(message: "no reply", code: "INTERNAL")
        }
    }

    func artifactsExport(jobID: String, dir: String) async throws -> ArtifactExport {
        let reply = state.withLock { s in
            s.calls.append("artifacts.export " + jobID)
            return s.exportReply
        }
        switch reply {
        case .success(let export): return export
        case .failure(let error): throw error
        case nil: throw RPCErrorInfo(message: "no reply", code: "INTERNAL")
        }
    }

    func evidenceData(path: String, maxBytes: Int) async throws -> Data { Data() }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: DaemonEvent.self)
        state.withLock { $0.events.append(continuation) }
        return stream
    }

    func reconnects() -> AsyncStream<Void> {
        let (stream, continuation) = AsyncStream.makeStream(of: Void.self)
        state.withLock { $0.reconnects.append(continuation) }
        return stream
    }
}

@MainActor
final class QAModelTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private let start = Timestamp(date: Date(timeIntervalSince1970: 1_800_000_000))

    private func run(_ id: String, rev: Int, status: QARunStatus = .running, host: String? = nil, minutesAgo: Double = 0, current: Int? = nil,
                     cancel: Bool = false, steps: [QAStep]? = nil) -> QARun {
        QARun(runID: id, jobID: "j" + id.dropFirst(), hostID: host ?? dev, name: "fixture-smoke", scenarioPath: "/Users/qa/fixture-smoke.yaml",
              status: status, rev: rev, startedAt: Timestamp(date: start.date.addingTimeInterval(-minutesAgo * 60)), stepsTotal: 3,
              currentStep: current, cancelRequested: cancel, reportDir: status == .running ? nil : "/h/artifacts/j", steps: steps)
    }

    private func steps(_ statuses: [QAStepStatus]) -> [QAStep] {
        let phases: [QAStepPhase] = [.setup, .steps, .teardown]
        return statuses.enumerated().map {
            QAStep(index: $0.offset + 1, phase: phases[$0.offset], name: "단계 \($0.offset + 1)", action: "assert_ui", status: $0.element)
        }
    }

    private func event(_ run: QARun, step: QAStep? = nil) -> DaemonEvent {
        var payload: [String: JSONValue] = ["run": PreviewBackend.payload(run.summary), "kind": .string(EventKind.qaUpdated)]
        if let step { payload["step"] = PreviewBackend.payload(step) }
        return DaemonEvent(seq: run.rev, hostID: run.hostID, kind: EventKind.qaUpdated, payload: payload)
    }

    private func model(_ backend: FakeQABackend) -> QAModel {
        QAModel(backend: backend, defaults: UserDefaults(suiteName: "qa-model-\(UUID())")!,
                backoff: AppStore.ResubscribeBackoff(initial: .milliseconds(10), maximum: .milliseconds(20)))
    }

    private func loaded(_ backend: FakeQABackend, runs: [QARun]) async throws -> QAModel {
        backend.state.withLock { $0.pages[dev] = QARunPage(runs: runs.map(\.summary)) }
        let model = model(backend)
        model.start()
        model.ensureLoaded(.host(dev))
        try await waitUntil { model.listing(.host(dev)).phase == .loaded }
        try await waitUntil { backend.subscribed }
        return model
    }

    func testListedRunsShowNewestFirstAndOnlyHigherRevEventsChangeThem() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-old", rev: 9, status: .passed, minutesAgo: 60), run("q-new", rev: 3, current: 2)])
        XCTAssertEqual(model.runs(in: .host(dev)).map(\.runID), ["q-new", "q-old"])
        backend.send(event(run("q-new", rev: 2, current: 1)))
        backend.send(event(run("q-new", rev: 4, current: 3)))
        try await waitUntil { model.run("q-new")?.currentStep == 3 }
        backend.send(event(run("q-new", rev: 4, current: 1)))
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(model.run("q-new")?.currentStep, 3)
        XCTAssertEqual(model.run("q-new")?.rev, 4)
    }

    func testEventOfAnUnknownRunAddsItToTheLoadedListsOfItsComputer() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [])
        backend.send(event(run("q-cli", rev: 1)))
        backend.send(event(run("q-other", rev: 1, host: PreviewFixtures.qaLaptopID)))
        try await waitUntil { model.run("q-other") != nil }
        XCTAssertEqual(model.runs(in: .host(dev)).map(\.runID), ["q-cli"])
    }

    func testEventStepsMergeByIndexIntoReadSteps() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 3, current: 2)])
        backend.state.withLock { $0.runs["q-1"] = self.run("q-1", rev: 3, current: 2, steps: self.steps([.pass, .running, .pending])) }
        model.select("q-1", in: .host(dev))
        try await waitUntil { model.record("q-1")?.steps != nil }
        var done = steps([.pass, .fail, .pending])[1]
        done.message = "기대와 다릅니다"
        backend.send(event(run("q-1", rev: 4, current: 3), step: done))
        try await waitUntil { model.record("q-1")?.steps?[1].stepStatus == .fail }
        XCTAssertEqual(model.record("q-1")?.steps?.map(\.stepStatus), [.pass, .fail, .pending])
    }

    func testEventsDroppedReadsTheListsAndTheRunsInViewAgain() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-live", rev: 3, current: 2), run("q-done", rev: 9, status: .passed, minutesAgo: 30)])
        backend.state.withLock { $0.runs["q-live"] = self.run("q-live", rev: 5, current: 3, steps: self.steps([.pass, .pass, .running])) }
        let lists = backend.calls("qa.list").count
        backend.send(DaemonEvent(seq: 0, kind: EventKind.eventsDropped, payload: ["count": .int(12)]))
        try await waitUntil { backend.calls("qa.list").count == lists + 1 }
        try await waitUntil { model.run("q-live")?.rev == 5 }
        XCTAssertEqual(backend.calls("qa.get"), ["qa.get q-live"])
        XCTAssertEqual(model.record("q-live")?.steps?.map(\.stepStatus), [.pass, .pass, .running])
    }

    func testRevGapReadsTheRunAgainWhenItsStepsAreShown() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 3, current: 1)])
        backend.state.withLock { $0.runs["q-1"] = self.run("q-1", rev: 3, current: 1, steps: self.steps([.running, .pending, .pending])) }
        model.select("q-1", in: .host(dev))
        try await waitUntil { model.record("q-1")?.steps != nil }
        backend.state.withLock { $0.runs["q-1"] = self.run("q-1", rev: 7, current: 3, steps: self.steps([.pass, .pass, .running])) }
        backend.send(event(run("q-1", rev: 7, current: 3), step: steps([.pass, .pass, .running])[2]))
        try await waitUntil { backend.calls("qa.get").count == 2 }
        try await waitUntil { model.record("q-1")?.steps?.map(\.stepStatus) == [.pass, .pass, .running] }
    }

    func testReconnectReadsTheListsAndTheRunsInViewAgain() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-live", rev: 3, current: 1)])
        backend.state.withLock { $0.runs["q-live"] = self.run("q-live", rev: 3, current: 1, steps: self.steps([.running, .pending, .pending])) }
        model.select("q-live", in: .host(dev))
        try await waitUntil { model.record("q-live")?.steps != nil }
        backend.state.withLock {
            $0.pages[self.dev] = QARunPage(runs: [self.run("q-live", rev: 8, status: .passed).summary])
            $0.runs["q-live"] = self.run("q-live", rev: 8, status: .passed, steps: self.steps([.pass, .pass, .pass]))
        }
        let lists = backend.calls("qa.list").count
        backend.reconnect()
        try await waitUntil { backend.calls("qa.list").count == lists + 1 }
        try await waitUntil { model.record("q-live")?.steps?.map(\.stepStatus) == [.pass, .pass, .pass] }
        XCTAssertEqual(model.run("q-live")?.runStatus, .passed)
        XCTAssertEqual(model.cancelState(of: "q-live"), .hidden)
    }

    func testFirstCancelStopsAndASecondDuringTeardownIsOffered() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 3, current: 2)])
        backend.state.withLock {
            $0.runs["q-1"] = self.run("q-1", rev: 3, current: 2, steps: self.steps([.pass, .running, .pending]))
            $0.cancelReply = .success(self.run("q-1", rev: 4, current: 2, cancel: true, steps: self.steps([.pass, .running, .pending])))
        }
        model.select("q-1", in: .host(dev))
        try await waitUntil { model.record("q-1")?.steps != nil }
        XCTAssertEqual(model.cancelState(of: "q-1"), .available)
        await model.cancel("q-1")
        XCTAssertEqual(model.cancelState(of: "q-1"), .stopping)
        await model.cancel("q-1")
        XCTAssertEqual(backend.calls("qa.cancel").count, 1, "a cancel is not sent again before teardown")
        backend.send(event(run("q-1", rev: 6, current: 3, cancel: true), step: steps([.pass, .blocked, .running])[2]))
        try await waitUntil { model.cancelState(of: "q-1") == .teardownStoppable }
        backend.state.withLock { $0.cancelReply = .success(self.run("q-1", rev: 7, current: 3, cancel: true)) }
        await model.cancel("q-1")
        XCTAssertEqual(backend.calls("qa.cancel").count, 2)
        XCTAssertEqual(model.cancelState(of: "q-1"), .stoppingTeardown)
    }

    func testCancelOfARunThatAlreadyEndedSaysSoAndReadsItAgain() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 3, current: 2)])
        backend.state.withLock {
            $0.cancelReply = .failure(RPCErrorInfo(message: "QA 실행 q-1는 이미 통과 상태로 끝났습니다. 취소할 필요가 없습니다", code: "INVALID_ARGUMENT"))
            $0.runs["q-1"] = self.run("q-1", rev: 9, status: .passed, steps: self.steps([.pass, .pass, .pass]))
        }
        await model.cancel("q-1")
        XCTAssertEqual(model.notice?.message, "실행이 이미 끝나 취소하지 않았습니다")
        XCTAssertEqual(model.run("q-1")?.runStatus, .passed)
        XCTAssertEqual(model.cancelState(of: "q-1"), .hidden)
    }

    func testFailedCancelLetsThePersonTryAgain() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 3, current: 2)])
        backend.state.withLock { $0.cancelReply = .failure(RPCErrorInfo(message: "데몬과 연결이 끊겼습니다", code: "INTERNAL")) }
        await model.cancel("q-1")
        XCTAssertEqual(model.notice?.tone, .error)
        XCTAssertEqual(model.cancelState(of: "q-1"), .available)
    }

    func testStartedRunIsSelectedAndItsScenarioRememberedWithoutSecrets() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [])
        backend.state.withLock { $0.runReply = .success(self.run("q-new", rev: 1, steps: self.steps([.pending, .pending, .pending]))) }
        let draft = QARunDraft(scenarioPath: "/Users/qa/login.yaml", hostID: dev,
                               vars: QAVarsDraft(rows: [QAVarRow(name: "user", value: "qa-bot"), QAVarRow(name: "secret_password", value: "hunter2")]),
                               timeout: .minutes(5))
        let failure = await model.start(draft)
        XCTAssertNil(failure)
        XCTAssertEqual(backend.state.withLock { $0.runRequests.first },
                       QARunRequest(hostID: dev, scenarioPath: "/Users/qa/login.yaml", vars: ["user": "qa-bot", "secret_password": "hunter2"],
                                    timeoutMS: 300_000))
        XCTAssertEqual(model.selectedRun(in: .host(dev))?.runID, "q-new")
        XCTAssertNotNil(model.record("q-new")?.steps)
        XCTAssertEqual(model.recentScenarios.first?.vars.map(\.value), ["qa-bot", ""])
    }

    func testStartFailureCarriesTheScenarioLine() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [])
        backend.state.withLock {
            $0.runReply = .failure(RPCErrorInfo(message: "/Users/qa/broken.yaml:14: 모르는 단계 종류 asert_ui입니다. assert_ui처럼 쓰세요",
                                                code: "INVALID_ARGUMENT"))
        }
        let failure = await model.start(QARunDraft(scenarioPath: "/Users/qa/broken.yaml", hostID: dev))
        XCTAssertEqual(failure?.code, "INVALID_ARGUMENT")
        XCTAssertEqual(QAScenarioProblem(message: failure?.message ?? "", path: "/Users/qa/broken.yaml")?.line, 14)
        XCTAssertTrue(model.recentScenarios.isEmpty)
    }

    func testExportKeepsAHashMismatch() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 9, status: .failed)])
        backend.state.withLock {
            $0.exportReply = .failure(RPCErrorInfo(message: "report.html의 sha256이 기록과 다릅니다", code: "HASH_MISMATCH"))
        }
        await model.export("q-1", to: "/Users/qa/Desktop")
        XCTAssertEqual(model.exports["q-1"], .failed(OperationError(code: "HASH_MISMATCH", message: "report.html의 sha256이 기록과 다릅니다"),
                                                     dir: "/Users/qa/Desktop"))
        backend.state.withLock { $0.exportReply = .success(ArtifactExport(jobID: "j-1", dir: "/Users/qa/Desktop/j-1", files: 5, bytes: 1_024)) }
        await model.export("q-1", to: "/Users/qa/Desktop")
        XCTAssertEqual(model.exports["q-1"], .exported(ArtifactExport(jobID: "j-1", dir: "/Users/qa/Desktop/j-1", files: 5, bytes: 1_024)))
    }

    func testReloadKeepsRunsOfLaterPagesAndTheirCursor() async throws {
        let backend = FakeQABackend()
        backend.state.withLock {
            $0.pages[self.dev] = QARunPage(runs: [self.run("q-new", rev: 2, status: .passed).summary], nextCursor: "c1")
            $0.pages[self.dev + "@c1"] = QARunPage(runs: [self.run("q-old", rev: 2, status: .passed, minutesAgo: 90).summary], nextCursor: "c2")
        }
        let model = model(backend)
        model.start()
        model.ensureLoaded(.host(dev))
        try await waitUntil { model.listing(.host(self.dev)).phase == .loaded }
        await model.loadMore(.host(dev))
        model.select("q-old", in: .host(dev))
        XCTAssertEqual(model.runs(in: .host(dev)).map(\.runID), ["q-new", "q-old"])
        backend.state.withLock { $0.pages[self.dev] = QARunPage(runs: [self.run("q-newest", rev: 1).summary, self.run("q-new", rev: 2, status: .passed).summary],
                                                               nextCursor: "c1") }
        await model.load(.host(dev))
        XCTAssertEqual(model.runs(in: .host(dev)).map(\.runID), ["q-newest", "q-new", "q-old"])
        XCTAssertEqual(model.listing(.host(dev)).nextCursor, "c2")
        XCTAssertEqual(model.selectedRun(in: .host(dev))?.runID, "q-old")
    }

    func testReconnectReadsEachListOnce() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-done", rev: 9, status: .passed)])
        model.ensureLoaded(.all)
        try await waitUntil { model.listing(.all).phase == .loaded }
        let before = backend.calls("qa.list").count
        backend.reconnect()
        try await waitUntil { backend.calls("qa.list").count == before + 2 }
        try await Task.sleep(for: .milliseconds(60))
        XCTAssertEqual(backend.calls("qa.list").count, before + 2)
        XCTAssertEqual(model.resyncs, 1)
    }

    func testTriggersDuringAResyncFoldIntoOneMore() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-done", rev: 9, status: .passed)])
        backend.state.withLock { $0.listDelay = .milliseconds(40) }
        let before = model.resyncs
        backend.reconnect()
        try await waitUntil { model.resyncs == before + 1 }
        backend.send(DaemonEvent(seq: 0, kind: EventKind.eventsDropped, payload: ["count": .int(3)]))
        backend.reconnect()
        try await waitUntil { model.resyncs == before + 2 }
        try await Task.sleep(for: .milliseconds(250))
        XCTAssertEqual(model.resyncs, before + 2)
    }

    func testFailedReloadOfALoadedListKeepsItAndSaysSo() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-done", rev: 9, status: .passed)])
        backend.state.withLock { $0.listFailure = RPCErrorInfo(message: "데몬이 답하지 않았습니다", code: "TIMEOUT") }
        await model.load(.host(dev))
        XCTAssertEqual(model.listing(.host(dev)).phase, .loaded)
        XCTAssertEqual(model.listing(.host(dev)).reloadError?.code, "TIMEOUT")
        XCTAssertEqual(model.runs(in: .host(dev)).map(\.runID), ["q-done"])
        backend.state.withLock { $0.listFailure = nil }
        await model.load(.host(dev))
        XCTAssertNil(model.listing(.host(dev)).reloadError)
    }

    func testFailedReadOfShownStepsIsKeptForTheRun() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [run("q-1", rev: 3, current: 1)])
        backend.state.withLock { $0.runs["q-1"] = self.run("q-1", rev: 3, current: 1, steps: self.steps([.running, .pending, .pending])) }
        model.select("q-1", in: .host(dev))
        try await waitUntil { model.record("q-1")?.steps != nil }
        backend.state.withLock { $0.runs["q-1"] = nil }
        await model.refresh("q-1")
        XCTAssertEqual(model.refreshErrors["q-1"]?.code, "NOT_FOUND")
        XCTAssertNotNil(model.record("q-1")?.steps)
    }

    func testThousandStepEventsApplyInPlaceWithinTheBound() async throws {
        let backend = FakeQABackend()
        let many = (1...1000).map { QAStep(index: $0, phase: $0 == 1000 ? .teardown : .steps, name: "단계 \($0)", action: "assert_ui", status: .pending) }
        let model = try await loaded(backend, runs: [])
        model.seed(runs: [run("q-big", rev: 1, steps: many)], listings: [.host(dev): QAModel.Listing(ids: ["q-big"], phase: .loaded)])
        let events = (1...1000).map { index -> DaemonEvent in
            var done = many[index - 1]
            done.status = QAStepStatus.pass.rawValue
            return event(run("q-big", rev: index + 1, current: index), step: done)
        }
        let clock = ContinuousClock()
        let elapsed = clock.measure {
            for event in events { model.handle(event) }
        }
        XCTAssertEqual(model.record("q-big")?.steps?.filter { $0.stepStatus == .pass }.count, 1000)
        XCTAssertLessThan(elapsed, .seconds(2), "1,000 step events took \(elapsed)")
        var record = QARunRecord(run("q-plain", rev: 1, steps: many))
        let plain = clock.measure {
            for index in 1...1000 {
                var done = many[index - 1]
                done.status = QAStepStatus.pass.rawValue
                record.apply(event: run("q-plain", rev: index + 1, current: index), step: done)
                _ = record.steps?.count
            }
        }
        XCTAssertLessThan(plain, .milliseconds(200), "1,000 record events took \(plain)")
    }

    func testRunningRunFocusesItsCurrentStepAndAFinishedOneItsFirstFailure() async throws {
        let backend = FakeQABackend()
        let model = try await loaded(backend, runs: [])
        model.seed(runs: [run("q-live", rev: 4, current: 3, steps: steps([.pass, .fail, .running])),
                          run("q-done", rev: 9, status: .failed, steps: steps([.pass, .fail, .pass]))],
                   listings: [.host(dev): QAModel.Listing(ids: ["q-live", "q-done"], phase: .loaded)])
        XCTAssertEqual(model.focusedStep(of: "q-live")?.index, 3)
        XCTAssertEqual(model.focusedStep(of: "q-done")?.index, 2)
        model.selectStep(1, of: "q-done")
        XCTAssertEqual(model.focusedStep(of: "q-done")?.index, 1)
    }
}
