import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

/// App-wide state: daemon reachability, the computer list, selection, and per-host control and
/// timeline. Daemon events keep it current.
@MainActor
@Observable
public final class AppStore {
    public enum Phase: Equatable, Sendable {
        case idle
        case starting
        case ready
        /// The event subscription ended; the store is subscribing again.
        case reconnecting
        case daemonUnavailable(DaemonFailure)
    }

    /// Why the daemon could not be reached, and where its log is.
    public struct DaemonFailure: Equatable, Sendable {
        public var message: String
        public var logPath: String

        public init(message: String, logPath: String) {
            self.message = message
            self.logPath = logPath
        }
    }

    /// Delays between attempts to subscribe again after the event stream ends.
    public struct ResubscribeBackoff: Sendable {
        public var initial: Duration
        public var maximum: Duration

        public init(initial: Duration, maximum: Duration) {
            self.initial = initial
            self.maximum = maximum
        }

        public static let standard = ResubscribeBackoff(initial: .milliseconds(500), maximum: .seconds(8))
    }

    /// UserDefaults key holding the selected host ID.
    public static let selectionKey = SettingsKey.selectedHost

    public let backend: any Backend
    /// The defaults domain the store keeps settings in.
    @ObservationIgnored let defaults: UserDefaults
    @ObservationIgnored private let backoff: ResubscribeBackoff

    public private(set) var phase: Phase = .idle
    public private(set) var hosts: [Host] = []
    /// True once `host.list` has answered at least once.
    public private(set) var hostsLoaded = false
    public private(set) var isLoadingHosts = false
    public private(set) var hostsError: OperationError?

    /// The selected host. Kept while the list is empty, replaced by the first host when it is not in a non-empty list.
    public var selectedHostID: String? {
        didSet {
            guard selectedHostID != oldValue else { return }
            if let selectedHostID {
                defaults.set(selectedHostID, forKey: Self.selectionKey)
            } else {
                defaults.removeObject(forKey: Self.selectionKey)
            }
            controlActions.selectionDidChange()
        }
    }

    /// The add-computer sheet, when shown.
    public var addHost: AddHostFlow?
    /// The diagnostics sheet, when shown.
    public var doctor: DoctorModel?
    /// The host awaiting removal confirmation.
    public var pendingRemoval: Host?
    /// The host awaiting confirmation before its connection is closed.
    public var pendingDisconnect: Host?
    /// The computer whose 다시 페어링 waits for the main window's confirmation.
    public var pendingRepair: Host?
    /// Failure of the last host action (connect, disconnect, repair, remove).
    public var actionError: OperationError?

    /// Selected workspace tab and inspector.
    public let layout: WorkspaceLayout
    /// Stream preset, statistics overlay, keyboard handling and displays of the screen tab.
    public let screenPreferences: ScreenPreferences
    /// Take control, give back to the AI and emergency stop, with their progress and failures.
    public let controlActions: ControlActions
    /// Conflict policy, sorts, columns and folders of the files tab.
    public let filesPreferences: FilesPreferences
    /// Default shell, text size and paste confirmation of the terminal tab.
    public let terminalPreferences: TerminalPreferences
    /// What of each computer reaches AI clients.
    public let privacyPreferences: PrivacyPreferences
    /// The quick switch palette (⌘K).
    public let quickSwitch: QuickSwitchModel
    /// The settings window (⌘,).
    public let settings: SettingsModel
    /// A remote file the files tab asked the logs tab to show.
    public var logSelection: LogSelection?
    /// QA runs of every computer, their selection and the run sheet.
    public let qa: QAModel

