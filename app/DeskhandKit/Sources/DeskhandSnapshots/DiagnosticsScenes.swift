import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Processes and logs tab scenes at 1440×900. Each tab is seeded from fixtures; nothing is read
/// from the backend.
@MainActor
enum DiagnosticsScenes {
    typealias Setup = WorkspaceScenes.Setup

    static let dev = PreviewFixtures.devPCID

    struct Processes {
        var query = ""
        var sort = ProcessSort.standard
        var tree = false
        var collapsed: Set<Int> = []
        var selected: Int?
        var phase: ProcessesModel.Phase = .loaded
        var refreshError: OperationError?
        var paused = false
        var notice: DiagnosticsNotice?
        var killing: Set<Int> = []
        /// PIDs left out of the seeded list.
        var ended: Set<Int> = []
    }

    static func processList(_ hostID: String = dev) -> [RemoteProcess] {
        PreviewFixtures.processes(hostID: hostID, user: "taeyoung")
    }

    static func seed(_ store: AppStore, hostID: String, _ processes: Processes) {
        guard let host = store.host(hostID) else { return }
        let model = store.processes(for: host)
        let list = processes.phase == .loaded ? processList(hostID).filter { !processes.ended.contains($0.pid) } : []
        model.seed(list, query: processes.query, sort: processes.sort, tree: processes.tree, collapsed: processes.collapsed,
                   selectedPID: processes.selected, phase: processes.phase, refreshError: processes.refreshError,
                   paused: processes.paused, notice: processes.notice, killing: processes.killing)
    }

    static func processesScene(_ name: String, _ setup: Setup, _ processes: Processes = Processes(), size: CGSize = WorkspaceScenes.size,
                               textSize: DynamicTypeSize = .large, sheet: ((AppStore) -> AnyView)? = nil) -> SnapshotScene {
        SnapshotScene(name: name, size: size) {
            let store = WorkspaceScenes.store(setup)
            let _ = seed(store, hostID: setup.selected, processes)
            if let sheet {
                SheetStandIn(base: SplitStandIn(store: store)) { sheet(store) }
                    .environment(\.dynamicTypeSize, textSize)
            } else {
                SplitStandIn(store: store)
                    .environment(\.dynamicTypeSize, textSize)
            }
        }
    }

    struct Logs {
        var filter = EventFilter()
        var search = ""
        var selected: ((EventLogEntry) -> Bool)?
        var section: LogsModel.Section = .events
        var systemPhase: LoadPhase = .loaded
        var eventsPhase: LoadPhase = .loaded
        var crashesPhase: LoadPhase = .loaded
        var systemRefreshError: OperationError?
        var transfers: [TransferDetail] = []
        var logFile: String?
    }

    /// Fixture events of the filter's log, range and levels, at most one page.
    static func events(_ hostID: String, _ filter: EventFilter) -> (events: [EventLogEntry], truncated: Bool) {
        let since = filter.range.bounds(now: .now).since
        let matching = PreviewFixtures.events(hostID: hostID, log: filter.log).filter { event in
            event.time.date >= since && event.eventLevel.map(filter.levels.contains) ?? true
                && (filter.provider == nil || filter.provider == event.provider)
        }
        return (Array(matching.prefix(EventFilter.pageSize)), matching.count > EventFilter.pageSize)
    }

    static func seed(_ store: AppStore, hostID: String, _ logs: Logs) {
        guard let host = store.host(hostID) else { return }
        let model = store.logs(for: host)
        let list = processList(hostID).filter { $0.pid != 0 }
        let busy = list.compactMap(\.cpuPct).reduce(0, +)
        let page: (events: [EventLogEntry], truncated: Bool) = logs.eventsPhase == .loaded ? events(hostID, logs.filter) : ([], false)
        let all = Set(PreviewFixtures.events(hostID: hostID, log: logs.filter.log).map(\.provider))
            .sorted { $0.localizedStandardCompare($1) == .orderedAscending }
        model.seed(system: logs.systemPhase == .loaded ? PreviewFixtures.system(hostID: hostID, cpuUsage: (busy * 10).rounded() / 10) : nil,
                   systemPhase: logs.systemPhase, systemRefreshError: logs.systemRefreshError, filter: logs.filter, events: page.events, eventsPhase: logs.eventsPhase,
                   truncated: page.truncated, search: logs.search,
                   selectedRecordID: logs.selected.flatMap { page.events.first(where: $0)?.recordID }, providers: all,
                   crashes: logs.crashesPhase == .loaded ? PreviewFixtures.crashDumps(hostID: hostID, user: host.session?.user ?? "user") : [],
                   crashesPhase: logs.crashesPhase, section: logs.section)
        store.files(for: host).transfers.seed(logs.transfers)
        store.logSelection = logs.logFile.map { LogSelection(hostID: hostID, path: $0) }
    }

