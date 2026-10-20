import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Debugger tab scenes at 1440×900. The tab is seeded from the Shop Admin simulation of
/// `PreviewDebugProgram`; nothing is read from the backend.
@MainActor
enum DebuggerScenes {
    typealias Setup = WorkspaceScenes.Setup
    typealias Program = PreviewDebugProgram

    static let dev = PreviewFixtures.devPCID
    static let page = Program.pageTargetID
    static let api = Program.Script.api
    static let checkout = Program.Script.checkout

    /// When the sample session launched.
    static var launched: Date { Date.now.addingTimeInterval(-6 * 60) }

    static let recent = [
        RecentLaunch(path: Program.appPath, args: ["--env=staging"]),
        RecentLaunch(path: "C:\\Users\\taeyoung\\AppData\\Local\\Programs\\shop-kiosk\\Shop Kiosk.exe", args: ["--kiosk", "--store=강남점"]),
    ]

    static func properties(_ objectID: String) -> DebugPropertyPage? {
        Program.properties(objectID: objectID).map { DebugPropertyPage(properties: $0) }
    }

    static func source(_ script: Program.Script) -> (targetID: String, source: DebugSource) {
        (script.targetID, DebugSource(scriptID: script.scriptID, url: script.url, text: script.source))
    }

    static var scripts: [DebugScript] {
        [Program.Script.main, .index, .checkout, .api].map { DebugScript(scriptID: $0.scriptID, targetID: $0.targetID, url: $0.url) }
    }

    /// The step of the submit flow at `marker` of `script`.
    static func submitStep(_ script: Program.Script, _ marker: String) -> Int {
        Program.stepIndex(.submit, script: script, line: script.line(marker)) ?? 0
    }

    /// Console of a session in its second round: the launch messages, the first order, its
    /// receipt, the coupon exception, and the second order.
    static func secondRoundConsole() -> [ConsoleEntry] {
        var entries = Program.consoleEntries(start: launched)
        let order = entries.first { $0.text.hasPrefix("주문 제출") }!
        var again = order
        again.seq = entries.count + 1
        again.time = Timestamp(date: launched.addingTimeInterval(5 * 60 + 40))
        entries.append(again)
        return entries
    }

    /// Console of a session in its first round, before the coupon exception.
    static func firstRoundConsole() -> [ConsoleEntry] {
        Program.consoleEntries(start: launched).filter { $0.consoleSource != .exception }
    }

    /// The session paused on the submit flow at `step`.
    static func pausedSession(step: Int, gen: Int = 3, breakpoints: [DebugBreakpoint] = Program.sampleBreakpoints()) -> DebugSession {
        Program.session(hostID: dev, created: launched, state: .paused, targets: Program.targets(mainGen: 2, pageGen: gen, pagePaused: true),
                        breakpoints: breakpoints)
    }

    struct Tab {
        var sessions: [(info: DebugSession, seed: (DebugSessionModel) -> Void)] = []
        var listPhase: LoadPhase = .loaded
        var starting: DebuggerModel.Starting?
        var recent: [RecentLaunch] = []
        var centerSplit = 0.56
        var navigatorSplit = 0.46
        var inspectorSplit = 0.4
    }

    static func seed(_ store: AppStore, hostID: String, _ tab: Tab) -> DebuggerModel? {
        guard let host = store.host(hostID) else { return nil }
        let model = store.debugger(for: host)
        let sessions = tab.sessions.map { item -> DebugSessionModel in
            let session = model.makeSession(item.info)
            item.seed(session)
            return session
        }
        model.seed(sessions, listPhase: tab.listPhase, starting: tab.starting, recent: tab.recent)
        model.centerSplit = tab.centerSplit
        model.navigatorSplit = tab.navigatorSplit
        model.inspectorSplit = tab.inspectorSplit
        return model
    }

