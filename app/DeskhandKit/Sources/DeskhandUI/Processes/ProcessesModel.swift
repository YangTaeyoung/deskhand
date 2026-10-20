import DeskhandClient
import Foundation
import Observation

/// A kill the person is asked to confirm: the process as listed, whether its tree goes too, and
/// the descendants that would end with it.
public struct KillRequest: Identifiable, Equatable, Sendable {
    public let id: UUID
    public var process: RemoteProcess
    public var tree: Bool
    public var descendants: [RemoteProcess]
    /// Descendants that keep running when only the process ends.
    public var survivors: Int

    public init(id: UUID = UUID(), process: RemoteProcess, tree: Bool, descendants: [RemoteProcess], survivors: Int = 0) {
        self.id = id
        self.process = process
        self.tree = tree
        self.descendants = descendants
        self.survivors = survivors
    }
}

/// A short message at the bottom of a diagnostics tab.
public struct DiagnosticsNotice: Identifiable, Equatable, Sendable {
    public enum Tone: Sendable, Equatable {
        case success
        case warning
        case error
    }

    public let id: UUID
    public var tone: Tone
    public var message: String
    public var detail: String?
    public var code: String?

    public init(id: UUID = UUID(), tone: Tone, message: String, detail: String? = nil, code: String? = nil) {
        self.id = id
        self.tone = tone
        self.message = message
        self.detail = detail
        self.code = code
    }
}

/// The processes tab of one computer: the list, refreshed every two seconds while the tab is on
/// screen, its search, sort and tree view, the selection, and ending a process or its tree after
/// confirmation, with the outcome of the `proc.kill` envelope.
@MainActor
@Observable
public final class ProcessesModel {
    public enum Phase: Equatable, Sendable {
        case idle
        case loading
        case loaded
        case failed(OperationError)
    }

    enum Keys {
        static let sortColumn = "processes.sortColumn"
        static let sortAscending = "processes.sortAscending"
        static let tree = "processes.tree"
    }

    public static let refreshInterval: Duration = .seconds(2)

    public let hostID: String
    public private(set) var phase: Phase = .idle
    /// The last list the agent sent.
    public private(set) var processes: [RemoteProcess] = []
    /// Parent and child links of `processes`.
    public private(set) var forest = ProcessForest([])
    /// The rows the table shows.
    public private(set) var rows: [ProcessRow] = []
    /// When `processes` arrived.
    public private(set) var updatedAt: Date?
    /// Why the last refresh failed while an older list is shown.
    public private(set) var refreshError: OperationError?
    public var query = "" {
        didSet { if query != oldValue { rebuild() } }
    }
    public var sort: ProcessSort {
        didSet {
            guard sort != oldValue else { return }
            defaults.set(sort.column.rawValue, forKey: Keys.sortColumn)
            defaults.set(sort.ascending, forKey: Keys.sortAscending)
            rebuild()
        }
    }
    public var showsTree: Bool {
        didSet {
            guard showsTree != oldValue else { return }
            defaults.set(showsTree, forKey: Keys.tree)
            rebuild()
        }
    }
    /// Tree rows whose children are hidden.
    public private(set) var collapsed: Set<Int> = []
    /// PIDs of `rows`.
    private var shownPIDs: Set<Int> = []
    public var selectedPID: Int?
    /// The kill waiting for confirmation.
    public var pendingKill: KillRequest?
    /// PIDs whose `proc.kill` is in flight.
    public private(set) var killing: Set<Int> = []
    public private(set) var notice: DiagnosticsNotice?
    /// The person stopped the live refresh.
    public var isPaused = false {
        didSet { updateLoop() }
    }

    @ObservationIgnored private let backend: any ProcessesBackend
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private var loop: RefreshLoop!
    @ObservationIgnored private var visible = false
    @ObservationIgnored private var inFlight: Task<Void, Never>?
    @ObservationIgnored private var refreshAgain = false
    @ObservationIgnored private var noticeTask: Task<Void, Never>?

    public init(hostID: String, backend: any ProcessesBackend, defaults: UserDefaults,
                wait: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.hostID = hostID
        self.backend = backend
        self.defaults = defaults
        let column = defaults.string(forKey: Keys.sortColumn).flatMap(ProcessColumn.init(rawValue:))
        sort = column.map { ProcessSort(column: $0, ascending: defaults.bool(forKey: Keys.sortAscending)) } ?? .standard
        showsTree = defaults.bool(forKey: Keys.tree)
        loop = RefreshLoop(interval: Self.refreshInterval, wait: wait) { [weak self] in
            await self?.refresh()
        }
    }

    /// The live refresh runs.
    public var isLive: Bool { loop.isActive }

    /// The selected process when its row is shown: still listed, matching the search and not
    /// inside a collapsed tree row.
    public var selected: RemoteProcess? {
        guard let pid = selectedPID, shownPIDs.contains(pid) else { return nil }
        return forest.process(pid)
    }

    /// The process of the kill waiting for confirmation is no longer listed.
    public var pendingKillEnded: Bool {
        guard let request = pendingKill else { return false }
        return !Self.isListed(request.process, in: forest)
    }

