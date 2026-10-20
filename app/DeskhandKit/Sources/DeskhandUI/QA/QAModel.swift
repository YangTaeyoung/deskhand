import AppKit
import DeskhandClient
import Foundation
import Observation

/// The QA tab: runs of every computer from `qa.list`, kept current by `qa.updated` events (higher
/// rev only, steps by index) and read again with `qa.get` after `events.dropped`, a rev gap, a new
/// event subscription or a daemon reconnect; the selection, starting, cancelling and exporting
/// runs, and the evidence files the viewer shows.
@MainActor
@Observable
public final class QAModel {
    /// The listing of one scope.
    public struct Listing: Equatable, Sendable {
        public var ids: Set<String> = []
        public var nextCursor: String?
        public var phase: LoadPhase = .idle
        public var loadingMore = false
        public var moreError: OperationError?

        public init(ids: Set<String> = [], nextCursor: String? = nil, phase: LoadPhase = .idle, loadingMore: Bool = false,
                    moreError: OperationError? = nil) {
            self.ids = ids
            self.nextCursor = nextCursor
            self.phase = phase
            self.loadingMore = loadingMore
            self.moreError = moreError
        }
    }

    public enum Sheet: Identifiable, Equatable, Sendable {
        case run(QARunDraft)

        public var id: String {
            switch self {
            case .run(let draft): draft.id
            }
        }
    }

    public static let pageSize = 50
    public static let recentLimit = 8
    /// Largest image read for a thumbnail or the viewer.
    public nonisolated static let imageLimit = 32 << 20
    /// Largest part of a text file the viewer reads.
    public nonisolated static let textLimit = 512 << 10
    private static let allHostsKey = "qa.allHosts"
    private static let recentKey = "qa.recentScenarios"
    private static let cachedImages = 64

    public private(set) var records: [String: QARunRecord] = [:]
    public private(set) var listings: [QAScope: Listing] = [:]
    /// The list shows every computer's runs instead of the selected computer's.
    public var showsAllHosts: Bool {
        didSet { defaults.set(showsAllHosts, forKey: Self.allHostsKey) }
    }
    public var filter: QARunFilter = .all
    public var search = ""
    /// The selected run of each scope.
    public var selection: [QAScope: String] = [:]
    /// The step the person selected in each run.
    public var selectedSteps: [String: Int] = [:]
    /// `qa.get` of runs whose steps were asked for.
    public private(set) var stepLoads: [String: LoadPhase] = [:]
    public private(set) var cancels: [String: QACancelProgress] = [:]
    public private(set) var exports: [String: QAExportState] = [:]
    public var sheet: Sheet?
    public var viewer: QAEvidenceSelection?
    public var notice: DiagnosticsNotice?
    public private(set) var recentScenarios: [QARecentScenario] = []
    public private(set) var evidence: [String: QAEvidenceContent] = [:]
    /// Fraction of the detail the step list takes over the step pane.
    public var stepsSplit = 0.46
    /// The run list shows; nil follows the window width.
    public var listShown: Bool?

    @ObservationIgnored let backend: any QABackend & EventsBackend
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private let backoff: AppStore.ResubscribeBackoff
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var reconnectsTask: Task<Void, Never>?
    @ObservationIgnored private var imageOrder: [String] = []
    @ObservationIgnored private var evidenceTasks: Set<String> = []
    /// Resyncs so far.
    @ObservationIgnored private(set) var resyncs = 0

    public init(backend: any QABackend & EventsBackend, defaults: UserDefaults, backoff: AppStore.ResubscribeBackoff = .standard) {
        self.backend = backend
        self.defaults = defaults
        self.backoff = backoff
        showsAllHosts = defaults.bool(forKey: Self.allHostsKey)
        if let data = defaults.data(forKey: Self.recentKey),
           let list = try? JSONDecoder().decode([QARecentScenario].self, from: data) {
            recentScenarios = list
        }
    }

    // MARK: Queries

    public func scope(for hostID: String) -> QAScope {
        showsAllHosts ? .all : .host(hostID)
    }

    public func listing(_ scope: QAScope) -> Listing {
        listings[scope] ?? Listing()
    }

    public func record(_ runID: String) -> QARunRecord? { records[runID] }

    public func run(_ runID: String) -> QARun? { records[runID]?.run }

    /// Runs of the scope, newest first, before the filter and search.
    public func runs(in scope: QAScope) -> [QARun] {
        listing(scope).ids.compactMap { records[$0]?.run }.sorted(by: Self.newestFirst)
    }

