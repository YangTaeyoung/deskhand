import DeskhandClient
import Foundation
import Synchronization
import XCTest
@testable import DeskhandUI

/// A `RefreshLoop.Wait` that suspends until the test calls `tick()`.
final class ManualTicker: Sendable {
    private struct Waiter {
        let id: UUID
        let continuation: CheckedContinuation<Void, Error>
    }

    private let waiters = Mutex<[Waiter]>([])

    var waiting: Int { waiters.withLock { $0.count } }

    func wait(_ interval: Duration) async throws {
        let id = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                if Task.isCancelled {
                    continuation.resume(throwing: CancellationError())
                } else {
                    waiters.withLock { $0.append(Waiter(id: id, continuation: continuation)) }
                }
            }
        } onCancel: {
            let waiter = waiters.withLock { list -> Waiter? in
                guard let i = list.firstIndex(where: { $0.id == id }) else { return nil }
                return list.remove(at: i)
            }
            waiter?.continuation.resume(throwing: CancellationError())
        }
    }

    /// Ends the oldest wait.
    func tick() {
        let waiter = waiters.withLock { $0.isEmpty ? nil : $0.removeFirst() }
        waiter?.continuation.resume()
    }
}

@MainActor
final class RefreshLoopTests: XCTestCase {
    func testRunsAtOnceThenAfterEveryWaitWhileActive() async throws {
        let ticker = ManualTicker()
        var runs = 0
        let loop = RefreshLoop(interval: .seconds(2), wait: { try await ticker.wait($0) }) { runs += 1 }
        loop.setActive(true)
        try await waitUntil { runs == 1 && ticker.waiting == 1 }
        ticker.tick()
        try await waitUntil { runs == 2 && ticker.waiting == 1 }
        loop.setActive(false)
        try await waitUntil { ticker.waiting == 0 }
        ticker.tick()
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(runs, 2)
        XCTAssertFalse(loop.isActive)
        loop.setActive(true)
        try await waitUntil { runs == 3 }
        loop.setActive(true)
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(runs, 3)
        loop.setActive(false)
    }
}