    // MARK: Refresh

    /// Whether the tab is on screen in a visible window and the computer can list processes.
    /// The list refreshes every two seconds while this holds and the person has not paused it.
    public func setVisible(_ visible: Bool) {
        self.visible = visible
        updateLoop()
        if visible, isPaused, phase == .idle {
            Task { await refresh() }
        }
    }

    private func updateLoop() {
        loop.setActive(visible && !isPaused)
    }

    /// Reads the list. A call made while a read is in flight waits for one more read after it.
    public func refresh() async {
        if let inFlight {
            refreshAgain = true
            await inFlight.value
            return
        }
        let task = Task { [weak self] in
            guard let self else { return }
            repeat {
                refreshAgain = false
                await load()
            } while refreshAgain
            inFlight = nil
        }
        inFlight = task
        await task.value
    }

    private func load() async {
        if processes.isEmpty, phase != .loaded { phase = .loading }
        do {
            let list = try await backend.procList(hostID: hostID)
            apply(list)
            refreshError = nil
        } catch is CancellationError {
            if phase == .loading { phase = .idle }
        } catch {
            if phase == .loaded {
                refreshError = OperationError(error)
            } else {
                phase = .failed(OperationError(error))
            }
        }
    }

    private func apply(_ list: [RemoteProcess]) {
        processes = list
        updatedAt = .now
        phase = .loaded
        rebuild()
        if let pid = selectedPID, forest.process(pid) == nil { selectedPID = nil }
    }

    private func rebuild() {
        forest = ProcessForest(processes)
        let pids = Set(processes.map(\.pid))
        collapsed = collapsed.intersection(pids)
        rebuildRows()
    }

    private func rebuildRows() {
        rows = ProcessList.rows(processes, query: query, sort: sort, tree: showsTree, collapsed: collapsed)
        shownPIDs = Set(rows.map(\.id))
    }

    public func toggleCollapsed(_ pid: Int) {
        if collapsed.contains(pid) { collapsed.remove(pid) } else { collapsed.insert(pid) }
        rebuildRows()
    }

    /// The same process is still listed: same PID and, when both are known, the same start.
    static func isListed(_ process: RemoteProcess, in forest: ProcessForest) -> Bool {
        guard let current = forest.process(process.pid) else { return false }
        guard let before = process.started, let now = current.started else { return true }
        return before == now
    }

    // MARK: Kill

    /// Why `pid`, or with `tree` its tree, cannot be ended from the app.
    public func killBlock(pid: Int, tree: Bool) -> KillBlock? {
        guard let process = forest.process(pid) else { return nil }
        return tree ? ProcessGuard.treeBlock(for: process, descendants: forest.descendants(of: pid)) : ProcessGuard.block(for: process)
    }

    /// Asks to confirm ending `pid`, or with `tree` it and its descendants. Protected processes
    /// and trees are never offered.
    public func requestKill(pid: Int, tree: Bool) {
        guard let process = forest.process(pid), killBlock(pid: pid, tree: tree) == nil, !killing.contains(pid) else { return }
        let descendants = forest.descendants(of: pid)
        pendingKill = KillRequest(process: process, tree: tree, descendants: tree ? descendants : [],
                                  survivors: tree ? 0 : descendants.count)
    }

    public func cancelKill() {
        pendingKill = nil
    }

    /// Sends `proc.kill` with the listed start time and reports its envelope: a success removes the
    /// processes; `STALE_TARGET` reads the list again; another failure or a refused request keeps
    /// them with the reason; an unknown outcome or a lost reply is checked against a fresh list.
    public func confirmKill(_ request: KillRequest) async {
        if pendingKill?.id == request.id { pendingKill = nil }
        let process = request.process
        guard !killing.contains(process.pid) else { return }
        killing.insert(process.pid)
        defer { killing.remove(process.pid) }
        let name = process.name
        let pid = String(process.pid)
        do {
            let receipt = try await backend.procKill(hostID: hostID, pid: process.pid, started: process.started, tree: request.tree)
            switch receipt.status {
            case .succeeded:
                let ended = Set([process.pid] + request.descendants.map(\.pid))
                processes.removeAll { ended.contains($0.pid) }
                apply(processes)
                if request.tree {
                    let count = FileFormat.count(max(receipt.killed, ended.count) - 1)
                    show(.success, .ui("processes.killed.tree", "‘\(name)’ 및 하위 프로세스 \(count)개를 종료했습니다"))
                } else {
                    show(.success, .ui("processes.killed", "‘\(name)’(PID \(pid)) 프로세스를 종료했습니다"))
                }
                await refresh()
            case .unknown:
                await verifyUnknown(request, error: receipt.error)
            case .failed, .cancelled, .running, .waitingForUser:
                await refused(request, receipt.error ?? OperationError(message: .ui("error.unknown", "알 수 없는 오류가 발생했습니다")))
            }
        } catch {
            if Self.wasRefused(error) {
                await refused(request, OperationError(error))
            } else {
                await verifyUnknown(request, error: OperationError(code: "EXECUTION_OUTCOME_UNKNOWN", message: OperationError(error).message))
            }
        }
    }

