import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

/// Panes of the settings window, in toolbar order.
public enum SettingsPane: String, CaseIterable, Identifiable, Sendable {
    case general
    case connection
    case streaming
    case keyboard
    case files
    case privacy

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .general: .ui("settings.pane.general", "일반")
        case .connection: .ui("settings.pane.connection", "연결")
        case .streaming: .ui("settings.pane.streaming", "스트리밍")
        case .keyboard: .ui("settings.pane.keyboard", "키보드")
        case .files: .ui("settings.pane.files", "파일")
        case .privacy: .ui("settings.pane.privacy", "개인정보·AI")
        }
    }

    public var symbol: String {
        switch self {
        case .general: "gearshape"
        case .connection: "network"
        case .streaming: "play.display"
        case .keyboard: "keyboard"
        case .files: "folder"
        case .privacy: "hand.raised"
        }
    }
}

/// How the daemon looks from the app right now.
public enum DaemonStatus: Equatable, Sendable {
    case running
    case starting
    /// The event stream ended and the app is connecting again.
    case reconnecting
    /// The daemon is being stopped and started from the settings window.
    case restarting
    case unavailable(String)

    public var title: String {
        switch self {
        case .running: .ui("settings.daemon.running", "실행 중")
        case .starting: .ui("settings.daemon.starting", "시작하는 중")
        case .reconnecting: .ui("settings.daemon.reconnecting", "다시 연결하는 중")
        case .restarting: .ui("settings.daemon.restarting", "다시 시작하는 중")
        case .unavailable: .ui("settings.daemon.unavailable", "응답 없음")
        }
    }

    /// The connection state its status dot draws.
    public var dot: ConnectionState {
        switch self {
        case .running: .ready
        case .starting: .connecting
        case .reconnecting, .restarting: .reconnecting
        case .unavailable: .blocked
        }
    }
}

/// A host action the connection pane runs.
public enum HostSettingsAction: Equatable, Sendable {
    case connect
    case disconnect
    case repair
    case remove
}

/// State of the settings window: the pane, the daemon's status and restart, opening at login,
/// the computer each per-computer pane shows, and the connection pane's host actions.
@MainActor
@Observable
public final class SettingsModel {
    public enum RestartState: Equatable, Sendable {
        case idle
        case running
        case failed(OperationError)
    }

    public var pane: SettingsPane {
        didSet { defaults.set(pane.rawValue, forKey: SettingsKey.Settings.pane) }
    }

    public private(set) var daemonInfo: DaemonInfo?
    public private(set) var restart: RestartState = .idle
    public var confirmingRestart = false

    public private(set) var loginItemStatus: LoginItemStatus
    public private(set) var loginItemError: String?

    /// The computer the connection pane shows.
    public var connectionHostID: String?
    /// The computer the files and privacy panes show.
    public var scopeHostID: String?

    public private(set) var hostActions: [String: HostSettingsAction] = [:]
    public private(set) var hostErrors: [String: OperationError] = [:]
    public var pendingRepair: Host?
    public var pendingRemoval: Host?

    @ObservationIgnored weak var store: AppStore?
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private let loginItem: any LoginItemControl

    init(defaults: UserDefaults, loginItem: any LoginItemControl) {
        self.defaults = defaults
        self.loginItem = loginItem
        pane = defaults.string(forKey: SettingsKey.Settings.pane).flatMap(SettingsPane.init(rawValue:)) ?? .general
        loginItemStatus = loginItem.status
    }

    // MARK: Daemon

    public var daemonStatus: DaemonStatus {
        if restart == .running { return .restarting }
        switch store?.phase ?? .idle {
        case .ready: return .running
        case .idle, .starting: return .starting
        case .reconnecting: return .reconnecting
        case .daemonUnavailable(let failure): return .unavailable(failure.message)
        }
    }

    public func refreshDaemonInfo() async {
        guard let store else { return }
        daemonInfo = await store.backend.daemonInfo()
    }

    /// Stops and starts the daemon, then reads its versions again. When the app could not
    /// reach the daemon before, it connects once the daemon is up.
    public func restartDaemon() async {
        guard let store, restart != .running else { return }
        confirmingRestart = false
        restart = .running
        do {
            try await store.backend.restartDaemon()
            restart = .idle
            if case .daemonUnavailable = store.phase { await store.start() }
        } catch {
            restart = .failed(OperationError(error))
        }
        await refreshDaemonInfo()
    }

    /// Starts the daemon when the app could not reach it.
    public func startDaemon() async {
        guard let store else { return }
        await store.start()
        await refreshDaemonInfo()
    }

    // MARK: Login item

    public var launchesAtLogin: Bool { loginItemStatus == .enabled || loginItemStatus == .requiresApproval }

    public func setLaunchesAtLogin(_ enabled: Bool) {
        do {
            try loginItem.setEnabled(enabled)
            loginItemError = nil
        } catch {
            loginItemError = (error as NSError).localizedDescription
        }
        loginItemStatus = loginItem.status
    }

    public func refreshLoginItem() {
        loginItemStatus = loginItem.status
    }

    public func openLoginItemSettings() {
        loginItem.openSystemSettings()
    }

    // MARK: Computers

    /// `id` when it is listed, otherwise the selected computer, otherwise the first one.
    public func host(_ id: String?) -> Host? {
        guard let store else { return nil }
        if let id, let host = store.host(id) { return host }
        return store.selectedHost ?? store.hosts.first
    }

    public func isRunning(_ hostID: String) -> Bool { hostActions[hostID] != nil }

    public func connect(_ hostID: String) {
        run(.connect, hostID) { try await $0.hostConnect(hostID: hostID) }
    }

    public func disconnect(_ hostID: String) {
        run(.disconnect, hostID) { try await $0.hostDisconnect(hostID: hostID) }
    }

    /// Re-pairs the computer the person confirmed in `pendingRepair`.
    public func confirmRepair() {
        guard let host = pendingRepair else { return }
        pendingRepair = nil
        run(.repair, host.hostID) { _ = try await $0.hostRepair(hostID: host.hostID) }
    }

    /// Removes the computer the person confirmed in `pendingRemoval`; with `uninstall` the agent too.
    public func confirmRemoval(uninstall: Bool) {
        guard let host = pendingRemoval else { return }
        pendingRemoval = nil
        run(.remove, host.hostID) { try await $0.hostRemove(hostID: host.hostID, uninstall: uninstall) }
    }

    public func dismissError(_ hostID: String) {
        hostErrors[hostID] = nil
    }

    /// Sets the daemon's versions and the restart state without asking the backend. Snapshot scenes use it.
    public func seed(daemonInfo: DaemonInfo?, restart: RestartState = .idle, hostActions: [String: HostSettingsAction] = [:],
                     hostErrors: [String: OperationError] = [:]) {
        self.daemonInfo = daemonInfo
        self.restart = restart
        self.hostActions = hostActions
        self.hostErrors = hostErrors
    }

    private func run(_ action: HostSettingsAction, _ hostID: String, _ body: @escaping @MainActor (any Backend) async throws -> Void) {
        guard let store, hostActions[hostID] == nil else { return }
        hostActions[hostID] = action
        hostErrors[hostID] = nil
        let backend = store.backend
        Task { [weak self, weak store] in
            do {
                try await body(backend)
                await store?.reloadHosts()
            } catch {
                self?.hostErrors[hostID] = OperationError(error)
            }
            self?.hostActions[hostID] = nil
        }
    }
}
