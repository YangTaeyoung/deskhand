import AppKit
import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

/// How far back the event log reaches.
public enum EventTimeRange: Hashable, Sendable {
    case lastHour
    case lastDay
    case lastWeek
    case custom(start: Date, end: Date)

    public static let presets: [EventTimeRange] = [.lastHour, .lastDay, .lastWeek]

    public var title: String {
        switch self {
        case .lastHour: .ui("logs.range.hour", "최근 1시간")
        case .lastDay: .ui("logs.range.day", "최근 24시간")
        case .lastWeek: .ui("logs.range.week", "최근 7일")
        case .custom(let start, let end):
            .ui("logs.range.custom", "\(DS.Format.dateTime(start, relative: false)) – \(DS.Format.dateTime(end, relative: false))")
        }
    }

    /// `since` and `until` of the range at `now`.
    public func bounds(now: Date) -> (since: Date, until: Date?) {
        switch self {
        case .lastHour: (now.addingTimeInterval(-3_600), nil)
        case .lastDay: (now.addingTimeInterval(-86_400), nil)
        case .lastWeek: (now.addingTimeInterval(-7 * 86_400), nil)
        case .custom(let start, let end): (start, end)
        }
    }

    public var isCustom: Bool {
        if case .custom = self { return true }
        return false
    }
}

/// What the event log asks the agent for.
public struct EventFilter: Equatable, Sendable {
    public static let pageSize = 200

    public var log: EventLogName = .system
    public var range: EventTimeRange = .lastDay
    /// Never empty.
    public var levels: Set<EventLevel> = Set(EventLevel.allCases)
    /// Exact provider name, or every provider.
    public var provider: String?

    public init(log: EventLogName = .system, range: EventTimeRange = .lastDay, levels: Set<EventLevel> = Set(EventLevel.allCases),
                provider: String? = nil) {
        self.log = log
        self.range = range
        self.levels = levels
        self.provider = provider
    }

    public var allLevels: Bool { levels.count == EventLevel.allCases.count }

    /// The `diag.events` parameters at `now`; `before` limits a page to events no newer than it.
    /// Every level selected sends no `levels`.
    public func query(hostID: String, now: Date = .now, before: Date? = nil) -> EventLogQuery {
        let bounds = range.bounds(now: now)
        var until = bounds.until
        if let before { until = min(until ?? before, before) }
        return EventLogQuery(hostID: hostID, log: log, since: bounds.since, until: until,
                             levels: allLevels ? nil : levels.sorted(), provider: provider, max: Self.pageSize)
    }
}

/// Columns of the event table.
public enum EventColumn: String, CaseIterable, Sendable {
    case level
    case time
    case provider
    case eventID
    case message

    public var title: String {
        switch self {
        case .level: .ui("logs.column.level", "수준")
        case .time: .ui("logs.column.time", "시간")
        case .provider: .ui("logs.column.provider", "공급자")
        case .eventID: .ui("logs.column.eventID", "이벤트 ID")
        case .message: .ui("logs.column.message", "메시지")
        }
    }
}

public struct EventSort: Equatable, Sendable {
    public var column: EventColumn
    public var ascending: Bool

    public init(column: EventColumn, ascending: Bool) {
        self.column = column
        self.ascending = ascending
    }

    public static let newestFirst = EventSort(column: .time, ascending: false)
}

/// Progress of one section of the logs tab.
public enum LoadPhase: Equatable, Sendable {
    case idle
    case loading
    case loaded
    case failed(OperationError)
}

/// The logs tab of one computer: the system summary, the Windows event log with its filters,
/// search, sort, selection and older pages, and the crash dumps with their downloads.
@MainActor
@Observable
public final class LogsModel {
    public enum Section: String, CaseIterable, Identifiable, Sendable {
        case events
        case crashes

        public var id: String { rawValue }

        public var title: String {
            switch self {
            case .events: .ui("logs.section.events", "이벤트 로그")
            case .crashes: .ui("logs.section.crashes", "크래시 덤프")
            }
        }
    }

    /// Where crash dumps and log files are saved: the download folder of the settings.
    public var downloadFolder: String {
        store?.filesPreferences.downloadFolder ?? FilesPreferences.defaultDownloadFolder
    }

    public let hostID: String
    public var section: Section = .events