    static func scene(_ name: String, _ setup: Setup, _ tab: Tab = Tab(), size: CGSize = WorkspaceScenes.size,
                      sheet: ((AppStore, DebuggerModel) -> AnyView)? = nil) -> SnapshotScene {
        SnapshotScene(name: name, size: size) {
            let store = WorkspaceScenes.store(setup)
            let model = seed(store, hostID: setup.selected, tab)
            if let sheet, let model {
                SheetStandIn(base: SplitStandIn(store: store)) { sheet(store, model) }
            } else {
                SplitStandIn(store: store)
            }
        }
    }

    // MARK: Sessions

    static func running() -> (info: DebugSession, seed: (DebugSessionModel) -> Void) {
        (Program.session(hostID: dev, created: launched, targets: Program.targets(mainGen: 2, pageGen: 4) + [
            DebugTarget(id: "7D21C0A4E5B34A1F9C6B2E8D0F4A3B11", role: DebugTargetRole.renderer.rawValue, type: "service_worker",
                        url: "app://shop-admin/sw.js", title: "sw.js", attached: false),
         ], breakpoints: Program.sampleBreakpoints()),
         { session in
            session.seed(console: firstRoundConsole(), scripts: scripts, history: ["order.total"])
        })
    }

    static func paused(expanded: [String] = [], frame: Int = 0, console: [ConsoleEntry]? = nil,
                       evaluations: [(expression: String, frame: String?, result: EvaluationResult)] = [], expandedResults: [Int] = [],
                       expandedEntries: Set<Int> = [], draft: String = "") -> (info: DebugSession, seed: (DebugSessionModel) -> Void) {
        let step = submitStep(api, "const payload = {")
        let state = Program.pauseState(flow: .submit, step: step, gen: 3, reason: .breakpoint, hitBreakpoints: ["bp-1"])
        return (pausedSession(step: step), { session in
            session.seed(pause: state, frame: frame, properties: properties, expanded: expanded, console: console ?? secondRoundConsole(),
                         evaluations: evaluations, expandedResults: expandedResults, expandedEntries: expandedEntries,
                         sources: [source(api), source(checkout), source(.index)], scripts: scripts,
                         history: ["order.total", "order.customer", "payload"], draft: draft)
        })
    }

    static func pausedOnException() -> (info: DebugSession, seed: (DebugSessionModel) -> Void) {
        let step = Program.steps(.coupon).count - 1
        let state = Program.pauseState(flow: .coupon, step: step, gen: 5, reason: .exception)
        let info = Program.session(hostID: dev, created: launched, state: .paused,
                                   targets: Program.targets(mainGen: 2, pageGen: 5, pagePaused: true), breakpoints: Program.sampleBreakpoints())
        return (info, { session in
            session.seed(pause: state, properties: properties, expanded: ["0:local/order"], console: firstRoundConsole(),
                         sources: [source(api), source(checkout), source(.index)], scripts: scripts)
        })
    }

    static func closed() -> (info: DebugSession, seed: (DebugSessionModel) -> Void) {
        (Program.session(hostID: dev, created: launched, state: .closed, targets: Program.targets(mainGen: 2, pageGen: 6),
                         breakpoints: Program.sampleBreakpoints(), closeReason: .processExited, exitCode: 1),
         { session in
            session.seed(console: Program.consoleEntries(start: launched))
        })
    }

    // MARK: Sheets

    static func launchSheet(_ draft: LaunchDraft, error: OperationError? = nil) -> (AppStore, DebuggerModel) -> AnyView {
        { store, model in
            guard let host = store.host(dev) else { return AnyView(EmptyView()) }
            return AnyView(LaunchSheet(model: model, host: host, files: store.backend, draft: draft, error: error, dismiss: {}))
        }
    }

