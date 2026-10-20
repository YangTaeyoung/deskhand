import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class TransferRetentionTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    private func transfer(_ id: String, state: String, daysAgo: Double) -> Transfer {
        Transfer(transferID: id, hostID: PreviewFixtures.devPCID, direction: "pull", local: "/tmp/\(id)", remote: "C:\\\(id)",
                 onConflict: "fail", state: state, bytesDone: 1, bytesTotal: 1, filesDone: 1, filesTotal: 1, rateBps: 0,
                 updatedAt: Timestamp(date: Date(timeIntervalSinceNow: -daysAgo * 86_400)))
    }

    func testFinishedTransfersOlderThanTheRetentionLeaveTheQueue() {
        let queue = TransferQueue(hostID: PreviewFixtures.devPCID, backend: PreviewBackend(scenario: .standard, stepDelay: .zero))
        queue.retention = { .week }
        queue.seed([
            transfer("old-done", state: "done", daysAgo: 9),
            transfer("old-failed", state: "failed", daysAgo: 8),
            transfer("recent-done", state: "done", daysAgo: 2),
            transfer("old-running", state: "running", daysAgo: 12),
            transfer("old-paused", state: "paused", daysAgo: 20),
        ])

        XCTAssertEqual(queue.visible.map(\.transferID), ["recent-done", "old-running", "old-paused"])

        queue.retention = { .month }
        XCTAssertEqual(queue.visible.map(\.transferID), ["old-done", "old-failed", "recent-done", "old-running", "old-paused"])
    }

    func testFilesModelQueueFollowsThePreference() throws {
        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        let host = try XCTUnwrap(PreviewFixtures.hosts().first { $0.hostID == PreviewFixtures.devPCID })
        let queue = store.files(for: host).transfers
        queue.seed([transfer("old-done", state: "done", daysAgo: 3)])

        store.filesPreferences.finishedTransferRetention = .day
        XCTAssertEqual(queue.visible.map(\.transferID), [])
        store.filesPreferences.finishedTransferRetention = .week
        XCTAssertEqual(queue.visible.map(\.transferID), ["old-done"])
    }

    func testRetentionDefaultsToThirtyDays() {
        XCTAssertEqual(FilesPreferences(defaults: defaults).finishedTransferRetention, .month)
    }

    func testDownloadFolderFallsBackToTheDefault() {
        let preferences = FilesPreferences(defaults: defaults)
        XCTAssertEqual(preferences.downloadFolder, FilesPreferences.defaultDownloadFolder)
        preferences.customDownloadFolder = "/Volumes/Work/Inbox"
        XCTAssertEqual(FilesPreferences(defaults: defaults).downloadFolder, "/Volumes/Work/Inbox")
        preferences.customDownloadFolder = nil
        XCTAssertEqual(FilesPreferences(defaults: defaults).downloadFolder, FilesPreferences.defaultDownloadFolder)
    }

    func testLogsDownloadIntoTheChosenFolder() throws {
        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        let host = try XCTUnwrap(PreviewFixtures.hosts().first { $0.hostID == PreviewFixtures.devPCID })
        store.filesPreferences.customDownloadFolder = "/Volumes/Work/Inbox"
        XCTAssertEqual(store.logs(for: host).downloadFolder, "/Volumes/Work/Inbox")
    }
}
