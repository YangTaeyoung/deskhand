import Foundation

extension DS {
    /// Sizes, counts, times and durations as every tab writes them. Sizes follow the daemon's QA
    /// `byteSize`; times follow the system 12/24-hour setting with Korean relative words.
    public enum Format {
        /// Korean, the language of the interface, for numbers, day periods and weekdays.
        static let locale = Locale(identifier: "ko_KR")

        /// True when the system clock setting is 24-hour, read each time it is asked.
        public static var uses24HourClock: Bool {
            switch Locale.autoupdatingCurrent.hourCycle {
            case .zeroToTwentyThree, .oneToTwentyFour: true
            case .zeroToEleven, .oneToTwelve: false
            @unknown default: false
            }
        }

        // MARK: Sizes and counts

        /// `563 B`, `2.1 KB`, `2.4 MB`, `5.0 GB`: under 1024 bytes the whole count and ` B`; otherwise the
        /// count divided by 1024 (KB), 1024² (MB) or 1024³ (GB) — the largest whose power the count
        /// reaches — with one decimal as printf `%.1f` rounds it, a space and the unit.
        public static func bytes(_ count: Int) -> String {
            let value = max(count, 0)
            if value < 1024 { return String(value) + " B" }
            let units: [(Double, String)] = [(1_073_741_824, "GB"), (1_048_576, "MB"), (1024, "KB")]
            let (divisor, unit) = units.first { Double(value) >= $0.0 } ?? units[2]
            return String(format: "%.1f", Double(value) / divisor) + " " + unit
        }

        /// `bytes(_:)` of a size in kilobytes, such as a process's working set.
        public static func memory(kb: Int) -> String {
            bytes(kb * 1024)
        }

        /// `42.1 MB/s`.
        public static func rate(_ bytesPerSecond: Int) -> String {
            let value = bytes(bytesPerSecond)
            return .ui("rate.perSecond", "\(value)/s")
        }

        /// `88,301,568바이트`.
        public static func exactBytes(_ count: Int) -> String {
            let text = count.formatted(.number.locale(locale))
            return .ui("size.exact", "\(text)바이트")
        }

        /// `3,000`.
        public static func count(_ value: Int) -> String {
            value.formatted(.number.locale(locale))
        }

        /// `12.4` with `decimals` digits after the point and no grouping.
        public static func decimal(_ value: Double, decimals: Int = 1) -> String {
            value.formatted(.number.precision(.fractionLength(decimals)).grouping(.never).locale(locale))
        }

        /// `38%` of a fraction from 0 to 1, rounded down so a transfer never shows 100% early.
        public static func percent(_ fraction: Double) -> String {
            (floor(fraction * 100) / 100).formatted(.percent.precision(.fractionLength(0)).locale(locale))
        }

        /// `23%`, `12.4%` of a value in percentage points, rounded to `decimals`.
        public static func percent(points: Double, decimals: Int = 0) -> String {
            (points / 100).formatted(.percent.precision(.fractionLength(decimals)).locale(locale))
        }

        // MARK: Times

        /// `17:53` or `오후 5:53`, with seconds when `seconds` is true.
        public static func clock(_ date: Date, seconds: Bool = false, hour24: Bool = uses24HourClock,
                                 calendar: Calendar = .autoupdatingCurrent) -> String {
            let zone = calendar.timeZone
            let gregorian = Calendar(identifier: .gregorian)
            if hour24 {
                let style = seconds
                    ? Date.VerbatimFormatStyle(
                        format: "\(hour: .twoDigits(clock: .twentyFourHour, hourCycle: .zeroBased)):\(minute: .twoDigits):\(second: .twoDigits)",
                        locale: locale, timeZone: zone, calendar: gregorian)
                    : Date.VerbatimFormatStyle(
                        format: "\(hour: .twoDigits(clock: .twentyFourHour, hourCycle: .zeroBased)):\(minute: .twoDigits)",
                        locale: locale, timeZone: zone, calendar: gregorian)
                return date.formatted(style)
            }
            let style = seconds
                ? Date.VerbatimFormatStyle(
                    format: "\(dayPeriod: .standard(.abbreviated)) \(hour: .defaultDigits(clock: .twelveHour, hourCycle: .oneBased)):\(minute: .twoDigits):\(second: .twoDigits)",
                    locale: locale, timeZone: zone, calendar: gregorian)
                : Date.VerbatimFormatStyle(
                    format: "\(dayPeriod: .standard(.abbreviated)) \(hour: .defaultDigits(clock: .twelveHour, hourCycle: .oneBased)):\(minute: .twoDigits)",
                    locale: locale, timeZone: zone, calendar: gregorian)
            return date.formatted(style)
        }

