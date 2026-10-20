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

    /// UserDefaults key holding the selected host ID.
    public static let selectionKey = "selectedHostID"

    public let backend: any Backend
    @ObservationIgnored private let defaults: UserDefaults

    public private(set) var phase: Phase = .idle
    public private(set) var hosts: [Host] = []
    /// True once `host.list` has answered at least once.
    public private(set) var hostsLoaded = false
    public private(set) var isLoadingHosts = false
    public private(set) var hostsError: OperationError?

    public var selectedHostID: String? {
        didSet {
            guard selectedHostID != oldValue else { return }
            if let selectedHostID {
                defaults.set(selectedHostID, forKey: Self.selectionKey)
            } else {
                defaults.removeObject(forKey: Self.selectionKey)
            }
        }
    }

    /// The add-computer sheet, when shown.
    public var addHost: AddHostFlow?
    /// The diagnostics sheet, when shown.
    public var doctor: DoctorModel?
    /// The host awaiting removal confirmation.
    public var pendingRemoval: Host?
    /// Failure of the last host action (connect, disconnect, repair, remove).
    public var actionError: OperationError?

    private var controls: [String: ControlState] = [:]
    private var timelines: [String: [TimelineEntry]] = [:]
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var reloadTask: Task<Void, Never>?

    public init(backend: any Backend, defaults: UserDefaults = .standard) {
        self.backend = backend
        self.defaults = defaults
        self.selectedHostID = defaults.string(forKey: Self.selectionKey)
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
        if let control = controls[hostID] { return control }
        guard let host = host(hostID) else { return nil }
        return ControlState(owner: host.owner, ownerClient: host.ownerClient, gen: 0, aiPaused: false)
    }

    public func timeline(for hostID: String) -> [TimelineEntry] {
        timelines[hostID] ?? []
    }

    // MARK: Lifecycle

    /// Starts the daemon connection, subscribes to events and loads the computer list.
    public func start() async {
        switch phase {
        case .starting, .ready: return
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
            return DaemonFailure(message: .ui("daemon.timeout", "5초 안에 데몬이 응답하지 않았습니다"), logPath: logPath)
        }
        return DaemonFailure(message: OperationError(error).message, logPath: Paths.daemonLog)
    }

    private func subscribe() {
        eventsTask?.cancel()
        let stream = backend.events(kinds: EventKind.storeKinds, hostID: nil)
        eventsTask = Task { [weak self] in
            do {
                for try await event in stream {
                    self?.handle(event)
                }
            } catch {}
        }
    }

    public func reloadHosts() async {
        isLoadingHosts = true
        defer { isLoadingHosts = false }
        do {
            hosts = try await backend.hostList()
            hostsError = nil
            hostsLoaded = true
            let ids = Set(hosts.map(\.hostID))
            controls = controls.filter { ids.contains($0.key) }
            timelines = timelines.filter { ids.contains($0.key) }
            if selectedHostID.map({ !ids.contains($0) }) ?? true {
                selectedHostID = hosts.first?.hostID
            }
        } catch {
            hostsError = OperationError(error)
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
        case EventKind.connectionState:
            guard let id = event.hostID, let i = hosts.firstIndex(where: { $0.hostID == id }),
                  let state = p["state"]?.stringValue else { return }
            hosts[i].state = state
            if let generation = p["connection_generation"]?.intValue { hosts[i].connectionGeneration = generation }
        case EventKind.controlChanged:
            guard let id = event.hostID, let owner = p["owner"]?.stringValue else { return }
            let control = ControlState(owner: owner, ownerClient: p["owner_client"]?.stringValue,
                                       gen: p["gen"]?.intValue ?? 0, aiPaused: p["ai_paused"]?.boolValue ?? false)
            controls[id] = control
            if let i = hosts.firstIndex(where: { $0.hostID == id }) {
                hosts[i].owner = control.owner
                hosts[i].ownerClient = control.ownerClient
            }
        case EventKind.timelineAppended:
            guard let id = event.hostID, let value = p["entry"],
                  let entry = try? LiveBackend.decode(TimelineEntry.self, from: value) else { return }
            var entries = timelines[id] ?? []
            if entries.last.map({ entry.seq > $0.seq }) ?? true { entries.append(entry) }
            timelines[id] = entries
        case EventKind.hostAdded, EventKind.hostRemoved, EventKind.hostUpdated,
             EventKind.capsChanged, EventKind.sessionChanged:
            scheduleReload()
        case EventKind.installProgress:
            if p["step"]?.stringValue == InstallStep.verify.rawValue, p["status"]?.stringValue == "done" {
                scheduleReload()
            }
        default:
            break
        }
    }

    // MARK: Control and timeline

    public func loadControl(for hostID: String) async {
        if let control = try? await backend.controlGet(hostID: hostID) {
            controls[hostID] = control
        }
    }

    public func loadTimeline(for hostID: String, limit: Int = 200) async {
        do {
            timelines[hostID] = try await backend.timelineList(hostID: hostID, since: nil, limit: limit)
        } catch {
            actionError = OperationError(error)
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

    public func disconnect(_ hostID: String) {
        perform { try await $0.hostDisconnect(hostID: hostID) }
    }

    public func repair(_ hostID: String) {
        perform { _ = try await $0.hostRepair(hostID: hostID) }
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
        timelines: [String: [TimelineEntry]] = [:]
    ) -> AppStore {
        let store = AppStore(backend: backend, defaults: defaults)
        store.phase = phase
        store.hosts = hosts
        store.hostsLoaded = phase == .ready
        store.controls = PreviewFixtures.controls(for: hosts)
        store.timelines = timelines
        store.selectedHostID = selectedHostID
        return store
    }
}
