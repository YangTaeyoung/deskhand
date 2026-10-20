import DeskhandClient
import Foundation
import Observation

/// An app launched for debugging on a computer, offered again in the launch sheet: its path,
/// arguments, folder, start pause, breakpoints and exception setting.
public struct RecentLaunch: Codable, Equatable, Sendable, Identifiable {
    public var path: String
    public var args: [String]
    public var cwd: String?
    public var pauseOnStart: Bool
    public var breakpoints: [BreakpointSpec]
    public var pauseOnExceptions: ExceptionPauseMode?

    public var id: String { path + "\u{1F}" + args.joined(separator: "\u{1F}") }

    public init(path: String, args: [String] = [], cwd: String? = nil, pauseOnStart: Bool = false, breakpoints: [BreakpointSpec] = [],
                pauseOnExceptions: ExceptionPauseMode? = nil) {
        self.path = path
        self.args = args
        self.cwd = cwd
        self.pauseOnStart = pauseOnStart
        self.breakpoints = breakpoints
        self.pauseOnExceptions = pauseOnExceptions
    }

    public init(_ request: DebugLaunchRequest) {
        self.init(path: request.path, args: request.args, cwd: request.cwd, pauseOnStart: request.pauseOnStart,
                  breakpoints: request.breakpoints, pauseOnExceptions: request.pauseOnExceptions)
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decode(String.self, forKey: .path)
        args = try c.decodeIfPresent([String].self, forKey: .args) ?? []
        cwd = try c.decodeIfPresent(String.self, forKey: .cwd)
        pauseOnStart = try c.decodeIfPresent(Bool.self, forKey: .pauseOnStart) ?? false
        breakpoints = try c.decodeIfPresent([BreakpointSpec].self, forKey: .breakpoints) ?? []
        pauseOnExceptions = try c.decodeIfPresent(ExceptionPauseMode.self, forKey: .pauseOnExceptions)
    }

    private enum CodingKeys: String, CodingKey {
        case path, args, cwd, pauseOnStart, breakpoints, pauseOnExceptions
    }

    /// The launch request for `hostID`.
    public func request(hostID: String) -> DebugLaunchRequest {
        DebugLaunchRequest(hostID: hostID, path: path, args: args, cwd: cwd, pauseOnStart: pauseOnStart, breakpoints: breakpoints,
                           pauseOnExceptions: pauseOnExceptions)
    }
}

/// A pause the workspace announces while another tab is in front.
public struct PausedElsewhere: Equatable, Sendable {
    public var debugID: String
    public var targetID: String
    public var gen: Int
    public var appName: String
    /// `api.js:25`.
    public var location: String?
}

/// The debugger tab of one computer: its sessions from `debug.list`, kept current by `debug.*`
/// events and read again whenever the daemon connection comes back or the event stream is
/// subscribed again; the selected session; and launching or attaching a new one.
@MainActor
@Observable
public final class DebuggerModel {
    /// A launch or attach in flight.
    public struct Starting: Equatable, Sendable {
        public var name: String
        public var kind: DebugSessionKind

        public init(name: String, kind: DebugSessionKind) {
            self.name = name
            self.kind = kind
        }
    }

    /// A sheet of the tab.
    public enum Sheet: Identifiable, Equatable, Sendable {
        case launch
        case attach
        case breakpoint
        /// Changing the condition of a breakpoint.
        case condition(String)

        public var id: String {
            switch self {
            case .launch: "launch"
            case .attach: "attach"
            case .breakpoint: "breakpoint"
            case .condition(let id): "condition-" + id
            }
        }
    }

    public static let recentLimit = 5