    static func launchPicker() -> (AppStore, DebuggerModel) -> AnyView {
        { store, model in
            guard let host = store.host(dev) else { return AnyView(EmptyView()) }
            let folder = "C:\\Shop\\desktop"
            let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: store.backend, hostID: dev, style: .windows), path: folder)
            browser.seed(path: folder, items: FilesScenes.remoteItems(folder))
            return AnyView(LaunchSheet(model: model, host: host, files: store.backend, browser: browser,
                                       pickerChoice: folder + "\\Shop Admin.exe", dismiss: {}))
        }
    }

    static func attachSheet() -> (AppStore, DebuggerModel) -> AnyView {
        { store, model in
            guard let host = store.host(dev) else { return AnyView(EmptyView()) }
            return AnyView(AttachSheet(model: model, host: host, processes: store.backend,
                                       list: PreviewFixtures.processes(hostID: dev, user: "taeyoung"), selectedPID: 7000, dismiss: {}))
        }
    }

    static func breakpointSheet() -> (AppStore, DebuggerModel) -> AnyView {
        { _, model in
            guard let session = model.selected else { return AnyView(EmptyView()) }
            return AnyView(BreakpointSheet(session: session, script: "checkout.js", line: "31", condition: "order.total > 40000", dismiss: {}))
        }
    }

    // MARK: Scenes

    static var all: [SnapshotScene] {
        let none = Setup(selected: dev, tab: .debugger, inspector: nil)
        let step = submitStep(api, "const payload = {")
        let evaluate = { (expression: String) in Program.evaluate(expression, flow: .submit, step: step, gen: 3, frame: 0) }
        let console = secondRoundConsole()
        let exceptionSeq = console.first { $0.consoleSource == .exception }?.seq ?? 0
        return [
            scene("debugger-idle", none, Tab(recent: recent)),
            scene("debugger-idle-first", Setup(selected: PreviewFixtures.meetingPCID, tab: .debugger, inspector: nil)),
            scene("debugger-launch-sheet", none, Tab(recent: recent),
                  sheet: launchSheet(LaunchDraft(path: Program.appPath, arguments: "--env=staging"))),
            scene("debugger-launch-picker", none, Tab(recent: recent), sheet: launchPicker()),
            scene("debugger-launch-failed", none, Tab(recent: recent), sheet: launchSheet(
                LaunchDraft(path: Program.notElectronPath),
                error: OperationError(code: "TIMEOUT", message: "20초 안에 디버그 포트가 열리지 않아 앱을 종료했습니다"))),
            scene("debugger-attach-sheet", none, Tab(recent: recent), sheet: attachSheet()),
            scene("debugger-running", none, Tab(sessions: [running()])),
            scene("debugger-paused", none, Tab(sessions: [paused()])),
            scene("debugger-paused-exception", none, Tab(sessions: [pausedOnException()])),
            scene("debugger-variables-expanded", none, Tab(sessions: [paused(expanded: [
                "0:local/order", "0:local/order/customer", "0:local/order/lines", "0:local/order/lines/0", "1:module",
            ])], inspectorSplit: 0.28)),
            scene("debugger-caller-frame", none, Tab(sessions: [paused(expanded: ["0:local/order"], frame: 1)])),
            scene("debugger-console", none, Tab(sessions: [paused(
                console: console,
                evaluations: [
                    ("order.total", "postOrder", .value(evaluate("order.total"))),
                    ("order.customer", "postOrder", .value(evaluate("order.customer"))),
                    ("order.summary.total", "postOrder", .value(evaluate("order.summary.total"))),
                    ("totl", "postOrder", .value(evaluate("totl"))),
                ],
                expandedResults: [1], expandedEntries: [exceptionSeq], draft: "order.lines.map((line) => line.sku)")],
                centerSplit: 0.3)),
            scene("debugger-breakpoint-sheet", none, Tab(sessions: [running()]), sheet: breakpointSheet()),
            scene("debugger-closed", none, Tab(sessions: [closed()])),
            scene("debugger-narrow", Setup(selected: dev, tab: .debugger), Tab(sessions: [paused()])),
            scene("debugger-loading", none, Tab(listPhase: .loading)),
            scene("debugger-error", none, Tab(listPhase: .failed(OperationError(
                code: "TIMEOUT", message: "데몬이 5초 안에 debug.list에 답하지 않았습니다")))),
            scene("debugger-offline", Setup(selected: PreviewFixtures.designID, tab: .debugger, inspector: nil)),
            scene("debugger-unsupported", Setup(selected: PreviewFixtures.qaLaptopID, tab: .debugger, inspector: nil)),
        ]
    }
}