    private var controls: [String: ControlState] = [:]
    /// Why the last `control.get` of a host failed, until a read succeeds or it is dismissed.
    private var controlErrors: [String: OperationError] = [:]
    /// Hosts whose next control state is applied whatever its `gen`, after the event stream
    /// was subscribed again.
    private var controlsToRevalidate: Set<String> = []
    /// Timelines of the hosts whose timeline was asked for; events for other hosts are not kept.
    private var timelines: [String: HostTimeline] = [:]
    private var timelineErrors: [String: OperationError] = [:]
    private var streamQualities: [String: StreamQuality] = [:]
    @ObservationIgnored private var filesModels: [String: FilesModel] = [:]
    @ObservationIgnored private var terminalWorkspaces: [String: TerminalWorkspace] = [:]
    @ObservationIgnored private var processModels: [String: ProcessesModel] = [:]
    @ObservationIgnored private var logModels: [String: LogsModel] = [:]
    @ObservationIgnored private var debuggerModels: [String: DebuggerModel] = [:]
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var reconnectsTask: Task<Void, Never>?
    @ObservationIgnored private var reloadTask: Task<Void, Never>?
    @ObservationIgnored private var resyncTask: Task<Void, Never>?
    @ObservationIgnored private var reloadToken = 0
    /// Orders host connection state between `host.list` replies and events.
    @ObservationIgnored private var stateOrder = HostStateOrder()

    /// `loginItem` opens the app at login; without one, a `MemoryLoginItem` that never touches the system.
    public init(backend: any Backend, defaults: UserDefaults = .standard, backoff: ResubscribeBackoff = .standard,
                loginItem: (any LoginItemControl)? = nil) {
        SettingsMigration.migrate(defaults)
        self.backend = backend
        self.defaults = defaults
        self.backoff = backoff
        self.selectedHostID = defaults.string(forKey: Self.selectionKey)
        self.layout = WorkspaceLayout(defaults: defaults)
        self.screenPreferences = ScreenPreferences(defaults: defaults)
        self.controlActions = ControlActions(backend: backend)
        self.filesPreferences = FilesPreferences(defaults: defaults)
        self.terminalPreferences = TerminalPreferences(defaults: defaults)
        self.privacyPreferences = PrivacyPreferences(defaults: defaults)
        self.quickSwitch = QuickSwitchModel(defaults: defaults)
        self.settings = SettingsModel(defaults: defaults, loginItem: loginItem ?? MemoryLoginItem())
        self.qa = QAModel(backend: backend, defaults: defaults, backoff: backoff)
        controlActions.store = self
        settings.store = self
    }

    // MARK: Queries

    public var selectedHost: Host? {
        selectedHostID.flatMap(host)
    }

    public func host(_ hostID: String) -> Host? {
        hosts.first { $0.hostID == hostID }
    }

    public func connection(for hostID: String) -> ConnectionState {
        host(hostID)?.connection ?? .offline
    }

    /// The last known control state: from `control.get` or events, otherwise from the host list.
    public func control(for hostID: String) -> ControlState? {
        guard let host = host(hostID) else { return nil }
        if let control = controls[hostID] { return control }
        return ControlState(owner: host.owner, ownerClient: host.ownerClient, gen: 0, aiPaused: false)
    }

    /// Timeline entries of a host, newest first.
    public func timeline(for hostID: String) -> [TimelineEntry] {
        timelines[hostID]?.entries ?? []
    }

    /// The host's timeline with its day groups and older-page state, once it was asked for.
    public func timelineState(for hostID: String) -> HostTimeline? {
        timelines[hostID]
    }

    /// True once the host's first timeline page has loaded.
    public func isTimelineLoaded(for hostID: String) -> Bool {
        timelines[hostID]?.loaded ?? false
    }

    /// Why the last timeline load of a host failed, until a load succeeds.
    public func timelineError(for hostID: String) -> OperationError? {
        timelineErrors[hostID]
    }

    /// The latest measured quality of the host's screen stream, while one is open.
    public func streamQuality(for hostID: String) -> StreamQuality? {
        streamQualities[hostID]
    }

    /// The files tab state of a host, created on first use and kept while the host is listed.
    public func files(for host: Host) -> FilesModel {
        if let model = filesModels[host.hostID] { return model }
        let model = FilesModel(host: host, backend: backend, preferences: filesPreferences)
        model.store = self
        filesModels[host.hostID] = model
        return model
    }