    /// The request did not reach the agent or the daemon refused it: a JSON-RPC error, or a call
    /// that could not be sent.
    static func wasRefused(_ error: any Error) -> Bool {
        if error is RPCErrorInfo || error is OperationError { return true }
        guard let error = error as? ClientError else { return false }
        switch error {
        case .notConnected, .daemonUnavailable, .socketPathTooLong, .connectFailed: return true
        case .disconnected, .closed, .protocolViolation: return false
        }
    }

    /// Reads the list again after a kill whose outcome is unknown and says what it shows.
    private func verifyUnknown(_ request: KillRequest, error: OperationError?) async {
        let process = request.process
        let name = process.name
        let pid = String(process.pid)
        await refresh()
        let code = error?.code ?? "EXECUTION_OUTCOME_UNKNOWN"
        if refreshError != nil || phase != .loaded {
            show(.warning, .ui("processes.kill.unverified", "‘\(name)’(PID \(pid)) 프로세스를 종료했는지 확인하지 못했습니다"),
                 detail: .ui("processes.kill.unverified.detail", "종료 요청의 응답을 받지 못했고 목록도 다시 읽지 못했습니다. 연결이 돌아오면 목록을 새로 고쳐 확인하세요."),
                 code: code)
        } else if Self.isListed(process, in: forest) {
            show(.warning, .ui("processes.kill.stillRunning", "‘\(name)’(PID \(pid)) 프로세스가 아직 실행 중입니다"),
                 detail: .ui("processes.kill.stillRunning.detail", "종료 요청의 응답을 받지 못했고, 다시 읽은 목록에 그대로 있습니다. 다시 종료하거나 원격 PC에서 확인하세요."),
                 code: code)
        } else {
            show(.success, .ui("processes.kill.verified", "‘\(name)’(PID \(pid)) 프로세스가 종료된 것을 확인했습니다"),
                 detail: .ui("processes.kill.verified.detail", "종료 요청의 응답을 받지 못해 목록을 다시 읽어 확인했습니다."))
        }
    }

    /// A kill that did not run: `STALE_TARGET` reads the list again, anything else keeps the
    /// process with the reason.
    private func refused(_ request: KillRequest, _ error: OperationError) async {
        guard error.code == "STALE_TARGET" else { return showFailure(request, error) }
        show(.warning, .ui("processes.kill.stale", "프로세스 목록이 바뀌어 종료하지 않았습니다"),
             detail: .ui("processes.kill.stale.detail", "같은 PID에 다른 프로세스가 실행 중입니다. 새로 읽은 목록에서 다시 선택하세요."),
             code: error.code)
        await refresh()
    }

    private func showFailure(_ request: KillRequest, _ error: OperationError) {
        let name = request.process.name
        show(.error, .ui("processes.kill.failed", "‘\(name)’ 프로세스를 종료하지 못했습니다"),
             detail: [error.message.asSentence, Self.killFix(error)].compactMap { $0 }.joined(separator: "\n"), code: error.code)
    }

    /// What to do after a refused kill.
    static func killFix(_ error: OperationError) -> String? {
        switch error.code {
        case "PERMISSION_DENIED", "ACCESS_DENIED", "ELEVATION_REQUIRED":
            .ui("processes.fix.denied", "서비스라면 서비스 관리자에서 중지하세요.")
        case "NOT_FOUND":
            .ui("processes.fix.notFound", "이미 끝난 프로세스일 수 있습니다. 목록을 새로 고쳐 확인하세요.")
        default:
            error.fix
        }
    }

    // MARK: Notices

    func show(_ tone: DiagnosticsNotice.Tone, _ message: String, detail: String? = nil, code: String? = nil) {
        noticeTask?.cancel()
        let notice = DiagnosticsNotice(tone: tone, message: message, detail: detail, code: code)
        self.notice = notice
        guard tone == .success else { return }
        noticeTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(5))
            guard !Task.isCancelled, self?.notice?.id == notice.id else { return }
            self?.notice = nil
        }
    }

    public func dismissNotice() {
        noticeTask?.cancel()
        notice = nil
    }

    // MARK: Seeding

    /// Puts a list in place without asking the backend. Snapshot scenes use it.
    public func seed(_ list: [RemoteProcess], at date: Date = .now, query: String = "", sort: ProcessSort = .standard, tree: Bool = false,
                     collapsed: Set<Int> = [], selectedPID: Int? = nil, phase: Phase = .loaded, refreshError: OperationError? = nil,
                     paused: Bool = false, notice: DiagnosticsNotice? = nil, killing: Set<Int> = []) {
        processes = list
        updatedAt = date
        self.query = query
        self.sort = sort
        showsTree = tree
        self.collapsed = collapsed
        self.phase = phase
        self.refreshError = refreshError
        isPaused = paused
        rebuild()
        self.selectedPID = selectedPID
        self.notice = notice
        self.killing = killing
    }
}