    public let hostID: String
    public private(set) var listPhase: LoadPhase = .idle
    /// In creation order.
    public private(set) var sessions: [DebugSessionModel] = []
    public var selectedSessionID: String?
    public private(set) var starting: Starting?
    public private(set) var recentLaunches: [RecentLaunch] = []
    public var sheet: Sheet?
    /// The script navigator popover is shown.
    public var showsScriptNavigator = false
    /// The session waiting for the close confirmation.
    public var pendingClose: String?
    /// Fractions of the split panes: sessions over breakpoints, source over console.
    public var navigatorSplit = 0.46
    public var centerSplit = 0.56
    /// The navigator column shows; nil follows the window width.
    public var navigatorShown: Bool?
    /// The navigator shows laid over a narrow tab.
    public var navigatorOverlay = false
    /// The console's expression field has the keyboard.
    public var consoleFocused = false
    /// Pauses the person dismissed from the workspace banner, as `debugID|targetID|gen`.
    public private(set) var dismissedPauses: Set<String> = []

    @ObservationIgnored let backend: any DebuggerBackend & EventsBackend
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private let backoff: AppStore.ResubscribeBackoff
    @ObservationIgnored let timings: DebugTimings
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var reconnectsTask: Task<Void, Never>?
    @ObservationIgnored private var seeded = false
    /// Reloads of the session list so far.
    @ObservationIgnored private(set) var reloads = 0

    public init(hostID: String, backend: any DebuggerBackend & EventsBackend, defaults: UserDefaults,
                backoff: AppStore.ResubscribeBackoff = .standard, timings: DebugTimings = .standard) {
        self.hostID = hostID
        self.backend = backend
        self.defaults = defaults
        self.backoff = backoff
        self.timings = timings
        recentLaunches = Self.readRecent(defaults, hostID: hostID)
    }

    private var recentKey: String { "debugger.recentLaunches.\(hostID)" }

    private static func readRecent(_ defaults: UserDefaults, hostID: String) -> [RecentLaunch] {
        guard let data = defaults.data(forKey: "debugger.recentLaunches.\(hostID)"),
              let list = try? JSONDecoder().decode([RecentLaunch].self, from: data) else { return [] }
        return list
    }

    // MARK: Queries

    public var selected: DebugSessionModel? {
        sessions.first { $0.id == selectedSessionID } ?? sessions.last { !$0.isClosed } ?? sessions.last
    }

    public func session(_ debugID: String) -> DebugSessionModel? { sessions.first { $0.id == debugID } }

    public var phase: DebuggerPhase {
        if let starting { return .starting(starting.name) }
        return selected?.phase ?? .idle
    }

    /// Sessions that are not closed.
    public var openSessions: [DebugSessionModel] { sessions.filter { !$0.isClosed } }

    /// The newest pause of an open session the person has not dismissed.
    public var pausedElsewhere: PausedElsewhere? {
        for session in sessions.reversed() where !session.isClosed {
            for target in session.pausedTargets {
                let state = session.pauses.state(for: target.id).flatMap { $0.paused ? $0 : nil }
                let gen = session.pauses.announcedGen(of: target.id) ?? state?.gen ?? 0
                guard !dismissedPauses.contains(Self.pauseKey(session.id, target.id, gen)) else { continue }
                let frame = state?.gen == gen ? state?.callFrames.first : nil
                let location = frame.map { DebugFormat.location(url: $0.url, line: $0.line) }
                return PausedElsewhere(debugID: session.id, targetID: target.id, gen: gen, appName: session.info.name, location: location)
            }
        }
        return nil
    }

    static func pauseKey(_ debugID: String, _ targetID: String, _ gen: Int) -> String {
        debugID + "|" + targetID + "|" + String(gen)
    }

    /// Hides the workspace banner for this pause; a later pause shows it again.
    public func dismiss(_ pause: PausedElsewhere) {
        dismissedPauses.insert(Self.pauseKey(pause.debugID, pause.targetID, pause.gen))
    }

    /// Selects the paused session and target.
    public func reveal(_ pause: PausedElsewhere) {
        selectedSessionID = pause.debugID
        session(pause.debugID)?.select(target: pause.targetID)
    }

    // MARK: Lifecycle

    /// Follows `debug.*` events of the computer and loads its sessions. When the event stream
    /// ends it subscribes again with backoff; whenever the daemon connection comes back it reads
    /// the sessions and the missed console entries again.
    public func start() {
        guard eventsTask == nil else { return }
        let backend = backend
        let hostID = hostID
        let backoff = backoff
        let skipFirstLoad = seeded
        eventsTask = Task { [weak self] in
            var delay = backoff.initial
            var first = true
            while !Task.isCancelled {
                let stream = backend.events(kinds: EventKind.debuggerKinds, hostID: hostID)
                if !(first && skipFirstLoad) { await self?.reload() }
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
                await self?.reload()
            }
        }
    }