    static func logsScene(_ name: String, _ setup: Setup, _ logs: Logs = Logs()) -> SnapshotScene {
        SnapshotScene(name: name, size: WorkspaceScenes.size) {
            let store = WorkspaceScenes.store(setup)
            let _ = seed(store, hostID: setup.selected, logs)
            SplitStandIn(store: store)
        }
    }

    /// Pulls of the crash dump fixtures: Shop.exe received, the dotnet full dump arriving.
    static func dumpTransfers() -> [TransferDetail] {
        let folder = FilesPreferences.defaultDownloadFolder
        let dumps = PreviewFixtures.crashDumps(hostID: dev, user: "taeyoung")
        let shop = dumps[0]
        let dotnet = dumps[1]
        func name(_ dump: CrashDump) -> String { FilePath.lastComponent(dump.path, style: .windows) }
        let done = Int(Double(dotnet.sizeBytes) * 0.38)
        return [
            TransferDetail(Transfer(transferID: "t-dump-dotnet", hostID: dev, direction: "pull", local: folder + "/" + name(dotnet),
                                    remote: dotnet.path, onConflict: "rename", state: "running", bytesDone: done, bytesTotal: dotnet.sizeBytes,
                                    filesDone: 0, filesTotal: 1, rateBps: 29_360_128),
                           files: [TransferFile(rel: name(dotnet), size: dotnet.sizeBytes, bytesDone: done, state: "running")]),
            TransferDetail(Transfer(transferID: "t-dump-shop", hostID: dev, direction: "pull", local: folder + "/" + name(shop), remote: shop.path,
                                    onConflict: "rename", state: "done", bytesDone: shop.sizeBytes, bytesTotal: shop.sizeBytes, filesDone: 1,
                                    filesTotal: 1, rateBps: 0, outcome: "written"),
                           files: [TransferFile(rel: name(shop), size: shop.sizeBytes, bytesDone: shop.sizeBytes, state: "done", outcome: "written",
                                                path: folder + "/" + name(shop))]),
        ]
    }

    /// The kill confirmation for `pid` of the seeded list, with the seeded row as its current values.
    static func killSheet(_ store: AppStore, pid: Int, tree: Bool) -> AnyView {
        guard let host = store.host(dev) else { return AnyView(EmptyView()) }
        let model = store.processes(for: host)
        guard let process = model.forest.process(pid) else { return AnyView(EmptyView()) }
        let descendants = model.forest.descendants(of: pid)
        let request = KillRequest(process: process, tree: tree, descendants: tree ? descendants : [],
                                  survivors: tree ? 0 : descendants.count)
        return AnyView(KillConfirmSheet(request: request, hostName: host.name, current: process, confirm: {}, cancel: {}))
    }