    /// The terminal tab state of a host, created on first use and kept while the host is listed.
    public func terminals(for host: Host) -> TerminalWorkspace {
        if let workspace = terminalWorkspaces[host.hostID] { return workspace }
        let workspace = TerminalWorkspace(hostID: host.hostID, backend: backend, preferences: terminalPreferences)
        terminalWorkspaces[host.hostID] = workspace
        return workspace
    }

    /// The processes tab state of a host, created on first use and kept while the host is listed.
    public func processes(for host: Host) -> ProcessesModel {
        if let model = processModels[host.hostID] { return model }
        let model = ProcessesModel(hostID: host.hostID, backend: backend, defaults: defaults)
        processModels[host.hostID] = model
        return model
    }

    /// The logs tab state of a host, created on first use and kept while the host is listed.
    public func logs(for host: Host) -> LogsModel {
        if let model = logModels[host.hostID] { return model }
        let model = LogsModel(hostID: host.hostID, backend: backend)
        model.store = self
        logModels[host.hostID] = model
        return model
    }

    /// The debugger tab state of a host, created on first use and kept while the host is listed.
    public func debugger(for host: Host) -> DebuggerModel {
        if let model = debuggerModels[host.hostID] { return model }
        let model = DebuggerModel(hostID: host.hostID, backend: backend, defaults: defaults)
        debuggerModels[host.hostID] = model
        return model
    }

    /// Transfers in the queues of computers whose files tab state exists, most recently changed first.
    var knownTransfers: [Transfer] {
        filesModels.values.flatMap { $0.transfers.visible }.sorted { $0.updatedAt.date > $1.updatedAt.date }
    }

    /// Open debug sessions of computers whose debugger tab state exists, by computer order.
    var knownDebugSessions: [DebugSessionModel] {
        hosts.compactMap { debuggerModels[$0.hostID] }.flatMap(\.openSessions)
    }

    /// A debugged app on the host is paused, for hosts whose debugger tab state exists.
    public func debuggerPaused(hostID: String) -> Bool {
        debuggerModels[hostID]?.openSessions.contains { !$0.pausedTargets.isEmpty } ?? false
    }

    /// Shows `tab` of the selected computer and closes the quick switch.
    public func showTab(_ tab: WorkspaceTab) {
        quickSwitch.dismiss()
        layout.tab = tab
    }

    /// Shows the logs tab with a remote file selected.
    public func openLog(hostID: String, path: String) {
        logSelection = LogSelection(hostID: hostID, path: path)
        layout.tab = .logs
    }

    /// Stops and drops the files, terminal, processes, logs and debugger tab state of a host that left the list.
    private func forgetTabState(_ hostID: String) {
        processModels[hostID]?.setVisible(false)
        logModels[hostID]?.setVisible(false)
        filesModels[hostID]?.stop()
        filesModels[hostID] = nil
        if logSelection?.hostID == hostID { logSelection = nil }
        terminalWorkspaces[hostID]?.stop()
        terminalWorkspaces[hostID] = nil
        processModels[hostID] = nil
        logModels[hostID] = nil
        debuggerModels[hostID]?.stop()
        debuggerModels[hostID] = nil
    }

    /// Records the quality of the host's screen stream; `nil` when the stream closes.
    public func setStreamQuality(_ quality: StreamQuality?, for hostID: String) {
        guard host(hostID) != nil else { return }
        streamQualities[hostID] = quality
    }

    // MARK: Lifecycle

    /// Starts the daemon connection, subscribes to events and loads the computer list.
    public func start() async {
        switch phase {
        case .starting, .ready, .reconnecting: return
        case .idle, .daemonUnavailable: break
        }
        phase = .starting
        do {
            try await backend.start()
        } catch {
            phase = .daemonUnavailable(Self.failure(error))
            return
        }
        phase = .ready
        subscribe()
        await reloadHosts()
    }

    static func failure(_ error: any Error) -> DaemonFailure {
        if case ClientError.daemonUnavailable(let logPath) = error {
            return DaemonFailure(message: .ui("daemon.timeout", "이 Mac의 Deskhand 데몬이 5초 안에 답하지 않았습니다"), logPath: logPath)
        }
        return DaemonFailure(message: OperationError(error).message, logPath: Paths.daemonLog)
    }