    public private(set) var system: DiagSystem?
    public private(set) var systemPhase: LoadPhase = .idle
    /// When `system` arrived.
    public private(set) var systemUpdatedAt: Date?
    /// Why the last summary refresh failed while an older summary is shown.
    public private(set) var systemRefreshError: OperationError?

    public private(set) var filter = EventFilter()
    /// Loaded events of `filter`, newest first, each record once.
    public private(set) var events: [EventLogEntry] = []
    /// `events` matching `search`, in `sort` order.
    public private(set) var visibleEvents: [EventLogEntry] = []
    public private(set) var eventsPhase: LoadPhase = .idle
    /// A reload for new filters is in flight while the previous rows stay on screen.
    public private(set) var isReloadingEvents = false
    /// Older events in the range were not returned.
    public private(set) var eventsTruncated = false
    public private(set) var isLoadingMore = false
    public private(set) var loadMoreError: OperationError?
    public private(set) var eventsUpdatedAt: Date?
    /// Providers seen in this log without a provider filter, sorted.
    public private(set) var providers: [String] = []
    public var search = "" {
        didSet { if search != oldValue { rebuildVisible() } }
    }
    public var sort = EventSort.newestFirst {
        didSet { if sort != oldValue { rebuildVisible() } }
    }
    public var selectedRecordID: Int?

    public private(set) var crashes: [CrashDump] = []
    public private(set) var crashesPhase: LoadPhase = .idle

    /// The first read that failed with `CAPABILITY_UNSUPPORTED`: the PC cannot give diagnostics at all.
    public var unsupportedError: OperationError? {
        for phase in [systemPhase, eventsPhase, crashesPhase] {
            if case .failed(let error) = phase, error.code == "CAPABILITY_UNSUPPORTED" { return error }
        }
        return nil
    }
    /// When `crashes` arrived.
    public private(set) var crashesUpdatedAt: Date?
    /// Downloads whose `transfer.start` is in flight, by remote path.
    public private(set) var downloadStarting: Set<String> = []
    /// Downloads that did not start, by remote path.
    public private(set) var downloadErrors: [String: OperationError] = [:]
    public private(set) var notice: DiagnosticsNotice?

    @ObservationIgnored weak var store: AppStore?
    @ObservationIgnored private let backend: any DiagnosticsBackend
    @ObservationIgnored private var started = false
    @ObservationIgnored private var eventsToken = 0
    @ObservationIgnored private var moreToken = 0
    /// The query of the first page of `events`, with its time range fixed.
    @ObservationIgnored private var firstPageQuery: EventLogQuery?
    @ObservationIgnored private var systemInFlight: Task<Void, Never>?
    @ObservationIgnored private var knownProviders: [EventLogName: Set<String>] = [:]
    @ObservationIgnored private var noticeTask: Task<Void, Never>?

    @ObservationIgnored private var systemLoop: RefreshLoop!

    /// How often the summary refreshes while the tab is on screen.
    public static let systemRefreshInterval: Duration = .seconds(5)

    public init(hostID: String, backend: any DiagnosticsBackend,
                wait: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.hostID = hostID
        self.backend = backend
        systemLoop = RefreshLoop(interval: Self.systemRefreshInterval, wait: wait) { [weak self] in
            guard let self, started, Date.now.timeIntervalSince(systemUpdatedAt ?? .distantPast) > 2 else { return }
            await reloadSystem()
        }
    }

    /// Whether the tab is on screen in a visible window and the computer can answer. The system
    /// summary refreshes every five seconds while this holds.
    public func setVisible(_ visible: Bool) {
        systemLoop.setActive(visible)
    }

    public var selectedEvent: EventLogEntry? {
        selectedRecordID.flatMap { id in events.first { $0.recordID == id } }
    }

    // MARK: Lifecycle

    /// Loads every section the first time; later calls do nothing.
    public func start() async {
        guard !started else { return }
        started = true
        if let store, let host = store.host(hostID) { store.files(for: host).transfers.start() }
        await reloadAll()
    }

    /// Reads the summary, the events of the current filters and the crash dumps again.
    public func reloadAll() async {
        async let system: Void = reloadSystem()
        async let events: Void = reloadEvents()
        async let crashes: Void = reloadCrashes()
        _ = await (system, events, crashes)
    }

