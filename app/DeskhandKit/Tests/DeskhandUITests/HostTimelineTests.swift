import DeskhandClient
@testable import DeskhandUI
import XCTest

final class HostTimelineTests: XCTestCase {
    private let base = Date(timeIntervalSince1970: 1_760_900_000)

    private func entry(_ seq: Int, hoursAgo: Double = 0) -> TimelineEntry {
        TimelineEntry(seq: seq, ts: Timestamp(date: base.addingTimeInterval(-hoursAgo * 3600 + Double(seq))), hostID: "h",
                      actor: "ai", kind: "input", summary: "#\(seq)", outcome: "ok")
    }

    func testInsertKeepsNewestFirstAndReplacesTheSameSeq() {
        var timeline = HostTimeline()
        for seq in [5, 9, 1, 7, 3] { timeline.insert(entry(seq)) }
        XCTAssertEqual(timeline.entries.map(\.seq), [9, 7, 5, 3, 1])
        var changed = entry(7)
        changed.summary = "바뀜"
        timeline.insert(changed)
        XCTAssertEqual(timeline.entries.map(\.seq), [9, 7, 5, 3, 1])
        XCTAssertEqual(timeline.entries[1].summary, "바뀜")
    }

    func testInsertBeyondTheCapacityDropsTheOldestAndOffersThemAgain() {
        var timeline = HostTimeline(capacity: 3)
        timeline.applyFirstPage(TimelinePage(entries: [entry(3), entry(2), entry(1)], nextCursor: nil))
        XCTAssertFalse(timeline.hasOlder)
        timeline.insert(entry(4))
        XCTAssertEqual(timeline.entries.map(\.seq), [4, 3, 2])
        XCTAssertEqual(timeline.olderCursor, "2")
        XCTAssertTrue(timeline.hasOlder)
    }

    func testAnOlderPageAppendsAndKeepsWhatThePersonLoaded() {
        var timeline = HostTimeline(capacity: 3)
        timeline.applyFirstPage(TimelinePage(entries: [entry(9), entry(8), entry(7)], nextCursor: "7"))
        timeline.applyOlderPage(TimelinePage(entries: [entry(6), entry(5)], nextCursor: "5"))
        XCTAssertEqual(timeline.entries.map(\.seq), [9, 8, 7, 6, 5])
        XCTAssertEqual(timeline.olderCursor, "5")
        timeline.insert(entry(10))
        XCTAssertEqual(timeline.entries.map(\.seq), [10, 9, 8, 7, 6], "the capacity grew to what was loaded")
        timeline.applyOlderPage(TimelinePage(entries: [entry(4)], nextCursor: nil))
        XCTAssertEqual(timeline.entries.last?.seq, 4)
        XCTAssertFalse(timeline.hasOlder)
    }

    func testAFirstPageThatDoesNotReachTheKnownEntriesReplacesThem() {
        var timeline = HostTimeline()
        timeline.applyFirstPage(TimelinePage(entries: [entry(3), entry(2)], nextCursor: nil))
        timeline.applyFirstPage(TimelinePage(entries: [entry(50), entry(49)], nextCursor: "49"))
        XCTAssertEqual(timeline.entries.map(\.seq), [50, 49])
        XCTAssertEqual(timeline.olderCursor, "49")
    }

    func testAFirstPageThatOverlapsMergesAndKeepsTheOlderCursor() {
        var timeline = HostTimeline()
        timeline.applyFirstPage(TimelinePage(entries: [entry(9), entry(8)], nextCursor: "8"))
        timeline.applyOlderPage(TimelinePage(entries: [entry(7), entry(6)], nextCursor: "6"))
        timeline.insert(entry(11))
        timeline.applyFirstPage(TimelinePage(entries: [entry(11), entry(10), entry(9)], nextCursor: "9"))
        XCTAssertEqual(timeline.entries.map(\.seq), [11, 10, 9, 8, 7, 6])
        XCTAssertEqual(timeline.olderCursor, "6")
    }

    func testDaysAreGroupedOnceWithTheRailOnAllButTheLastRow() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        var timeline = HostTimeline(calendar: calendar)
        timeline.applyFirstPage(TimelinePage(entries: [entry(4), entry(3), entry(2, hoursAgo: 30), entry(1, hoursAgo: 30)], nextCursor: nil))
        XCTAssertEqual(timeline.days.map { $0.rows.map(\.entry.seq) }, [[4, 3], [2, 1]])
        XCTAssertEqual(timeline.days.map { $0.rows.map(\.continuesRail) }, [[true, false], [true, false]])
        XCTAssertEqual(timeline.days.map(\.isFirst), [true, false])
        timeline.insert(entry(5))
        XCTAssertEqual(timeline.days.first?.rows.map(\.entry.seq), [5, 4, 3])
    }
}