    /// Consumes daemon events. When the stream ends or fails, the phase becomes `reconnecting`;
    /// after a backoff delay it subscribes again and reloads the computer list, returning to
    /// `ready` once the list loads. Whenever the daemon connection comes back under a stream that
    /// lasts, or the daemon reports `events.dropped`, it reads the computers, control states and
    /// loaded timelines again.
    private func subscribe() {
        eventsTask?.cancel()
        reconnectsTask?.cancel()
        let reconnects = backend.reconnects()
        reconnectsTask = Task { [weak self] in
            for await _ in reconnects {
                guard !Task.isCancelled else { return }
                self?.daemonReconnected()
            }
        }
        let backoff = self.backoff
        eventsTask = Task { [weak self] in
            var delay = backoff.initial
            var resubscribing = false
            while !Task.isCancelled {
                guard let stream = self?.backend.events(kinds: EventKind.storeKinds, hostID: nil) else { return }
                if resubscribing {
                    self?.stateOrder.forget()
                    let loaded = await self?.reloadHosts() ?? false
                    guard !Task.isCancelled, let store = self else { return }
                    if loaded {
                        store.phase = .ready
                        delay = backoff.initial
                        await store.revalidateControls()
                    } else {
                        try? await Task.sleep(for: delay)
                        delay = min(delay * 2, backoff.maximum)
                        continue
                    }
                }
                do {
                    for try await event in stream {
                        self?.handle(event)
                    }
                } catch {}
                guard !Task.isCancelled, let store = self else { return }
                store.phase = .reconnecting
                resubscribing = true
                try? await Task.sleep(for: delay)
                delay = min(delay * 2, backoff.maximum)
            }
        }
    }

    /// Loads the computer list. Only the latest of overlapping loads is applied; returns whether it succeeded.
    @discardableResult
    public func reloadHosts() async -> Bool {
        reloadToken += 1
        let token = reloadToken
        let requested = stateOrder.mark
        isLoadingHosts = true
        do {
            let list = try await backend.hostList()
            guard token == reloadToken else { return false }
            isLoadingHosts = false
            apply(hostList: list, requested: requested)
            hostsError = nil
            hostsLoaded = true
            return true
        } catch {
            guard token == reloadToken else { return false }
            isLoadingHosts = false
            if error is CancellationError || Task.isCancelled { return false }
            hostsError = OperationError(error)
            return false
        }
    }

    /// Applies a `host.list` reply asked for when the state order's mark was `requested`. A host
    /// whose state event arrived after the request keeps that event's state and generation unless
    /// the reply's generation is newer.
    private func apply(hostList list: [Host], requested: Int) {
        let ids = Set(list.map(\.hostID))
        for host in hosts where !ids.contains(host.hostID) {
            controlActions.forget(host.hostID)
            forgetTabState(host.hostID)
            stateOrder.remove(host.hostID)
        }
        hosts = list.map { listed in
            guard let held = hosts.first(where: { $0.hostID == listed.hostID }),
                  !stateOrder.replyWins(listed.hostID, generation: listed.connectionGeneration, requested: requested) else {
                stateOrder.recordReply(listed.hostID, generation: listed.connectionGeneration)
                return listed
            }
            var merged = listed
            merged.state = held.state
            merged.connectionGeneration = held.connectionGeneration
            return merged
        }
        for i in hosts.indices { syncOwner(at: i) }
        controls = controls.filter { ids.contains($0.key) }
        controlErrors = controlErrors.filter { ids.contains($0.key) }
        controlsToRevalidate = controlsToRevalidate.intersection(ids)
        timelines = timelines.filter { ids.contains($0.key) }
        timelineErrors = timelineErrors.filter { ids.contains($0.key) }
        streamQualities = streamQualities.filter { ids.contains($0.key) }
        reconcileSelection()
    }

    /// Selects the first host when the selection is not in a non-empty list.
    private func reconcileSelection() {
        guard let first = hosts.first else { return }
        if selectedHostID.map({ host($0) == nil }) ?? true {
            selectedHostID = first.hostID
        }
    }