    /// Marks the model as started so the view loads nothing. Snapshot scenes call it after seeding.
    public func markSeeded() {
        started = true
    }

    // MARK: System

    /// Reads the summary. A call while a read is in flight waits for that read. A failure keeps
    /// an older summary with `systemRefreshError`.
    public func reloadSystem() async {
        if let systemInFlight {
            await systemInFlight.value
            return
        }
        let task = Task { [weak self] in
            guard let self else { return }
            await loadSystem()
            systemInFlight = nil
        }
        systemInFlight = task
        await task.value
    }

    private func loadSystem() async {
        if system == nil { systemPhase = .loading }
        do {
            system = try await backend.diagSystem(hostID: hostID)
            systemUpdatedAt = .now
            systemPhase = .loaded
            systemRefreshError = nil
        } catch is CancellationError {
            if system == nil { systemPhase = .idle }
        } catch {
            if system == nil {
                systemPhase = .failed(OperationError(error))
            } else {
                systemRefreshError = OperationError(error)
            }
        }
    }

    // MARK: Events

    /// Applies new filters and reads the first page for them. Only the reply to the latest
    /// filters is applied.
    public func setFilter(_ filter: EventFilter) async {
        guard filter != self.filter || eventsPhase == .idle else { return }
        var filter = filter
        if filter.levels.isEmpty { filter.levels = Set(EventLevel.allCases) }
        self.filter = filter
        await reloadEvents()
    }

    /// Turns `level` on or off; the last selected level stays on. With `solo`, shows only
    /// `level`, or every level again when it already was the only one.
    public func toggleLevel(_ level: EventLevel, solo: Bool = false) async {
        var next = filter
        if solo {
            next.levels = next.levels == [level] ? Set(EventLevel.allCases) : [level]
        } else if next.levels.contains(level) {
            guard next.levels.count > 1 else { return }
            next.levels.remove(level)
        } else {
            next.levels.insert(level)
        }
        await setFilter(next)
    }

    /// Reads the first page of the current filters.
    public func reloadEvents() async {
        eventsToken += 1
        let token = eventsToken
        let query = filter.query(hostID: hostID)
        if events.isEmpty { eventsPhase = .loading } else { isReloadingEvents = true }
        moreToken += 1
        isLoadingMore = false
        loadMoreError = nil
        do {
            let page = try await backend.diagEvents(query)
            guard token == eventsToken else { return }
            firstPageQuery = query
            events = Self.merge([], page.events)
            eventsTruncated = page.truncated
            eventsUpdatedAt = .now
            eventsPhase = .loaded
            if query.provider == nil {
                knownProviders[query.log, default: []].formUnion(page.events.map(\.provider))
            }
            providers = (knownProviders[filter.log] ?? []).sorted { $0.localizedStandardCompare($1) == .orderedAscending }
            if let id = selectedRecordID, !events.contains(where: { $0.recordID == id }) { selectedRecordID = nil }
            rebuildVisible()
        } catch is CancellationError {
        } catch {
            guard token == eventsToken else { return }
            eventsPhase = .failed(OperationError(error))
            events = []
            rebuildVisible()
        }
        if token == eventsToken { isReloadingEvents = false }
    }

    /// Reads the page of events older than the oldest loaded one, in the time range of the first
    /// page, and adds the new records.
    public func loadMoreEvents() async {
        guard eventsTruncated, !isLoadingMore, let oldest = events.last, let first = firstPageQuery else { return }
        let token = eventsToken
        moreToken += 1
        let mine = moreToken
        isLoadingMore = true
        loadMoreError = nil
        defer { if mine == moreToken { isLoadingMore = false } }
        func query(before date: Date) -> EventLogQuery {
            var query = first
            query.until = min(first.until ?? date, date)
            return query
        }
        do {
            var page = try await backend.diagEvents(query(before: oldest.time.date))
            guard token == eventsToken else { return }
            let known = Set(events.map(\.recordID))
            if page.events.allSatisfy({ known.contains($0.recordID) }) {
                page = try await backend.diagEvents(query(before: oldest.time.date.addingTimeInterval(-0.001)))
                guard token == eventsToken else { return }
            }
            events = Self.merge(events, page.events)
            eventsTruncated = page.truncated
            rebuildVisible()
        } catch is CancellationError {
        } catch {
            guard token == eventsToken else { return }
            loadMoreError = OperationError(error)
        }
    }

