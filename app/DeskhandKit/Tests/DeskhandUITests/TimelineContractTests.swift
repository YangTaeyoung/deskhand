import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandUI

/// `timeline.list{host_id, since?, limit?, cursor?}` as the daemon reads it: `since` is an
/// RFC 3339 time, `cursor` the previous `next_cursor` (local-api.md §6, §7).
@MainActor
final class TimelineContractTests: XCTestCase {
    func testSinceIsSentAsAnRFC3339TimeAndTheCursorPagesOlderEntries() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let page = try DaemonFixture.line("timeline-page1")
        daemon.setHandler { request in request.method == "timeline.list" ? .result(page) : nil }
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path)
        try await client.connect(kind: "app", name: "test", version: "0")
        let backend = LiveBackend(client: client)

        let since = try XCTUnwrap(Timestamp(string: "2026-10-21T02:04:01.000Z")).date
        let first = try await backend.timelineList(hostID: "pc", since: since, limit: 2, cursor: nil)
        XCTAssertEqual(first.entries.map(\.seq), [5, 4])
        XCTAssertEqual(first.nextCursor, "4")
        let sent = try XCTUnwrap(daemon.requests(method: "timeline.list").first)
        XCTAssertEqual(sent.params["since"] as? String, "2026-10-21T02:04:01.000Z")
        XCTAssertEqual(sent.params["limit"] as? Int, 2)
        XCTAssertNil(sent.params["cursor"])

        _ = try await backend.timelineList(hostID: "pc", since: nil, limit: 2, cursor: "4")
        let older = try XCTUnwrap(daemon.requests(method: "timeline.list").last)
        XCTAssertEqual(older.params["cursor"] as? String, "4")
        XCTAssertNil(older.params["since"])
        await client.close()
    }

    func testThePreviewKeepsEntriesFromSince() async throws {
        let backend = PreviewBackend(stepDelay: .zero)
        let all = try await backend.timelineList(hostID: PreviewFixtures.devPCID, since: nil, limit: 1_000, cursor: nil).entries
        let middle = try XCTUnwrap(all.dropFirst(all.count / 2).first)
        let recent = try await backend.timelineList(hostID: PreviewFixtures.devPCID, since: middle.ts.date, limit: 1_000, cursor: nil).entries
        XCTAssertFalse(recent.isEmpty)
        XCTAssertTrue(recent.allSatisfy { $0.ts.date >= middle.ts.date })
        XCTAssertLessThan(recent.count, all.count)
    }
}