    /// Runs of the scope that pass the status filter and the search, newest first. The search
    /// matches the name, the scenario path, the run ID and `hostName`.
    public func visibleRuns(in scope: QAScope, hostName: (String) -> String? = { _ in nil }) -> [QARun] {
        let terms = search.trimmingCharacters(in: .whitespaces).lowercased()
        return runs(in: scope).filter { run in
            guard filter.matches(run.runStatus) else { return false }
            guard !terms.isEmpty else { return true }
            let fields = [run.name, run.scenarioPath ?? "", run.runID, hostName(run.hostID) ?? ""]
            return fields.contains { $0.lowercased().contains(terms) }
        }
    }

    static func newestFirst(_ a: QARun, _ b: QARun) -> Bool {
        a.startedAt.date == b.startedAt.date ? a.runID > b.runID : a.startedAt.date > b.startedAt.date
    }

    /// The selected run of the scope when it is still listed there, otherwise its newest run.
    public func selectedRun(in scope: QAScope) -> QARun? {
        if let id = selection[scope], listing(scope).ids.contains(id), let run = records[id]?.run { return run }
        return runs(in: scope).first
    }

    /// The step the detail shows: the person's choice, otherwise the running step of a running run,
    /// the first failed step, the first blocked one, the first with evidence, or the first step.
    public func focusedStep(of runID: String) -> QAStep? {
        guard let steps = records[runID]?.steps, !steps.isEmpty else { return nil }
        if let chosen = selectedSteps[runID], let step = steps.first(where: { $0.index == chosen }) { return step }
        if let run = records[runID]?.run, run.isRunning, let current = run.currentStep, let step = steps.first(where: { $0.index == current }) {
            return step
        }
        if let failed = steps.first(where: { $0.stepStatus == .fail && !$0.optional }) { return failed }
        if let blocked = steps.first(where: { $0.stepStatus == .blocked && !$0.optional }) { return blocked }
        return steps.first { !$0.evidence.isEmpty } ?? steps.first
    }

    public func cancelState(of runID: String) -> QACancelState {
        guard let record = records[runID] else { return .hidden }
        return QACancelState(run: record.run, phase: record.currentPhase, progress: cancels[runID] ?? QACancelProgress())
    }

    /// The running run of a computer, when the list knows one.
    public func runningRun(hostID: String) -> QARun? {
        records.values.map(\.run).filter { $0.hostID == hostID && $0.isRunning }.sorted(by: Self.newestFirst).first
    }

    // MARK: Lifecycle

    /// Follows `qa.updated` and `events.dropped`. When the stream ends it subscribes again with
    /// backoff and then reads the loaded lists and the runs in view again; it reads them again
    /// whenever the daemon connection comes back.
    public func start() {
        guard eventsTask == nil else { return }
        let backend = backend
        let backoff = backoff
        eventsTask = Task { [weak self] in
            var delay = backoff.initial
            var first = true
            while !Task.isCancelled {
                let stream = backend.events(kinds: EventKind.qaKinds, hostID: nil)
                if !first { await self?.resync() }
                first = false
                var received = false
                do {
                    for try await event in stream {
                        received = true
                        self?.handle(event)
                    }
                } catch {}
                guard !Task.isCancelled else { return }
                if received { delay = backoff.initial }
                try? await Task.sleep(for: delay)
                delay = min(delay * 2, backoff.maximum)
            }
        }
        let reconnects = backend.reconnects()
        reconnectsTask = Task { [weak self] in
            for await _ in reconnects {
                guard !Task.isCancelled else { return }
                await self?.resync()
            }
        }
    }

    public func stop() {
        eventsTask?.cancel()
        eventsTask = nil
        reconnectsTask?.cancel()
        reconnectsTask = nil
    }

    /// Reads the first page of every scope asked for so far, and `qa.get` of every running run and
    /// of the selected runs whose steps were read.
    func resync() async {
        resyncs += 1
        for scope in Array(listings.keys) {
            await load(scope)
        }
        let selected = Set(selection.values)
        let ids = records.values.filter { $0.run.isRunning || ($0.steps != nil && selected.contains($0.run.runID)) }.map(\.run.runID)
        for id in ids.sorted() { await refresh(id) }
    }

    // MARK: Lists

    /// Reads the first page of the scope's list when it has not loaded yet.
    public func ensureLoaded(_ scope: QAScope) {
        guard listings[scope] == nil else { return }
        listings[scope] = Listing(phase: .loading)
        Task { await load(scope) }
    }