        /// `오늘 15:12`, `어제 오전 6:40`, `10월 3일 15:12`, or `2025년 12월 31일 23:59` in another
        /// year. With `relative` false the date is always written out.
        public static func dateTime(_ date: Date, seconds: Bool = false, relative: Bool = true, now: Date = .now,
                                    hour24: Bool = uses24HourClock, calendar: Calendar = .autoupdatingCurrent) -> String {
            let time = clock(date, seconds: seconds, hour24: hour24, calendar: calendar)
            if relative {
                if calendar.isDate(date, inSameDayAs: now) { return .ui("date.today", "오늘 \(time)") }
                if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
                    return .ui("date.yesterday", "어제 \(time)")
                }
            }
            let day = monthDay(date, withYear: !calendar.isDate(date, equalTo: now, toGranularity: .year), calendar: calendar)
            return .ui("date.dayTime", "\(day) \(time)")
        }

        /// `2026년 10월 20일 09:12:03`: the full date and time to the second, for tooltips and copies.
        public static func fullDateTime(_ date: Date, hour24: Bool = uses24HourClock, calendar: Calendar = .autoupdatingCurrent) -> String {
            let day = monthDay(date, withYear: true, calendar: calendar)
            let time = clock(date, seconds: true, hour24: hour24, calendar: calendar)
            return .ui("date.dayTime", "\(day) \(time)")
        }

        /// `오늘`, `어제`, `10월 16일 금요일`, or `2025년 10월 16일 목요일` in another year.
        public static func day(_ date: Date, now: Date = .now, calendar: Calendar = .autoupdatingCurrent) -> String {
            if calendar.isDate(date, inSameDayAs: now) { return .ui("timeline.today", "오늘") }
            if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
                return .ui("timeline.yesterday", "어제")
            }
            let day = monthDay(date, withYear: !calendar.isDate(date, equalTo: now, toGranularity: .year), calendar: calendar)
            let weekday = date.formatted(Date.VerbatimFormatStyle(format: "\(weekday: .wide)", locale: locale,
                                                                  timeZone: calendar.timeZone, calendar: Calendar(identifier: .gregorian)))
            return .ui("date.dayWeekday", "\(day) \(weekday)")
        }

        /// `3분 전`, `어제`.
        public static func relative(_ date: Date, now: Date = .now) -> String {
            let formatter = RelativeDateTimeFormatter()
            formatter.locale = locale
            formatter.dateTimeStyle = .named
            return formatter.localizedString(for: date, relativeTo: now)
        }

        /// `10월 3일` or `2025년 10월 3일`.
        private static func monthDay(_ date: Date, withYear: Bool, calendar: Calendar) -> String {
            let parts = calendar.dateComponents([.year, .month, .day], from: date)
            let month = String(parts.month ?? 1)
            let day = String(parts.day ?? 1)
            if withYear {
                let year = String(parts.year ?? 1)
                return .ui("date.yearMonthDay", "\(year)년 \(month)월 \(day)일")
            }
            return .ui("date.monthDay", "\(month)월 \(day)일")
        }

        // MARK: Durations

        /// `0.1초 미만`, `0.4초`, `12초`, `1분 12초`, `1시간 2분`: how long something took.
        public static func elapsed(ms: Int) -> String {
            if ms < 100 { return .ui("qa.duration.underTenth", "0.1초 미만") }
            if ms < 10_000 {
                let text = String(format: "%.1f", Double(ms) / 1000)
                return .ui("qa.duration.seconds", "\(text)초")
            }
            let total = ms / 1000
            if total < 60 {
                let seconds = String(total)
                return .ui("qa.duration.wholeSeconds", "\(seconds)초")
            }
            if total < 3600 {
                let minutes = String(total / 60)
                let seconds = String(total % 60)
                return .ui("qa.duration.minutes", "\(minutes)분 \(seconds)초")
            }
            let hours = String(total / 3600)
            let minutes = String(total % 3600 / 60)
            return .ui("qa.duration.hours", "\(hours)시간 \(minutes)분")
        }

        /// `elapsed(ms:)` from `start` to `end`.
        public static func elapsed(from start: Date, to end: Date) -> String {
            elapsed(ms: Int(end.timeIntervalSince(start) * 1000))
        }

        /// `2일 5시간`, `6시간 40분`, `12분`: how long a computer has been up, at least a minute.
        public static func uptime(seconds: Int) -> String {
            let total = max(60, seconds)
            let units: Set<Duration.UnitsFormatStyle.Unit> = total >= 86_400 ? [.days, .hours] : (total >= 3_600 ? [.hours, .minutes] : [.minutes])
            return Duration.seconds(total).formatted(.units(allowed: units, width: .narrow, maximumUnitCount: 2).locale(locale))
        }

        /// `약 9초`, `약 2분 5초`, `약 1시간 4분`: time left, rounded up to a whole second.
        public static func remaining(seconds: Double) -> String {
            let total = max(1, Int(seconds.rounded(.up)))
            let units: Set<Duration.UnitsFormatStyle.Unit> = total >= 3600 ? [.hours, .minutes] : [.minutes, .seconds]
            let text = Duration.seconds(total).formatted(.units(allowed: units, width: .abbreviated).locale(locale))
            return .ui("eta.about", "약 \(text)")
        }
    }
}
