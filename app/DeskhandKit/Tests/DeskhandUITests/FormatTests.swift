@testable import DeskhandUI
import XCTest

final class FormatTests: XCTestCase {
    private var calendar: Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "Asia/Seoul")!
        calendar.locale = Locale(identifier: "ko_KR")
        return calendar
    }

    private func date(_ year: Int, _ month: Int, _ day: Int, _ hour: Int, _ minute: Int, _ second: Int = 0) -> Date {
        calendar.date(from: DateComponents(year: year, month: month, day: day, hour: hour, minute: minute, second: second))!
    }

    // MARK: Sizes

    func testSizesUseBase1024OneDecimalAndASpacedUnitLikeTheDaemon() {
        XCTAssertEqual(DS.Format.bytes(0), "0 B")
        XCTAssertEqual(DS.Format.bytes(-5), "0 B")
        XCTAssertEqual(DS.Format.bytes(563), "563 B")
        XCTAssertEqual(DS.Format.bytes(1023), "1023 B")
        XCTAssertEqual(DS.Format.bytes(1024), "1.0 KB")
        XCTAssertEqual(DS.Format.bytes(1280), "1.2 KB", "an exact tie rounds to even")
        XCTAssertEqual(DS.Format.bytes(2184), "2.1 KB")
        XCTAssertEqual(DS.Format.bytes(196_608), "192.0 KB")
        XCTAssertEqual(DS.Format.bytes(1_048_575), "1024.0 KB", "the unit follows the byte count, not the rounded value")
        XCTAssertEqual(DS.Format.bytes(2_516_992), "2.4 MB")
        XCTAssertEqual(DS.Format.bytes(5_368_709_120), "5.0 GB")
        XCTAssertEqual(DS.Format.bytes(3 << 40), "3.0 TB")
        XCTAssertEqual(DS.Format.bytes(1 << 40), "1.0 TB")
        XCTAssertEqual(DS.Format.bytes((1 << 40) - 1), "1024.0 GB", "TB starts at 1024 GB, as the daemon's byteSize")
        XCTAssertEqual(DS.Format.bytes(2_000_398_934_016), "1.8 TB")
    }

    func testMemoryInKilobytesUsesTheSizeRule() {
        XCTAssertEqual(DS.Format.memory(kb: 302 * 1024), "302.0 MB")
        XCTAssertEqual(DS.Format.memory(kb: 1_677_722), "1.6 GB")
        XCTAssertEqual(DS.Format.memory(kb: 144), "144.0 KB")
        XCTAssertEqual(DS.Format.memory(kb: 420_045), "410.2 MB")
    }

    func testRatesAndExactCounts() {
        XCTAssertEqual(DS.Format.rate(44_144_640), "42.1 MB/s")
        XCTAssertEqual(DS.Format.rate(900), "900 B/s")
        XCTAssertEqual(DS.Format.exactBytes(88_301_568), "88,301,568바이트")
        XCTAssertEqual(DS.Format.count(3000), "3,000")
    }

    func testPercentRoundsDownByDefaultAndKeepsRequestedDecimals() {
        XCTAssertEqual(DS.Format.percent(0.389), "38%")
        XCTAssertEqual(DS.Format.percent(0.999), "99%")
        XCTAssertEqual(DS.Format.percent(1), "100%")
        XCTAssertEqual(DS.Format.percent(points: 12.44, decimals: 1), "12.4%")
        XCTAssertEqual(DS.Format.percent(points: 23.4), "23%")
    }

    // MARK: Times

    func testClockFollowsThe24HourSetting() {
        let evening = date(2026, 10, 20, 17, 53, 20)
        let morning = date(2026, 10, 20, 9, 5, 7)
        XCTAssertEqual(DS.Format.clock(evening, hour24: true, calendar: calendar), "17:53")
        XCTAssertEqual(DS.Format.clock(morning, hour24: true, calendar: calendar), "09:05")
        XCTAssertEqual(DS.Format.clock(evening, seconds: true, hour24: true, calendar: calendar), "17:53:20")
        XCTAssertEqual(DS.Format.clock(evening, hour24: false, calendar: calendar), "오후 5:53")
        XCTAssertEqual(DS.Format.clock(morning, seconds: true, hour24: false, calendar: calendar), "오전 9:05:07")
    }

    func testDateTimeSaysTodayAndYesterdayInKoreanThenTheDate() {
        let now = date(2026, 10, 20, 18, 0)
        XCTAssertEqual(DS.Format.dateTime(date(2026, 10, 20, 15, 12), now: now, hour24: true, calendar: calendar), "오늘 15:12")
        XCTAssertEqual(DS.Format.dateTime(date(2026, 10, 19, 6, 40), now: now, hour24: false, calendar: calendar), "어제 오전 6:40")
        XCTAssertEqual(DS.Format.dateTime(date(2026, 10, 3, 15, 12), now: now, hour24: true, calendar: calendar), "10월 3일 15:12")
        XCTAssertEqual(DS.Format.dateTime(date(2025, 12, 31, 23, 59, 9), seconds: true, now: now, hour24: true, calendar: calendar),
                       "2025년 12월 31일 23:59:09")
        XCTAssertEqual(DS.Format.dateTime(date(2026, 10, 20, 15, 12), relative: false, now: now, hour24: true, calendar: calendar),
                       "10월 20일 15:12")
    }

    func testDayTitlesUseTheRelativeWordsThenTheWeekday() {
        let now = date(2026, 10, 20, 18, 0)
        XCTAssertEqual(DS.Format.day(date(2026, 10, 20, 1, 0), now: now, calendar: calendar), "오늘")
        XCTAssertEqual(DS.Format.day(date(2026, 10, 19, 23, 0), now: now, calendar: calendar), "어제")
        XCTAssertEqual(DS.Format.day(date(2026, 10, 16, 9, 0), now: now, calendar: calendar), "10월 16일 금요일")
        XCTAssertEqual(DS.Format.day(date(2025, 10, 16, 9, 0), now: now, calendar: calendar), "2025년 10월 16일 목요일")
    }

    func testFullDateTimeAlwaysCarriesTheYearAndSeconds() {
        XCTAssertEqual(DS.Format.fullDateTime(date(2026, 10, 20, 9, 12, 3), hour24: true, calendar: calendar), "2026년 10월 20일 09:12:03")
        XCTAssertEqual(DS.Format.fullDateTime(date(2026, 10, 20, 21, 12, 3), hour24: false, calendar: calendar),
                       "2026년 10월 20일 오후 9:12:03")
    }

    // MARK: Durations

    func testElapsedDurationsUseSecondsThroughout() {
        XCTAssertEqual(DS.Format.elapsed(ms: 3), "0.1초 미만")
        XCTAssertEqual(DS.Format.elapsed(ms: 420), "0.4초")
        XCTAssertEqual(DS.Format.elapsed(ms: 12_300), "12초")
        XCTAssertEqual(DS.Format.elapsed(ms: 72_000), "1분 12초")
        XCTAssertEqual(DS.Format.elapsed(ms: 3_720_000), "1시간 2분")
    }

    func testUptimeAndRemainingTime() {
        XCTAssertEqual(DS.Format.uptime(seconds: 190_800), "2일 5시간")
        XCTAssertEqual(DS.Format.uptime(seconds: 24_000), "6시간 40분")
        XCTAssertEqual(DS.Format.uptime(seconds: 30), "1분")
        XCTAssertEqual(DS.Format.remaining(seconds: 8.2), "약 9초")
        XCTAssertEqual(DS.Format.remaining(seconds: 125), "약 2분 5초")
    }
}