    /// Reads the first page of the scope's list.
    public func load(_ scope: QAScope) async {
        var listing = listings[scope] ?? Listing()
        if listing.phase != .loaded { listing.phase = .loading }
        listings[scope] = listing
        do {
            let page = try await backend.qaList(hostID: scope.hostID, limit: Self.pageSize, cursor: nil)
            take(page.runs)
            var current = listings[scope] ?? Listing()
            current.ids = Set(page.runs.map(\.runID)).union(current.ids.filter { records[$0]?.run.isRunning == true })
            current.nextCursor = page.nextCursor
            current.phase = .loaded
            current.moreError = nil
            listings[scope] = current
        } catch is CancellationError {
            if listings[scope]?.phase == .loading { listings[scope]?.phase = .idle }
        } catch {
            if listings[scope]?.phase != .loaded { listings[scope]?.phase = .failed(OperationError(error)) }
        }
    }

    /// Reads the next page of the scope's list.
    public func loadMore(_ scope: QAScope) async {
        guard var listing = listings[scope], let cursor = listing.nextCursor, !listing.loadingMore else { return }
        listing.loadingMore = true
        listing.moreError = nil
        listings[scope] = listing
        do {
            let page = try await backend.qaList(hostID: scope.hostID, limit: Self.pageSize, cursor: cursor)
            take(page.runs)
            listings[scope]?.ids.formUnion(page.runs.map(\.runID))
            listings[scope]?.nextCursor = page.nextCursor
        } catch {
            listings[scope]?.moreError = OperationError(error)
        }
        listings[scope]?.loadingMore = false
    }

    private func take(_ runs: [QARun]) {
        for run in runs {
            if records[run.runID] == nil {
                records[run.runID] = QARunRecord(run)
            } else {
                records[run.runID]?.apply(listed: run)
            }
        }
    }

    /// Adds a run to the lists it belongs in that have loaded.
    private func list(_ run: QARun) {
        for scope in [QAScope.all, .host(run.hostID)] where listings[scope] != nil {
            listings[scope]?.ids.insert(run.runID)
        }
    }

    // MARK: Runs

    /// Reads the steps of a run whose steps are not known yet.
    public func loadSteps(_ runID: String) {
        guard records[runID]?.steps == nil, stepLoads[runID] != .loading else { return }
        Task { await refresh(runID) }
    }

    /// Reads a run with its steps.
    public func refresh(_ runID: String) async {
        if records[runID]?.steps == nil { stepLoads[runID] = .loading }
        do {
            let run = try await backend.qaGet(runID: runID)
            apply(read: run)
            stepLoads[runID] = .loaded
        } catch is CancellationError {
            if stepLoads[runID] == .loading { stepLoads[runID] = nil }
        } catch {
            if records[runID]?.steps == nil { stepLoads[runID] = .failed(OperationError(error)) }
        }
    }

    private func apply(read run: QARun) {
        if records[run.runID] == nil {
            records[run.runID] = QARunRecord(run)
        } else {
            records[run.runID]?.apply(read: run)
        }
        list(run)
        if !run.isRunning { cancels[run.runID]?.inFlight = false }
    }

    /// Selects a run in a scope and reads its steps when they are not known.
    public func select(_ runID: String?, in scope: QAScope) {
        selection[scope] = runID
        if let runID { loadSteps(runID) }
    }

    public func selectStep(_ index: Int?, of runID: String) {
        selectedSteps[runID] = index
    }

    // MARK: Events

    func handle(_ event: DaemonEvent) {
        switch event.kind {
        case EventKind.qaUpdated:
            guard let value = event.payload["run"], let run = try? LiveBackend.decode(QARun.self, from: value) else { return }
            let step = event.payload["step"].flatMap { $0 == .null ? nil : try? LiveBackend.decode(QAStep.self, from: $0) }
            guard var record = records[run.runID] else {
                records[run.runID] = QARunRecord(event: run, step: step)
                list(run)
                return
            }
            let result = record.apply(event: run, step: step)
            records[run.runID] = record
            list(run)
            if !run.isRunning { cancels[run.runID]?.inFlight = false }
            if result == .appliedWithGap, record.steps != nil {
                Task { await refresh(run.runID) }
            }
        case EventKind.eventsDropped:
            Task { await resync() }
        default:
            break
        }
    }

    // MARK: Starting

