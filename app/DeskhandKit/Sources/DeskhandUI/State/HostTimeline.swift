import DeskhandClient
import Foundation

/// One page of `timeline.list`: entries newest first and the cursor of the next older page.
public struct TimelinePage: Sendable, Equatable {
    public var entries: [TimelineEntry]
    /// `next_cursor`: present while older entries remain.
    public var nextCursor: String?

    public init(entries: [TimelineEntry], nextCursor: String? = nil) {
        self.entries = entries
        self.nextCursor = nextCursor
    }
}

/// One timeline row with whether its rail continues to the row below.
public struct TimelineRowItem: Sendable, Equatable, Identifiable {
    public let entry: TimelineEntry
    public let continuesRail: Bool

    public var id: Int { entry.seq }
}

/// Timeline rows of one calendar day, newest first.
public struct TimelineDay: Sendable, Equatable, Identifiable {
    public let day: Date
    public let isFirst: Bool
    public let rows: [TimelineRowItem]

    public var id: Date { day }
}

/// The timeline of one computer as the inspector shows it: entries newest first, at most
/// `capacity` of them, the cursor of the older entries not held, and the entries grouped by day,
/// regrouped only when the entries change.
public struct HostTimeline: Sendable {
    /// Entries kept per computer unless the person loads older ones.
    public static let defaultCapacity = 1000
    /// Entries asked for per page.
    public static let pageSize = 200

    public private(set) var entries: [TimelineEntry] = []
    public private(set) var days: [TimelineDay] = []
    /// `next_cursor` for the entries older than the oldest held, or nil when none are older.
    public private(set) var olderCursor: String?
    /// True once a first page has been applied.
    public private(set) var loaded = false
    /// An older page is being read.
    public var isLoadingOlder = false
    /// Why the last older page could not be read.
    public var olderError: OperationError?
    private(set) var capacity: Int
    private let calendar: Calendar

    public init(capacity: Int = HostTimeline.defaultCapacity, calendar: Calendar = .autoupdatingCurrent) {
        self.capacity = capacity
        self.calendar = calendar
    }

    public var hasOlder: Bool { olderCursor != nil }

    /// Puts `entry` in seq order, replacing an entry with the same seq, and drops the oldest
    /// beyond the capacity.
    public mutating func insert(_ entry: TimelineEntry) {
        let index = insertionIndex(for: entry.seq)
        if index < entries.count, entries[index].seq == entry.seq {
            entries[index] = entry
        } else {
            entries.insert(entry, at: index)
        }
        trim()
        regroup()
    }

    /// Applies the newest page. A page that does not reach the held entries replaces them, so
    /// no gap hides between the two; otherwise the page merges in and the older cursor stays.
    public mutating func applyFirstPage(_ page: TimelinePage) {
        let reaches = page.nextCursor == nil || entries.isEmpty
            || (page.entries.last.map { oldest in entries[0].seq >= oldest.seq } ?? true)
        if reaches {
            let heldOlder = entries.last.map { held in page.entries.last.map { held.seq < $0.seq } ?? true } ?? false
            entries = Self.merged(entries, page.entries)
            if !heldOlder { olderCursor = page.nextCursor }
        } else {
            entries = page.entries
            olderCursor = page.nextCursor
        }
        loaded = true
        trim()
        regroup()
    }

    /// Appends a page older than the held entries and keeps it: the capacity grows to hold it.
    public mutating func applyOlderPage(_ page: TimelinePage) {
        entries = Self.merged(entries, page.entries)
        olderCursor = page.nextCursor
        capacity = max(capacity, entries.count)
        isLoadingOlder = false
        olderError = nil
        regroup()
    }

    /// Index where `seq` belongs in the newest-first entries.
    private func insertionIndex(for seq: Int) -> Int {
        var low = 0
        var high = entries.count
        while low < high {
            let mid = (low + high) / 2
            if entries[mid].seq > seq { low = mid + 1 } else { high = mid }
        }
        return low
    }

    private mutating func trim() {
        guard entries.count > capacity else { return }
        entries.removeLast(entries.count - capacity)
        if let oldest = entries.last { olderCursor = String(oldest.seq) }
    }

    private mutating func regroup() {
        var groups: [(Date, [TimelineEntry])] = []
        for entry in entries {
            let day = calendar.startOfDay(for: entry.ts.date)
            if let last = groups.last, last.0 == day {
                groups[groups.count - 1].1.append(entry)
            } else {
                groups.append((day, [entry]))
            }
        }
        days = groups.enumerated().map { index, group in
            let rows = group.1.enumerated().map { TimelineRowItem(entry: $1, continuesRail: $0 < group.1.count - 1) }
            return TimelineDay(day: group.0, isFirst: index == 0, rows: rows)
        }
    }

    /// Union of two newest-first lists by seq, newest first; `incoming` wins on equal seqs.
    static func merged(_ current: [TimelineEntry], _ incoming: [TimelineEntry]) -> [TimelineEntry] {
        var result: [TimelineEntry] = []
        result.reserveCapacity(current.count + incoming.count)
        var i = 0
        var j = 0
        while i < current.count || j < incoming.count {
            if j == incoming.count || (i < current.count && current[i].seq > incoming[j].seq) {
                result.append(current[i])
                i += 1
            } else {
                if i < current.count, current[i].seq == incoming[j].seq { i += 1 }
                result.append(incoming[j])
                j += 1
            }
        }
        return result
    }
}