    /// Union of two event lists by record, newest first.
    static func merge(_ current: [EventLogEntry], _ incoming: [EventLogEntry]) -> [EventLogEntry] {
        var seen = Set(current.map(\.recordID))
        var all = current
        for event in incoming where seen.insert(event.recordID).inserted { all.append(event) }
        return all.sorted { $0.time.date != $1.time.date ? $0.time.date > $1.time.date : $0.recordID > $1.recordID }
    }

    private func rebuildVisible() {
        let needle = search.trimmingCharacters(in: .whitespacesAndNewlines)
        let matching = needle.isEmpty ? events : events.filter { event in
            event.message.localizedCaseInsensitiveContains(needle) || event.provider.localizedCaseInsensitiveContains(needle)
                || String(event.eventID) == needle
        }
        visibleEvents = sort == .newestFirst ? matching : matching.sorted { Self.precedes($0, $1, by: sort) }
    }

    static func precedes(_ a: EventLogEntry, _ b: EventLogEntry, by sort: EventSort) -> Bool {
        let order: ComparisonResult
        switch sort.column {
        case .level:
            let x = a.eventLevel.map { EventLevel.allCases.firstIndex(of: $0) ?? 9 } ?? 9
            let y = b.eventLevel.map { EventLevel.allCases.firstIndex(of: $0) ?? 9 } ?? 9
            order = x == y ? .orderedSame : (x < y ? .orderedAscending : .orderedDescending)
        case .time, .message:
            order = a.time.date == b.time.date ? .orderedSame : (a.time.date < b.time.date ? .orderedAscending : .orderedDescending)
        case .provider:
            order = a.provider.localizedStandardCompare(b.provider)
        case .eventID:
            order = a.eventID == b.eventID ? .orderedSame : (a.eventID < b.eventID ? .orderedAscending : .orderedDescending)
        }
        if order == .orderedSame { return a.time.date != b.time.date ? a.time.date > b.time.date : a.recordID > b.recordID }
        return sort.ascending ? order == .orderedAscending : order == .orderedDescending
    }

    // MARK: Crash dumps

    public func reloadCrashes() async {
        if crashes.isEmpty { crashesPhase = .loading }
        do {
            crashes = try await backend.diagCrashes(hostID: hostID, since: nil)
            crashesUpdatedAt = .now
            crashesPhase = .loaded
        } catch is CancellationError {
        } catch {
            crashesPhase = .failed(OperationError(error))
        }
    }

    /// The newest pull of `dump` in the computer's transfer queue.
    public func transfer(for dump: CrashDump) -> Transfer? {
        transfer(remotePath: dump.path)
    }

    /// The newest pull of the remote file at `remotePath` in the computer's transfer queue.
    public func transfer(remotePath: String) -> Transfer? {
        guard let store, let host = store.host(hostID) else { return nil }
        return store.files(for: host).transfers.transfers.first { transfer in
            !transfer.transferDirection.isUpload && PathStyle.windows.sameName(transfer.remote, remotePath)
        }
    }

    /// Pulls `dump` into the download folder through the transfer queue, under a free name
    /// when a file of that name is there.
    public func download(_ dump: CrashDump) async {
        await download(remotePath: dump.path)
    }

    /// Pulls the remote file at `remotePath` into the download folder through the transfer
    /// queue, under a free name when a file of that name is there.
    public func download(remotePath: String) async {
        guard let store, let host = store.host(hostID), !downloadStarting.contains(remotePath) else { return }
        downloadStarting.insert(remotePath)
        downloadErrors[remotePath] = nil
        defer { downloadStarting.remove(remotePath) }
        do {
            _ = try await store.files(for: host).download(remotePath: remotePath, toFolder: downloadFolder)
        } catch {
            downloadErrors[remotePath] = OperationError(error)
        }
    }

    /// Opens a downloaded file with its default app on this Mac, or shows it as `reveal` does
    /// when it is not on the disk.
    public func open(_ transfer: Transfer) {
        let path = Self.savedPath(of: transfer)
        if FileManager.default.fileExists(atPath: path) {
            NSWorkspace.shared.open(URL(fileURLWithPath: path))
        } else {
            reveal(transfer)
        }
    }