    public func stop() {
        eventsTask?.cancel()
        eventsTask = nil
        reconnectsTask?.cancel()
        reconnectsTask = nil
    }

    /// Reads the sessions; known ones are updated and read their missed console entries.
    public func reload() async {
        reloads += 1
        if listPhase != .loaded { listPhase = .loading }
        let marks = Dictionary(sessions.map { ($0.id, $0.changeClock) }, uniquingKeysWith: { first, _ in first })
        do {
            let list = try await backend.debugList(hostID: hostID)
            apply(list: list, marks: marks)
            listPhase = .loaded
        } catch is CancellationError {
            if listPhase == .loading { listPhase = .idle }
        } catch {
            if listPhase != .loaded { listPhase = .failed(OperationError(error)) }
        }
    }

    private func apply(list: [DebugSession], marks: [String: Int]) {
        let listed = Set(list.map(\.debugID))
        for info in list {
            if let existing = session(info.debugID) {
                existing.apply(info: info, authoritative: true, since: (marks[info.debugID] ?? 0) + 1)
                Task { await existing.loadConsole() }
            } else {
                insert(info)
            }
        }
        for session in sessions where !listed.contains(session.id) && !session.isClosed {
            session.applyClosed(reason: nil, exitCode: nil)
        }
    }

    @discardableResult
    private func insert(_ info: DebugSession) -> DebugSessionModel {
        if let existing = session(info.debugID) {
            existing.apply(info: info)
            return existing
        }
        let model = DebugSessionModel(info: info, backend: backend, timings: timings)
        sessions.append(model)
        Task { await model.begin() }
        return model
    }

    // MARK: Starting sessions

    /// Launches an app for debugging and selects its session; returns why it failed.
    public func launch(_ request: DebugLaunchRequest) async -> OperationError? {
        let name = FilePath.lastComponent(request.path, style: .windows)
        let display = name.lowercased().hasSuffix(".exe") ? String(name.dropLast(4)) : name
        return await begin(Starting(name: display.isEmpty ? request.path : display, kind: .launch)) {
            try await self.backend.debugLaunch(request)
        } onSuccess: {
            self.remember(RecentLaunch(request))
        }
    }

    /// Attaches to a running app and selects its session; returns why it failed.
    public func attach(_ request: DebugAttachRequest, name: String) async -> OperationError? {
        await begin(Starting(name: name, kind: .attach)) {
            try await self.backend.debugAttach(request)
        } onSuccess: {}
    }

    private func begin(_ next: Starting, _ call: @escaping @MainActor () async throws -> DebugSession,
                       onSuccess: @MainActor () -> Void) async -> OperationError? {
        guard starting == nil else {
            return OperationError(code: "IN_PROGRESS", message: .ui("debugger.startInProgress", "다른 앱을 시작하는 중입니다"))
        }
        starting = next
        defer { starting = nil }
        do {
            let info = try await call()
            let model = insert(info)
            selectedSessionID = model.id
            onSuccess()
            return nil
        } catch {
            return OperationError(error)
        }
    }

    private func remember(_ launch: RecentLaunch) {
        recentLaunches.removeAll { $0.path.caseInsensitiveCompare(launch.path) == .orderedSame }
        recentLaunches.insert(launch, at: 0)
        if recentLaunches.count > Self.recentLimit { recentLaunches.removeLast(recentLaunches.count - Self.recentLimit) }
        if let data = try? JSONEncoder().encode(recentLaunches) { defaults.set(data, forKey: recentKey) }
    }

    /// Removes a closed session from the list.
    public func dismiss(_ debugID: String) {
        guard let session = session(debugID), session.isClosed else { return }
        sessions.removeAll { $0.id == debugID }
        if selectedSessionID == debugID { selectedSessionID = nil }
    }