    private func scheduleReload() {
        reloadTask?.cancel()
        reloadTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(100))
            guard !Task.isCancelled else { return }
            await self?.reloadHosts()
        }
    }

    // MARK: Events

    func handle(_ event: DaemonEvent) {
        let p = event.payload
        switch event.kind {
        case EventKind.eventsDropped:
            requestResync()
        case EventKind.hostAdded, EventKind.hostUpdated:
            guard let value = p["host"], var host = try? LiveBackend.decode(Host.self, from: value) else {
                return scheduleReload()
            }
            if let i = hosts.firstIndex(where: { $0.hostID == host.hostID }) {
                if stateOrder.acceptsEvent(host.hostID, generation: host.connectionGeneration) {
                    stateOrder.recordEvent(host.hostID, generation: host.connectionGeneration)
                } else {
                    host.state = hosts[i].state
                    host.connectionGeneration = hosts[i].connectionGeneration
                }
                hosts[i] = host
                syncOwner(at: i)
            } else {
                stateOrder.recordEvent(host.hostID, generation: host.connectionGeneration)
                hosts.append(host)
                syncOwner(at: hosts.count - 1)
            }
            reconcileSelection()
        case EventKind.hostRemoved:
            guard let id = event.hostID else { return }
            hosts.removeAll { $0.hostID == id }
            stateOrder.remove(id)
            controlActions.forget(id)
            forgetTabState(id)
            controls[id] = nil
            controlErrors[id] = nil
            controlsToRevalidate.remove(id)
            timelines[id] = nil
            timelineErrors[id] = nil
            streamQualities[id] = nil
            reconcileSelection()
        default:
            guard let id = event.hostID, let i = hosts.firstIndex(where: { $0.hostID == id }) else {
                if event.kind == EventKind.installProgress { handleInstallProgress(p) }
                return
            }
            handle(event.kind, payload: p, hostIndex: i)
        }
    }

    /// Applies an event for a host that is in the list.
    private func handle(_ kind: String, payload p: [String: JSONValue], hostIndex i: Int) {
        let id = hosts[i].hostID
        switch kind {
        case EventKind.connectionState:
            guard let state = p["state"]?.stringValue else { return }
            let generation = p["connection_generation"]?.intValue ?? hosts[i].connectionGeneration
            guard stateOrder.acceptsEvent(id, generation: generation) else { return }
            stateOrder.recordEvent(id, generation: generation)
            hosts[i].state = state
            hosts[i].connectionGeneration = generation
        case EventKind.controlChanged:
            guard let owner = p["owner"]?.stringValue else { return }
            let control = ControlState(owner: owner, ownerClient: p["owner_client"]?.stringValue,
                                       gen: p["gen"]?.intValue ?? 0, aiPaused: p["ai_paused"]?.boolValue ?? false)
            applyControl(control, for: id)
        case EventKind.timelineAppended:
            guard timelines[id] != nil, let value = p["entry"],
                  let entry = try? LiveBackend.decode(TimelineEntry.self, from: value) else { return }
            timelines[id]?.insert(entry)
        case EventKind.capsChanged:
            guard let value = p["caps"], let caps = try? LiveBackend.decode(HostCaps.self, from: value) else { return }
            hosts[i].caps = caps
        case EventKind.sessionChanged:
            if let value = p["session"], value != .null {
                guard let session = try? LiveBackend.decode(SessionInfo.self, from: value) else { return }
                hosts[i].session = session
            } else {
                hosts[i].session = nil
            }
        case EventKind.displayChanged:
            guard let value = p["displays"], let displays = try? LiveBackend.decode([DisplayInfo].self, from: value) else { return }
            hosts[i].displays = displays
        case EventKind.installProgress:
            handleInstallProgress(p)
        default:
            break
        }
    }

    private func handleInstallProgress(_ p: [String: JSONValue]) {
        if p["step"]?.stringValue == InstallStep.verify.rawValue, p["status"]?.stringValue == "done" {
            scheduleReload()
        }
    }

    // MARK: Control and timeline

    /// Reads the control state of a host; a failure is kept for `controlError(for:)`.
    public func loadControl(for hostID: String) async {
        do {
            let control = try await backend.controlGet(hostID: hostID)
            controlErrors[hostID] = nil
            applyControl(control, for: hostID)
        } catch is CancellationError {
        } catch {
            guard host(hostID) != nil else { return }
            controlErrors[hostID] = OperationError(error)
        }
    }

    /// Why the last control read of a host failed.
    public func controlError(for hostID: String) -> OperationError? {
        controlErrors[hostID]
    }

    public func dismissControlError(for hostID: String) {
        controlErrors[hostID] = nil
    }

    /// The one writer of control state, for `control.changed` events, `control.get` and the
    /// replies of `control.*` calls: applies `control` only when its `gen` is larger than the
    /// known one (local-api.md §5), or when the host is being revalidated. Returns whether it
    /// was applied.
    @discardableResult
    func applyControl(_ control: ControlState, for hostID: String) -> Bool {
        guard let i = hosts.firstIndex(where: { $0.hostID == hostID }) else { return false }
        if let current = controls[hostID], !controlsToRevalidate.contains(hostID), control.gen <= current.gen {
            return false
        }
        controlsToRevalidate.remove(hostID)
        controls[hostID] = control
        syncOwner(at: i)
        return true
    }

    /// After the daemon connection came back: drops the held connection generations, which start
    /// over when the daemon restarts, and reads everything again.
    func daemonReconnected() {
        stateOrder.forget()
        requestResync()
    }

    /// Runs `resync()` unless one is already running.
    private func requestResync() {
        guard resyncTask == nil else { return }
        resyncTask = Task { [weak self] in
            await self?.resync()
            self?.resyncTask = nil
        }
    }

    /// Reads the computers, the control state of every known host and the loaded timelines again.
    func resync() async {
        guard await reloadHosts() else { return }
        await revalidateControls()
        for hostID in Array(timelines.keys) { await loadTimeline(for: hostID) }
    }

    /// Reads the control state of every host already known again, accepting it whatever its
    /// `gen`, after the event stream was subscribed again.
    func revalidateControls() async {
        let known = Array(controls.keys)
        controlsToRevalidate.formUnion(known)
        for hostID in known {
            await loadControl(for: hostID)
        }
    }

    /// Copies the known control owner onto the host at `index`.
    private func syncOwner(at index: Int) {
        guard let control = controls[hosts[index].hostID] else { return }
        hosts[index].owner = control.owner
        hosts[index].ownerClient = control.ownerClient
    }

    /// Loads the newest page of a host's timeline and merges it with entries already received
    /// from events; from the first call on, `timeline.appended` events of the host are kept.
    /// A failure is kept per host for `timelineError(for:)`.
    public func loadTimeline(for hostID: String, limit: Int = HostTimeline.pageSize) async {
        guard host(hostID) != nil else { return }
        if timelines[hostID] == nil { timelines[hostID] = HostTimeline() }
        do {
            let page = try await backend.timelineList(hostID: hostID, since: nil, limit: limit, cursor: nil)
            guard host(hostID) != nil else { return }
            timelines[hostID]?.applyFirstPage(page)
            timelineErrors[hostID] = nil
        } catch is CancellationError {
        } catch {
            guard host(hostID) != nil else { return }
            timelineErrors[hostID] = OperationError(error)
        }
    }

    /// Reads the page older than the oldest held entry of a host's timeline.
    public func loadOlderTimeline(for hostID: String) async {
        guard let cursor = timelines[hostID]?.olderCursor, timelines[hostID]?.isLoadingOlder == false else { return }
        timelines[hostID]?.isLoadingOlder = true
        timelines[hostID]?.olderError = nil
        do {
            let page = try await backend.timelineList(hostID: hostID, since: nil, limit: HostTimeline.pageSize, cursor: cursor)
            timelines[hostID]?.applyOlderPage(page)
        } catch {
            timelines[hostID]?.isLoadingOlder = false
            if !(error is CancellationError) { timelines[hostID]?.olderError = OperationError(error) }
        }
    }

    // MARK: Host actions

    @discardableResult
    public func presentAddHost() -> AddHostFlow {
        if let addHost { return addHost }
        let flow = AddHostFlow(store: self)
        addHost = flow
        return flow
    }

    public func presentDoctor(for hostID: String) {
        guard let host = host(hostID) else { return }
        let model = DoctorModel(backend: backend, host: host)
        doctor = model
        model.run()
    }

    public func connect(_ hostID: String) {
        perform { try await $0.hostConnect(hostID: hostID) }
    }

    /// Asks to confirm closing the host's connection; `disconnect(_:)` closes it.
    public func requestDisconnect(_ host: Host) {
        pendingDisconnect = host
    }

    public func disconnect(_ hostID: String) {
        pendingDisconnect = nil
        perform { try await $0.hostDisconnect(hostID: hostID) }
    }

    /// Asks to confirm re-pairing `host` in the main window; `confirmRepair(_:)` runs it.
    public func requestRepair(_ host: Host) {
        guard !isRepairing(host.hostID) else { return }
        pendingRepair = host
    }

    /// Re-pairs `host` through the settings model's run, the one 다시 페어링 flow of the app.
    public func confirmRepair(_ host: Host) {
        pendingRepair = nil
        settings.repair(host)
    }

    public func isRepairing(_ hostID: String) -> Bool { settings.hostActions[hostID] == .repair }

    /// Why the last 다시 페어링 of the computer failed.
    public func repairError(for hostID: String) -> OperationError? {
        settings.failedActions[hostID] == .repair ? settings.hostErrors[hostID] : nil
    }

    public func dismissRepairError(for hostID: String) {
        guard settings.failedActions[hostID] == .repair else { return }
        settings.dismissError(hostID)
    }

    public func remove(_ hostID: String, uninstall: Bool) {
        pendingRemoval = nil
        perform { [weak self] backend in
            try await backend.hostRemove(hostID: hostID, uninstall: uninstall)
            await self?.reloadHosts()
        }
    }

    private func perform(_ action: @escaping @MainActor (any Backend) async throws -> Void) {
        let backend = self.backend
        Task { [weak self] in
            do {
                try await action(backend)
                self?.actionError = nil
            } catch {
                self?.actionError = OperationError(error)
            }
        }
    }
}