    /// Pauses, resumes or cancels a download through the transfer queue.
    public func perform(_ action: TransferAction, on transfer: Transfer) {
        guard let store, let host = store.host(hostID) else { return }
        store.files(for: host).transfers.perform(action, on: transfer.transferID)
    }

    /// Where a finished pull wrote its file: the requested path, or the free name it chose.
    public static func savedPath(of transfer: Transfer) -> String {
        guard transfer.transferOutcome == .renamed, transfer.files.count == 1, let rel = transfer.files.first?.rel,
              let folder = FilePath.parent(transfer.local, style: .posix) else { return transfer.local }
        return FilePath.join(folder, rel, style: .posix)
    }

    /// Shows a downloaded file in Finder, or for a file the preview keeps in memory, in the local
    /// pane of the files tab.
    public func reveal(_ transfer: Transfer) {
        let path = Self.savedPath(of: transfer)
        if FileManager.default.fileExists(atPath: path) {
            NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
            return
        }
        guard let store, let host = store.host(hostID) else { return }
        let files = store.files(for: host)
        if files.overlay?.item(at: path) != nil {
            store.layout.tab = .files
            Task {
                await files.start(host: host)
                await files.open(.local, path: FilePath.parent(path, style: .posix) ?? path)
                files.local.selection = [path]
                files.activeSide = .local
            }
            return
        }
        let name = FilePath.lastComponent(path, style: .posix)
        show(.warning, .ui("logs.reveal.missing", "‘\(name)’ 파일을 찾을 수 없습니다"),
             detail: .ui("logs.reveal.missing.detail", "받은 뒤 옮기거나 지웠을 수 있습니다. 다시 받으세요."))
    }

    /// The agent's message and what to do about it, for the error banners of the tab.
    public static func explanation(_ error: OperationError) -> String {
        let fix: String? = switch error.code {
        case "PERMISSION_DENIED", "ACCESS_DENIED":
            .ui("logs.fix.denied", "에이전트가 이 정보를 읽을 권한이 없습니다. 원격 PC에서 이벤트 뷰어로 확인하거나 진단을 실행해 에이전트 권한을 확인하세요.")
        default:
            error.fix
        }
        return [error.message, fix].compactMap { $0?.asSentence }.filter { !$0.isEmpty }.joined(separator: " ")
    }

    // MARK: Notices

    func show(_ tone: DiagnosticsNotice.Tone, _ message: String, detail: String? = nil, code: String? = nil) {
        noticeTask?.cancel()
        let notice = DiagnosticsNotice(tone: tone, message: message, detail: detail, code: code)
        self.notice = notice
        noticeTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(tone == .success ? 4 : 8))
            guard !Task.isCancelled, self?.notice?.id == notice.id else { return }
            self?.notice = nil
        }
    }

    public func dismissNotice() {
        noticeTask?.cancel()
        notice = nil
    }

    // MARK: Seeding

    /// Puts every section in place without asking the backend. Snapshot scenes use it.
    public func seed(system: DiagSystem?, systemPhase: LoadPhase = .loaded, systemRefreshError: OperationError? = nil,
                     filter: EventFilter = EventFilter(),
                     events: [EventLogEntry], eventsPhase: LoadPhase = .loaded, truncated: Bool = false, search: String = "",
                     selectedRecordID: Int? = nil, providers: [String]? = nil, crashes: [CrashDump], crashesPhase: LoadPhase = .loaded,
                     section: Section = .events, at date: Date = .now) {
        self.system = system
        self.systemPhase = systemPhase
        self.systemRefreshError = systemRefreshError
        systemUpdatedAt = system == nil ? nil : date
        self.filter = filter
        self.events = Self.merge([], events)
        self.eventsPhase = eventsPhase
        eventsTruncated = truncated
        eventsUpdatedAt = eventsPhase == .loaded ? date : nil
        self.search = search
        self.providers = providers ?? Set(events.map(\.provider)).sorted { $0.localizedStandardCompare($1) == .orderedAscending }
        self.selectedRecordID = selectedRecordID
        self.crashes = crashes
        self.crashesPhase = crashesPhase
        crashesUpdatedAt = crashesPhase == .loaded ? date : nil
        firstPageQuery = filter.query(hostID: hostID, now: date)
        self.section = section
        rebuildVisible()
        started = true
    }
}