    static var all: [SnapshotScene] {
        let chrome = processList().first { $0.pid == 6100 }!
        var application = EventFilter()
        application.log = .application
        application.range = .lastWeek
        application.levels = [.critical, .error, .warning]
        return [
            processesScene("processes", Setup(selected: dev, tab: .processes, inspector: nil), Processes(selected: 9272)),
            processesScene("processes-search", Setup(selected: dev, tab: .processes, inspector: nil), Processes(query: "node")),
            processesScene("processes-large-text", Setup(selected: dev, tab: .processes, inspector: nil), Processes(selected: 9272),
                           textSize: .xxxLarge),
            processesScene("processes-tree", Setup(selected: dev, tab: .processes), Processes(
                sort: ProcessSort(column: .pid, ascending: true), tree: true, collapsed: [4, 1104, 1392],
                selected: PreviewFixtures.deskhandShellPID)),
            processesScene("processes-tree-narrow", Setup(selected: dev, tab: .processes), Processes(
                sort: ProcessSort(column: .pid, ascending: true), tree: true, collapsed: [4, 1104, 1392],
                selected: 9300), size: CGSize(width: 1180, height: 760)),
            processesScene("processes-kill-confirm", Setup(selected: dev, tab: .processes, inspector: nil), Processes(selected: 6100),
                           sheet: { killSheet($0, pid: 6100, tree: true) }),
            processesScene("processes-kill-deskhand", Setup(selected: dev, tab: .processes, inspector: nil),
                           Processes(selected: PreviewFixtures.deskhandShellPID),
                           sheet: { killSheet($0, pid: PreviewFixtures.deskhandShellPID, tree: false) }),
            processesScene("processes-kill-result", Setup(selected: dev, tab: .processes, inspector: nil), Processes(
                sort: ProcessSort(column: .memory, ascending: false), selected: 2260,
                refreshError: OperationError(code: "TIMEOUT", message: "에이전트가 5초 안에 답하지 않았습니다"),
                notice: DiagnosticsNotice(tone: .error, message: "‘MsMpEng.exe’ 프로세스를 종료하지 못했습니다",
                                          detail: "Windows가 ‘MsMpEng.exe’의 종료를 거부했습니다. 보호된 프로세스라 관리자 권한으로도 끝낼 수 없습니다.\n서비스라면 서비스 관리자에서 중지하세요.",
                                          code: "PERMISSION_DENIED"))),
            processesScene("processes-unknown-outcome", Setup(selected: dev, tab: .processes, inspector: nil), Processes(
                paused: true,
                notice: DiagnosticsNotice(tone: .warning, message: "‘Code.exe’(PID 7000) 프로세스가 아직 실행 중입니다",
                                          detail: "종료 요청의 응답을 받지 못했고, 다시 읽은 목록에 그대로 있습니다. 다시 종료하거나 원격 PC에서 확인하세요.",
                                          code: "EXECUTION_OUTCOME_UNKNOWN"))),
            processesScene("processes-protected", Setup(selected: dev, tab: .processes, inspector: nil),
                           Processes(selected: PreviewFixtures.agentPID)),
            processesScene("processes-kill-ended", Setup(selected: dev, tab: .processes, inspector: nil), Processes(ended: [6100]),
                           sheet: { store in
                AnyView(KillConfirmSheet(request: KillRequest(process: chrome, tree: false, descendants: [], survivors: 8),
                                         hostName: store.host(dev)?.name ?? "개발 PC", ended: true, confirm: {}, cancel: {}))
            }),
            processesScene("processes-loading", Setup(selected: dev, tab: .processes, inspector: nil), Processes(phase: .loading)),
            processesScene("processes-error", Setup(selected: dev, tab: .processes, inspector: nil), Processes(
                phase: .failed(OperationError(code: "CAPABILITY_UNSUPPORTED", message: "에이전트가 proc.list를 지원하지 않습니다"))))
            ,
            processesScene("processes-offline", Setup(selected: PreviewFixtures.designID, tab: .processes, inspector: nil)),
            logsScene("logs", Setup(selected: dev, tab: .logs)),
            logsScene("logs-wide", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                selected: { $0.provider == "Microsoft-Windows-DistributedCOM" })),
            logsScene("logs-filtered", Setup(selected: dev, tab: .logs, inspector: nil), Logs(filter: application)),
            logsScene("logs-event-detail", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                filter: application, selected: { $0.provider == ".NET Runtime" })),
            logsScene("logs-search", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                filter: EventFilter(range: .lastWeek), search: "Shop Sync")),
            logsScene("logs-crashes", Setup(selected: dev, tab: .logs, inspector: nil), Logs(section: .crashes, transfers: dumpTransfers())),
            logsScene("logs-crashes-empty", Setup(selected: PreviewFixtures.meetingPCID, tab: .logs, inspector: nil), Logs(section: .crashes)),
            logsScene("logs-loading", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                systemPhase: .loading, eventsPhase: .loading, crashesPhase: .loading)),
            logsScene("logs-error", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                systemPhase: .failed(OperationError(code: "TIMEOUT", message: "에이전트가 10초 안에 시스템 정보를 보내지 않았습니다")),
                eventsPhase: .failed(OperationError(code: "PERMISSION_DENIED", message: "이벤트 로그 서비스가 시스템 로그 읽기를 거부했습니다")))),
            logsScene("logs-summary-stale", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                systemRefreshError: OperationError(code: "TIMEOUT", message: "에이전트가 10초 안에 시스템 정보를 보내지 않았습니다"))),
            logsScene("logs-file", Setup(selected: dev, tab: .logs, inspector: nil), Logs(
                filter: EventFilter(log: .application), logFile: PreviewFixtures.lockedLog)),
        ]
    }
}