    /// What to do after the session list failed to load.
    public static func listFix(_ error: OperationError) -> String {
        switch error.code {
        case "OFFLINE", "AUTH_REQUIRED", "SESSION_LOCKED": return error.fix ?? ""
        default:
            return .ui("debugger.listFix", "이 Mac의 Deskhand 데몬이 답하지 않았습니다. 잠시 뒤 다시 시도하고, 계속되면 컴퓨터 메뉴의 진단 실행으로 데몬 상태를 확인하세요.")
        }
    }

    // MARK: Events

    func handle(_ event: DaemonEvent) {
        let p = event.payload
        guard let debugID = p["debug_id"]?.stringValue else { return }
        switch event.kind {
        case EventKind.debugSessionChanged:
            guard let value = p["session"], let info = try? LiveBackend.decode(DebugSession.self, from: value), info.hostID == hostID else { return }
            insert(info)
        case EventKind.debugTargetChanged:
            guard let session = session(debugID), let value = p["target"],
                  let target = try? LiveBackend.decode(DebugTarget.self, from: value) else { return }
            session.apply(target: target, removed: p["removed"]?.boolValue ?? false, rev: p["rev"]?.intValue)
        case EventKind.debugBreakpointChanged:
            guard let session = session(debugID), let value = p["breakpoint"],
                  let breakpoint = try? LiveBackend.decode(DebugBreakpoint.self, from: value) else { return }
            session.apply(breakpoint: breakpoint, removed: p["removed"]?.boolValue ?? false)
            if let error = p["error"], case .object = error {
                let title = BreakpointPattern.title(of: breakpoint)
                session.show(.error, .ui("debugger.breakpoint.refused", "‘\(title)’ 중단점을 대상이 받아들이지 않아 지웠습니다"),
                             error: OperationError(code: error["code"]?.stringValue, message: error["message"]?.stringValue ?? ""))
            }
        case EventKind.debugPaused:
            guard let session = session(debugID), let value = p["state"],
                  let state = try? LiveBackend.decode(DebugPauseState.self, from: value) else { return }
            session.apply(paused: state)
            if session.pauses.isPaused(state.targetID), selected?.pauseState == nil { selectedSessionID = session.id }
        case EventKind.debugResumed:
            guard let session = session(debugID), let targetID = p["target_id"]?.stringValue, let gen = p["gen"]?.intValue else { return }
            session.apply(resumedTarget: targetID, gen: gen)
        case EventKind.debugConsole:
            guard let session = session(debugID), let value = p["entry"],
                  let entry = try? LiveBackend.decode(ConsoleEntry.self, from: value) else { return }
            session.apply(entry: entry)
        case EventKind.debugConsoleCleared:
            guard let session = session(debugID), let through = p["through_seq"]?.intValue else { return }
            session.applyConsoleCleared(through: through)
        case EventKind.debugClosed:
            guard let session = session(debugID) else { return }
            session.applyClosed(reason: p["reason"]?.stringValue.flatMap(DebugCloseReason.init(rawValue:)), exitCode: p["exit_code"]?.intValue,
                                rev: p["rev"]?.intValue)
        default:
            break
        }
    }

    // MARK: Seeding

    /// A session model for `info` that reads nothing until it is seeded or begun.
    public func makeSession(_ info: DebugSession) -> DebugSessionModel {
        DebugSessionModel(info: info, backend: backend, timings: timings)
    }

    /// Puts sessions in place without asking the backend; `start()` then only follows events.
    /// Snapshot scenes use it.
    public func seed(_ sessions: [DebugSessionModel], selected: String? = nil, listPhase: LoadPhase = .loaded, starting: Starting? = nil,
                     recent: [RecentLaunch] = [], navigatorShown: Bool? = nil, showsScriptNavigator: Bool = false) {
        self.sessions = sessions
        selectedSessionID = selected ?? sessions.last?.id
        self.listPhase = listPhase
        self.starting = starting
        recentLaunches = recent
        self.navigatorShown = navigatorShown
        self.showsScriptNavigator = showsScriptNavigator
        seeded = true
    }
}