    /// Starts a run from the draft and selects it in its computer's list and the all-computers
    /// list; returns why it did not start.
    public func start(_ draft: QARunDraft) async -> OperationError? {
        guard let request = draft.request else {
            return OperationError(code: "INVALID_ARGUMENT", message: .ui("qa.start.incomplete", "시나리오 파일과 변수를 확인하세요"))
        }
        do {
            let run = try await backend.qaRun(request)
            for scope in [QAScope.all, .host(run.hostID)] where listings[scope] == nil {
                listings[scope] = Listing(phase: .loading)
                Task { await load(scope) }
            }
            apply(read: run)
            selection[.all] = run.runID
            selection[.host(run.hostID)] = run.runID
            remember(draft)
            return nil
        } catch {
            return OperationError(error)
        }
    }

    /// The run sheet for a new run on `hostID`, or for running `rerun` again with the variables
    /// and limit last used with its scenario.
    public func presentRunSheet(hostID: String, rerun: QARun? = nil) {
        if let rerun, let path = rerun.scenarioPath {
            let recent = recentScenarios.first { $0.path == path }
            sheet = .run(QARunDraft(scenarioPath: path, hostID: rerun.hostID, vars: QAVarsDraft(rows: recent?.vars ?? []),
                                    timeout: recent?.timeout ?? .scenario))
        } else {
            sheet = .run(QARunDraft(hostID: hostID))
        }
    }

    /// The draft for a recent scenario on `hostID`.
    public func draft(from recent: QARecentScenario, hostID: String) -> QARunDraft {
        QARunDraft(scenarioPath: recent.path, hostID: hostID, vars: QAVarsDraft(rows: recent.vars), timeout: recent.timeout)
    }

    private func remember(_ draft: QARunDraft) {
        let path = draft.scenarioPath.trimmingCharacters(in: .whitespacesAndNewlines)
        recentScenarios.removeAll { $0.path == path }
        recentScenarios.insert(QARecentScenario(path: path, hostID: draft.hostID, vars: draft.vars.remembered(), timeout: draft.timeout), at: 0)
        if recentScenarios.count > Self.recentLimit { recentScenarios.removeLast(recentScenarios.count - Self.recentLimit) }
        if let data = try? JSONEncoder().encode(recentScenarios) { defaults.set(data, forKey: Self.recentKey) }
    }

    // MARK: Cancelling

    /// Sends `qa.cancel` when the run's cancel button can: first to stop setup or steps, then while
    /// teardown runs to stop teardown too.
    public func cancel(_ runID: String) async {
        let state = cancelState(of: runID)
        guard state.canSend else { return }
        var progress = cancels[runID] ?? QACancelProgress()
        let previous = progress
        if state == .teardownStoppable { progress.secondSent = true } else { progress.firstSent = true }
        progress.inFlight = true
        cancels[runID] = progress
        do {
            let run = try await backend.qaCancel(runID: runID)
            cancels[runID]?.inFlight = false
            apply(read: run)
        } catch {
            let failure = OperationError(error)
            if failure.code == "INVALID_ARGUMENT" {
                cancels[runID]?.inFlight = false
                notice = DiagnosticsNotice(tone: .info, message: .ui("qa.cancel.ended", "실행이 이미 끝나 취소하지 않았습니다"), detail: failure.message)
                await refresh(runID)
            } else {
                cancels[runID] = previous
                notice = DiagnosticsNotice(tone: .error, message: .ui("qa.cancel.failed", "취소하지 못했습니다"), detail: failure.detail,
                                           code: failure.code)
            }
        }
    }

    // MARK: Results

    /// Copies the run's result bundle into a new folder in `dir`.
    public func export(_ runID: String, to dir: String) async {
        guard let run = records[runID]?.run, !run.isRunning else { return }
        if case .exporting = exports[runID] { return }
        exports[runID] = .exporting(dir: dir)
        do {
            exports[runID] = .exported(try await backend.artifactsExport(jobID: run.jobID, dir: dir))
        } catch {
            exports[runID] = .failed(OperationError(error), dir: dir)
        }
    }

    public func dismissExport(_ runID: String) {
        exports[runID] = nil
    }

    /// The report page of a finished run, from `report.html` or `report_dir`.
    public static func reportPath(_ run: QARun) -> String? {
        run.report?.html ?? run.reportDir.map { QAReportPaths(dir: $0).html }
    }

