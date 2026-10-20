import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class LogsModelTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID

    private func makeStore(_ backend: GatedBackend) -> AppStore {
        let defaults = UserDefaults(suiteName: "logs-tests-\(UUID().uuidString)")!
        return AppStore.seeded(backend: backend, defaults: defaults, hosts: PreviewFixtures.hosts(), selectedHostID: dev)
    }

    private func started(_ hostID: String? = nil) async throws -> (LogsModel, GatedBackend, AppStore) {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = store.logs(for: try XCTUnwrap(store.host(hostID ?? dev)))
        await model.start()
        return (model, backend, store)
    }

    // MARK: Query

    func testFilterBuildsTheEventQuery() throws {
        let now = Date(timeIntervalSince1970: 1_792_000_000)
        var filter = EventFilter()
        XCTAssertEqual(filter.query(hostID: dev, now: now), EventLogQuery(
            hostID: dev, log: .system, since: now.addingTimeInterval(-86_400), until: nil, levels: nil, provider: nil, max: 200))

        filter.range = .lastHour
        XCTAssertEqual(filter.query(hostID: dev, now: now).since, now.addingTimeInterval(-3_600))
        filter.range = .lastWeek
        XCTAssertEqual(filter.query(hostID: dev, now: now).since, now.addingTimeInterval(-7 * 86_400))

        let start = now.addingTimeInterval(-50_000)
        let end = now.addingTimeInterval(-20_000)
        filter.range = .custom(start: start, end: end)
        filter.log = .application
        filter.levels = [.warning, .critical]
        filter.provider = "Application Error"
        let custom = filter.query(hostID: dev, now: now)
        XCTAssertEqual(custom.since, start)
        XCTAssertEqual(custom.until, end)
        XCTAssertEqual(custom.levels, [.critical, .warning])
        XCTAssertEqual(custom.provider, "Application Error")
        XCTAssertEqual(custom.log, .application)

        let older = filter.query(hostID: dev, now: now, before: now.addingTimeInterval(-30_000))
        XCTAssertEqual(older.until, now.addingTimeInterval(-30_000))
        let clamped = filter.query(hostID: dev, now: now, before: now.addingTimeInterval(-1_000))
        XCTAssertEqual(clamped.until, end)
    }

    func testErrorExplanationJoinsTheMessageAndTheFixAsSentences() {
        XCTAssertEqual(LogsModel.explanation(OperationError(code: "TIMEOUT", message: "에이전트가 10초 안에 시스템 정보를 보내지 않았습니다")),
                       "에이전트가 10초 안에 시스템 정보를 보내지 않았습니다. 컴퓨터가 켜져 있고 같은 네트워크나 VPN에 있는지 확인하세요.")
        XCTAssertEqual(LogsModel.explanation(OperationError(message: "응답이 없습니다.")), "응답이 없습니다.")
    }

    // MARK: Loading

    func testStartLoadsTheSummaryEventsAndCrashDumps() async throws {
        let (model, backend, _) = try await started()
        XCTAssertEqual(model.systemPhase, .loaded)
        XCTAssertEqual(model.system?.computerName, "DEV-PC")
        XCTAssertEqual(model.eventsPhase, .loaded)
        XCTAssertFalse(model.events.isEmpty)
        let times = model.events.map(\.time.date)
        XCTAssertEqual(times, times.sorted(by: >))
        XCTAssertEqual(model.crashesPhase, .loaded)
        XCTAssertEqual(model.crashes.map(\.app), ["Shop.exe", "dotnet.exe", "Code.exe"])
        XCTAssertEqual(backend.eventQueries.count, 1)
        await model.start()
        XCTAssertEqual(backend.eventQueries.count, 1)
    }

    func testAnOfflineComputerFailsEverySection() async throws {
        let (model, _, _) = try await started(PreviewFixtures.designID)
        for phase in [model.systemPhase, model.eventsPhase, model.crashesPhase] {
            guard case .failed(let error) = phase else { return XCTFail("\(phase) is not failed") }
            XCTAssertEqual(error.code, "OFFLINE")
        }
    }

    func testOnlyTheLatestFilterReplyIsApplied() async throws {
        let (model, backend, _) = try await started()
        backend.holdEventQueries(true)
        var errors = model.filter
        errors.levels = [.error]
        var warnings = model.filter
        warnings.levels = [.warning]
        let first = Task { await model.setFilter(errors) }
        try await waitUntil { backend.pendingEventQueryCount == 1 }
        let second = Task { await model.setFilter(warnings) }
        try await waitUntil { backend.pendingEventQueryCount == 2 }
        XCTAssertTrue(model.isReloadingEvents)
        backend.releaseEventQuery()
        await first.value
        backend.releaseEventQuery()
        await second.value
        XCTAssertEqual(model.filter.levels, [.warning])
        XCTAssertFalse(model.events.isEmpty)
        XCTAssertTrue(model.events.allSatisfy { $0.eventLevel == .warning })
        XCTAssertFalse(model.isReloadingEvents)
    }

    func testMoreLoadsOlderEventsWithoutDuplicates() async throws {
        let (model, backend, _) = try await started()
        var week = model.filter
        week.range = .lastWeek
        await model.setFilter(week)
        XCTAssertTrue(model.eventsTruncated)
        XCTAssertEqual(model.events.count, 200)
        let oldest = try XCTUnwrap(model.events.last)
        await model.loadMoreEvents()
        XCTAssertEqual(backend.eventQueries.last?.until, oldest.time.date)
        XCTAssertGreaterThan(model.events.count, 200)
        XCTAssertEqual(Set(model.events.map(\.recordID)).count, model.events.count)
        let times = model.events.map(\.time.date)
        XCTAssertEqual(times, times.sorted(by: >))
        XCTAssertFalse(model.eventsTruncated)
    }

    func testAFilterChangeDuringMoreLetsPagingResume() async throws {
        let (model, backend, _) = try await started()
        var week = model.filter
        week.range = .lastWeek
        await model.setFilter(week)
        XCTAssertTrue(model.eventsTruncated)
        backend.holdEventQueries(true)
        let more = Task { await model.loadMoreEvents() }
        try await waitUntil { backend.pendingEventQueryCount == 1 }
        XCTAssertTrue(model.isLoadingMore)
        var noVerbose = week
        noVerbose.levels = [.critical, .error, .warning, .information]
        let change = Task { await model.setFilter(noVerbose) }
        try await waitUntil { backend.pendingEventQueryCount == 2 }
        backend.releaseEventQuery()
        await more.value
        backend.releaseEventQuery()
        await change.value
        backend.holdEventQueries(false)
        XCTAssertFalse(model.isLoadingMore)
        XCTAssertTrue(model.eventsTruncated)
        let count = model.events.count
        await model.loadMoreEvents()
        XCTAssertGreaterThan(model.events.count, count)
    }

    func testMoreKeepsTheTimeRangeOfTheFirstPage() async throws {
        let (model, backend, _) = try await started()
        var week = model.filter
        week.range = .lastWeek
        await model.setFilter(week)
        let first = try XCTUnwrap(backend.eventQueries.last)
        try await Task.sleep(for: .milliseconds(20))
        await model.loadMoreEvents()
        let more = try XCTUnwrap(backend.eventQueries.last)
        XCTAssertEqual(more.since, first.since)
        XCTAssertNotEqual(more.until, first.until)
    }

    func testSummaryReloadsShareOneRequestAndAFailureKeepsTheLastSummary() async throws {
        let (model, backend, _) = try await started()
        let calls = backend.systemCalls
        let loadedAt = try XCTUnwrap(model.systemUpdatedAt)
        backend.holdSystem(true)
        let first = Task { await model.reloadSystem() }
        try await waitUntil { backend.pendingSystemCount == 1 }
        let second = Task { await model.reloadSystem() }
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(backend.pendingSystemCount, 1)
        backend.holdSystem(false)
        backend.releaseSystem()
        await first.value
        await second.value
        XCTAssertEqual(backend.systemCalls, calls + 1)

        backend.failSystem(with: OperationError(code: "TIMEOUT", message: "응답이 없습니다"))
        await model.reloadSystem()
        XCTAssertNotNil(model.system)
        XCTAssertEqual(model.systemRefreshError?.code, "TIMEOUT")
        XCTAssertGreaterThanOrEqual(try XCTUnwrap(model.systemUpdatedAt), loadedAt)
        backend.failSystem(with: nil)
        await model.reloadSystem()
        XCTAssertNil(model.systemRefreshError)
    }

    func testLevelTogglesKeepAtLeastOneLevel() async throws {
        let (model, _, _) = try await started()
        await model.toggleLevel(.information)
        XCTAssertEqual(model.filter.levels, [.critical, .error, .warning, .verbose])
        await model.toggleLevel(.error, solo: true)
        XCTAssertEqual(model.filter.levels, [.error])
        await model.toggleLevel(.error)
        XCTAssertEqual(model.filter.levels, [.error])
        await model.toggleLevel(.error, solo: true)
        XCTAssertEqual(model.filter.levels, Set(EventLevel.allCases))
    }

    func testSearchFiltersLoadedEventsAndKeepsTheSelection() async throws {
        let (model, _, _) = try await started()
        let target = try XCTUnwrap(model.events.first { $0.provider == "Service Control Manager" })
        model.selectedRecordID = target.recordID
        model.search = "service control"
        XCTAssertFalse(model.visibleEvents.isEmpty)
        XCTAssertTrue(model.visibleEvents.allSatisfy { $0.provider == "Service Control Manager" })
        XCTAssertEqual(model.selectedEvent?.recordID, target.recordID)
        model.search = "7036"
        XCTAssertTrue(model.visibleEvents.allSatisfy { $0.eventID == 7036 })
        model.search = "Windows Update 서비스"
        XCTAssertTrue(model.visibleEvents.allSatisfy { $0.message.contains("Windows Update 서비스") })
    }

    func testProvidersComeFromUnfilteredLoadsOfEachLog() async throws {
        let (model, _, _) = try await started()
        XCTAssertTrue(model.providers.contains("Service Control Manager"))
        var filter = model.filter
        filter.provider = "disk"
        await model.setFilter(filter)
        XCTAssertTrue(model.events.allSatisfy { $0.provider == "disk" })
        XCTAssertTrue(model.providers.contains("Service Control Manager"))
        filter.provider = nil
        filter.log = .application
        await model.setFilter(filter)
        XCTAssertTrue(model.providers.contains("Application Error"))
        XCTAssertFalse(model.providers.contains("Service Control Manager"))
    }

    // MARK: Crash dumps

    func testDownloadPullsIntoTheDeskhandDownloadsFolderUnderAFreeName() async throws {
        let (model, backend, store) = try await started()
        defer { withExtendedLifetime(store) {} }
        let dump = try XCTUnwrap(model.crashes.first)
        await model.download(dump)
        let request = try XCTUnwrap(backend.transferRequests.last)
        XCTAssertEqual(request.direction, .pull)
        XCTAssertEqual(request.remote, dump.path)
        XCTAssertEqual(request.local, FilesPreferences.defaultDownloadFolder + "/Shop.exe.4013.dmp")
        XCTAssertEqual(request.onConflict, .rename)
        let transfer = try XCTUnwrap(model.transfer(for: dump))
        XCTAssertEqual(transfer.remote, dump.path)
        XCTAssertNil(model.downloadErrors[dump.path])
        XCTAssertFalse(FileManager.default.fileExists(atPath: request.local))
    }
}