@MainActor
final class ProcessesModelTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private var defaults: UserDefaults!

    override func setUp() async throws {
        defaults = UserDefaults(suiteName: "processes-tests-\(UUID().uuidString)")!
    }

    private func make(_ backend: GatedBackend, ticker: ManualTicker = ManualTicker()) -> ProcessesModel {
        ProcessesModel(hostID: dev, backend: backend, defaults: defaults, wait: { try await ticker.wait($0) })
    }

    private func loaded(_ backend: GatedBackend = GatedBackend(PreviewBackend(stepDelay: .zero))) async -> (ProcessesModel, GatedBackend) {
        let model = make(backend)
        await model.refresh()
        return (model, backend)
    }

    // MARK: Live refresh

    func testRefreshesOnEveryTickOnlyWhileVisibleAndNotPaused() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let ticker = ManualTicker()
        let model = make(backend, ticker: ticker)
        XCTAssertEqual(model.phase, .idle)

        model.setVisible(true)
        try await waitUntil { backend.procListCalls == 1 && model.phase == .loaded && ticker.waiting == 1 }
        XCTAssertTrue(model.isLive)
        ticker.tick()
        try await waitUntil { backend.procListCalls == 2 && ticker.waiting == 1 }

        model.setVisible(false)
        XCTAssertFalse(model.isLive)
        try await waitUntil { ticker.waiting == 0 }
        ticker.tick()
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(backend.procListCalls, 2)

        model.setVisible(true)
        try await waitUntil { backend.procListCalls == 3 && ticker.waiting == 1 }
        model.isPaused = true
        XCTAssertFalse(model.isLive)
        try await waitUntil { ticker.waiting == 0 }
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(backend.procListCalls, 3)
        model.isPaused = false
        try await waitUntil { backend.procListCalls == 4 }
        model.setVisible(false)
    }

    func testFirstLoadFailureIsTheFailedPhase() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let error = OperationError(code: "OFFLINE", message: "개발 PC에 연결되어 있지 않습니다")
        backend.failProcList(with: error)
        let model = make(backend)
        await model.refresh()
        XCTAssertEqual(model.phase, .failed(error))
        XCTAssertTrue(model.processes.isEmpty)
    }

    func testRefreshFailureKeepsTheLastListAndReportsIt() async throws {
        let (model, backend) = await loaded()
        let count = model.processes.count
        XCTAssertGreaterThan(count, 70)
        let updated = try XCTUnwrap(model.updatedAt)
        let error = OperationError(code: "TIMEOUT", message: "에이전트가 5초 안에 답하지 않았습니다")
        backend.failProcList(with: error)
        await model.refresh()
        XCTAssertEqual(model.phase, .loaded)
        XCTAssertEqual(model.processes.count, count)
        XCTAssertEqual(model.refreshError, error)
        XCTAssertEqual(model.updatedAt, updated)
        backend.failProcList(with: nil)
        await model.refresh()
        XCTAssertNil(model.refreshError)
    }

    // MARK: Table

    func testSearchSortAndTreeRebuildTheRows() async throws {
        let (model, _) = await loaded()
        XCTAssertEqual(model.sort, .standard)
        let cpus = model.rows.filter { !ProcessList.isIdle($0.process) }.compactMap(\.process.cpuPct)
        XCTAssertEqual(cpus, cpus.sorted(by: >))
        XCTAssertEqual(model.rows.last?.id, 0)

        model.query = "chrome"
        XCTAssertEqual(model.rows.count, 9)
        XCTAssertTrue(model.rows.allSatisfy { $0.process.name == "chrome.exe" })

        model.showsTree = true
        XCTAssertEqual(model.rows.first?.process.name, "explorer.exe")
        XCTAssertEqual(model.rows.first?.isContext, true)
        XCTAssertEqual(model.rows.first { $0.id == 6100 }?.depth, 1)

        model.query = ""
        model.toggleCollapsed(5520)
        XCTAssertNil(model.rows.first { $0.id == 6100 })
        model.toggleCollapsed(5520)
        XCTAssertNotNil(model.rows.first { $0.id == 6100 })
    }

    func testSortAndTreeChoicesPersist() async throws {
        let (model, backend) = await loaded()
        model.sort = ProcessSort(column: .memory, ascending: false)
        model.showsTree = true
        let reopened = make(backend)
        XCTAssertEqual(reopened.sort, ProcessSort(column: .memory, ascending: false))
        XCTAssertTrue(reopened.showsTree)
    }

    // MARK: Kill

    func testProtectedProcessesAreNeverOfferedForKill() async throws {
        let (model, backend) = await loaded()
        XCTAssertEqual(model.killBlock(pid: 980, tree: false), .critical)
        XCTAssertEqual(model.killBlock(pid: PreviewFixtures.agentPID, tree: false), .agent)
        XCTAssertEqual(model.killBlock(pid: 2032, tree: true), .protectedDescendant(name: "deskhand-agent.exe"))
        XCTAssertNil(model.killBlock(pid: 2032, tree: false))
        model.requestKill(pid: 980, tree: false)
        XCTAssertNil(model.pendingKill)
        model.requestKill(pid: PreviewFixtures.agentPID, tree: false)
        XCTAssertNil(model.pendingKill)
        model.requestKill(pid: 2032, tree: true)
        XCTAssertNil(model.pendingKill)
        XCTAssertTrue(backend.killCalls.isEmpty)
    }

    func testKillSucceededRemovesTheProcessAndSaysSo() async throws {
        let (model, backend) = await loaded()
        model.selectedPID = 7120
        model.requestKill(pid: 7120, tree: false)
        let request = try XCTUnwrap(model.pendingKill)
        XCTAssertEqual(request.process.name, "node.exe")
        XCTAssertFalse(request.tree)
        await model.confirmKill(request)
        XCTAssertNil(model.pendingKill)
        XCTAssertEqual(backend.killCalls, [KillCall(hostID: dev, pid: 7120, tree: false)])
        XCTAssertNil(model.processes.first { $0.pid == 7120 })
        XCTAssertNil(model.selectedPID)
        XCTAssertEqual(model.notice?.tone, .success)
        XCTAssertEqual(model.notice?.message, "‘node.exe’(PID 7120)을(를) 종료했습니다")
        XCTAssertTrue(model.killing.isEmpty)
    }

    func testTreeKillSendsTreeAndEndsEveryDescendant() async throws {
        let (model, backend) = await loaded()
        model.requestKill(pid: 6100, tree: true)
        let request = try XCTUnwrap(model.pendingKill)
        XCTAssertEqual(request.descendants.count, 8)
        await model.confirmKill(request)
        XCTAssertEqual(backend.killCalls, [KillCall(hostID: dev, pid: 6100, tree: true)])
        XCTAssertFalse(model.processes.contains { $0.name == "chrome.exe" })
        XCTAssertEqual(model.notice?.tone, .success)
        XCTAssertEqual(model.notice?.message, "‘chrome.exe’과(와) 하위 프로세스를 종료했습니다 · 9개")
    }

    func testKillFailureKeepsTheProcessAndShowsTheAgentError() async throws {
        let (model, _) = await loaded()
        model.requestKill(pid: 2260, tree: false)
        await model.confirmKill(try XCTUnwrap(model.pendingKill))
        XCTAssertNotNil(model.processes.first { $0.pid == 2260 })
        XCTAssertEqual(model.notice?.tone, .error)
        XCTAssertEqual(model.notice?.message, "‘MsMpEng.exe’을(를) 종료하지 못했습니다")
        XCTAssertEqual(model.notice?.code, "PERMISSION_DENIED")
        XCTAssertTrue(model.notice?.detail?.contains("보호된 프로세스") ?? false)
    }

    func testUnknownOutcomeIsCheckedAgainstAFreshList() async throws {
        let (model, backend) = await loaded()
        let calls = backend.procListCalls
        model.requestKill(pid: 8800, tree: false)
        await model.confirmKill(try XCTUnwrap(model.pendingKill))
        XCTAssertGreaterThan(backend.procListCalls, calls)
        XCTAssertNil(model.processes.first { $0.pid == 8800 })
        XCTAssertEqual(model.notice?.tone, .success)
        XCTAssertEqual(model.notice?.message, "‘Shop.exe’(PID 8800)이(가) 종료된 것을 확인했습니다")

        backend.answerKill(with: KillReceipt(status: .unknown, error: OperationError(
            code: "EXECUTION_OUTCOME_UNKNOWN", message: "응답을 잃었습니다", nextAction: "check_state")))
        model.requestKill(pid: 7000, tree: false)
        await model.confirmKill(try XCTUnwrap(model.pendingKill))
        XCTAssertNotNil(model.processes.first { $0.pid == 7000 })
        XCTAssertEqual(model.notice?.tone, .warning)
        XCTAssertEqual(model.notice?.message, "‘Code.exe’(PID 7000)이(가) 아직 실행 중입니다")
        XCTAssertEqual(model.notice?.code, "EXECUTION_OUTCOME_UNKNOWN")

        backend.failProcList(with: OperationError(code: "TIMEOUT", message: "응답이 없습니다"))
        model.requestKill(pid: 7036, tree: false)
        await model.confirmKill(try XCTUnwrap(model.pendingKill))
        XCTAssertEqual(model.notice?.tone, .warning)
        XCTAssertEqual(model.notice?.message, "‘Code.exe’(PID 7036)을(를) 종료했는지 확인하지 못했습니다")
    }

    func testKillOfAnEndedProcessReportsNotFound() async throws {
        let (model, backend) = await loaded()
        backend.answerKill(with: KillReceipt(status: .failed, error: OperationError(
            code: "NOT_FOUND", message: "PID 7120 프로세스가 없습니다. 이미 끝났을 수 있습니다", nextAction: "check_state")))
        model.requestKill(pid: 7120, tree: false)
        await model.confirmKill(try XCTUnwrap(model.pendingKill))
        XCTAssertEqual(model.notice?.tone, .error)
        XCTAssertEqual(model.notice?.code, "NOT_FOUND")
    }
}