/// A remote file to show in the logs tab.
public struct LogSelection: Sendable, Equatable {
    public var hostID: String
    public var path: String

    public init(hostID: String, path: String) {
        self.hostID = hostID
        self.path = path
    }
}

// MARK: Seeded state

extension AppStore {
    /// A store already in `phase` with `hosts` loaded, without waiting on the backend.
    /// Snapshot scenes use it to render a state deterministically.
    public static func seeded(
        backend: any Backend,
        defaults: UserDefaults,
        phase: Phase = .ready,
        hosts: [Host],
        selectedHostID: String?,
        controls: [String: ControlState]? = nil,
        timelines: [String: [TimelineEntry]] = [:],
        timelineCursors: [String: String] = [:],
        timelineErrors: [String: OperationError] = [:],
        streamQualities: [String: StreamQuality] = [:]
    ) -> AppStore {
        let store = AppStore(backend: backend, defaults: defaults)
        store.phase = phase
        store.hosts = hosts.map { host in
            guard let control = controls?[host.hostID] else { return host }
            var host = host
            host.owner = control.owner
            host.ownerClient = control.ownerClient
            return host
        }
        store.hostsLoaded = phase == .ready || phase == .reconnecting
        store.controls = controls ?? PreviewFixtures.controls(for: hosts)
        store.timelines = timelines.mapValues { _ in HostTimeline() }
        for (hostID, entries) in timelines {
            store.timelines[hostID]?.applyFirstPage(TimelinePage(entries: entries, nextCursor: timelineCursors[hostID]))
        }
        store.timelineErrors = timelineErrors
        store.streamQualities = streamQualities
        store.selectedHostID = selectedHostID
        return store
    }
}