    /// Opens the run's report page in the default web browser, or says it is missing.
    public func openReport(_ run: QARun) {
        guard let path = Self.reportPath(run) else { return }
        guard FileManager.default.fileExists(atPath: path) else {
            notice = DiagnosticsNotice(tone: .warning, message: .ui("qa.report.missing", "보고서 파일을 찾을 수 없습니다"),
                                       detail: .ui("qa.report.missing.detail", "\(path.abbreviatingHome) 에 없습니다. 결과 폴더를 옮기거나 지웠을 수 있습니다."))
            return
        }
        let url = URL(fileURLWithPath: path)
        if let browser = NSWorkspace.shared.urlForApplication(toOpen: URL(string: "https://example.com")!) {
            NSWorkspace.shared.open([url], withApplicationAt: browser, configuration: NSWorkspace.OpenConfiguration())
        } else {
            NSWorkspace.shared.open(url)
        }
    }

    // MARK: Evidence

    public func content(of artifact: Artifact) -> QAEvidenceContent? {
        evidence[artifact.path]
    }

    /// Reads an evidence file once: images whole up to `imageLimit`, text up to `textLimit`.
    public func loadEvidence(_ artifact: Artifact) {
        let path = artifact.path
        guard evidence[path] == nil || isFailed(path), !evidenceTasks.contains(path) else { return }
        evidenceTasks.insert(path)
        evidence[path] = .loading
        let image = Self.isImage(artifact)
        let limit = image ? Self.imageLimit : Self.textLimit
        let backend = backend
        Task { [weak self] in
            let content: QAEvidenceContent
            do {
                let data = try await backend.evidenceData(path: path, maxBytes: limit + 1)
                content = image ? Self.image(data) : Self.text(data, limit: limit, json: artifact.mime.contains("json"))
            } catch {
                content = .failed(OperationError(error))
            }
            self?.store(content, for: path)
        }
    }

    private func isFailed(_ path: String) -> Bool {
        if case .failed = evidence[path] { return true }
        return false
    }

    private func store(_ content: QAEvidenceContent, for path: String) {
        evidenceTasks.remove(path)
        evidence[path] = content
        guard case .image = content else { return }
        imageOrder.removeAll { $0 == path }
        imageOrder.append(path)
        while imageOrder.count > Self.cachedImages {
            evidence[imageOrder.removeFirst()] = nil
        }
    }

    nonisolated static func isImage(_ artifact: Artifact) -> Bool {
        artifact.kind == "screenshot" || artifact.mime.hasPrefix("image/")
    }

    nonisolated static func image(_ data: Data) -> QAEvidenceContent {
        guard let image = NSImage(data: data), image.isValid else {
            return .failed(OperationError(message: .ui("qa.evidence.badImage", "이미지를 읽을 수 없습니다")))
        }
        return .image(image)
    }

    /// The text of `data`, JSON laid out one member per line.
    nonisolated static func text(_ data: Data, limit: Int, json: Bool) -> QAEvidenceContent {
        let truncated = data.count > limit
        let slice = truncated ? data.prefix(limit) : data
        if json, !truncated, let object = try? JSONSerialization.jsonObject(with: slice),
           let pretty = try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]),
           let text = String(data: pretty, encoding: .utf8) {
            return .text(text, truncated: false)
        }
        return .text(String(decoding: slice, as: UTF8.self), truncated: truncated)
    }

    // MARK: Seeding

    /// Puts runs, lists and evidence in place without asking the backend; the seeded lists do not
    /// load again until a resync. Snapshot scenes use it.
    public func seed(runs: [QARun], listings: [QAScope: Listing], selection: [QAScope: String] = [:], selectedSteps: [String: Int] = [:],
                     stepLoads: [String: LoadPhase] = [:], cancels: [String: QACancelProgress] = [:], exports: [String: QAExportState] = [:],
                     evidence: [String: QAEvidenceContent] = [:], viewer: QAEvidenceSelection? = nil, notice: DiagnosticsNotice? = nil,
                     recent: [QARecentScenario] = [], showsAllHosts: Bool = false, filter: QARunFilter = .all, search: String = "",
                     listShown: Bool? = nil) {
        records = Dictionary(runs.map { ($0.runID, QARunRecord($0)) }, uniquingKeysWith: { _, last in last })
        self.listings = listings
        self.selection = selection
        self.selectedSteps = selectedSteps
        self.stepLoads = stepLoads
        self.cancels = cancels
        self.exports = exports
        self.evidence = evidence
        self.viewer = viewer
        self.notice = notice
        recentScenarios = recent
        self.showsAllHosts = showsAllHosts
        self.filter = filter
        self.search = search
        self.listShown = listShown
    }
}

extension QARunRecord {
    /// A run first heard of from a `qa.updated` event.
    init(event run: QARun, step: QAStep?) {
        var base = run.summary
        base.rev = run.rev - 1
        self.init(base)
        apply(event: run, step: step)
    }
}
