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
    typealias Seeded = (info: DebugSession, seed: (DebugSessionModel) -> Void)

    static let dev = PreviewFixtures.devPCID
    static let page = Program.pageTargetID
    static let api = Program.Script.api
    static let checkout = Program.Script.checkout

    /// When the sample session launched.
    static var launched: Date { Date.now.addingTimeInterval(-6 * 60) }

    static let recent = [
        RecentLaunch(path: Program.appPath, args: ["--env=staging"], cwd: "C:\\Shop\\desktop",
                     breakpoints: Program.sampleBreakpoints().prefix(2).map(\.spec), pauseOnExceptions: .all),
        RecentLaunch(path: Program.kioskPath, args: ["--kiosk", "--store=강남점"], pauseOnStart: true),
    ]

    static func properties(_ objectID: String) -> DebugPropertyPage? {
        if objectID == "min-local" {
            return DebugPropertyPage(properties: [
                DebugProperty(name: "e", value: RemoteValue(type: "object", className: "Object", valuePreview: "{id: \"O-1043\", total: 46800}")),
                DebugProperty(name: "t", value: RemoteValue(type: "number", valuePreview: "46800")),
            ])
        }
        return Program.properties(objectID: objectID).map { DebugPropertyPage(properties: $0) }
    }

    static func source(_ script: Program.Script) -> (targetID: String, source: DebugSource) {
        (script.targetID, DebugSource(scriptID: script.scriptID, url: script.url, text: script.source))
    }

    static var scripts: [DebugScript] {
        [Program.Script.main, .index, .checkout, .api, .receipt].map { DebugScript(scriptID: $0.scriptID, targetID: $0.targetID, url: $0.url) } + [
            DebugScript(scriptID: "7", targetID: Program.mainTargetID, url: "node:internal/main/run_main_module"),
            DebugScript(scriptID: "120", targetID: page, url: "app://shop-admin/renderer/vendor.min.js"),
        ]
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
        var sessions: [Seeded] = []
        var listPhase: LoadPhase = .loaded
        var starting: DebuggerModel.Starting?
        var recent: [RecentLaunch] = []
        var centerSplit = 0.56
        var navigatorSplit = 0.46
        var navigatorShown: Bool?
        var showsScriptNavigator = false
    }

    static func seed(_ store: AppStore, hostID: String, _ tab: Tab) -> DebuggerModel? {
        guard let host = store.host(hostID) else { return nil }
        let model = store.debugger(for: host)
        let sessions = tab.sessions.map { item -> DebugSessionModel in
            let session = model.makeSession(item.info)
            item.seed(session)
            return session
        }
        model.seed(sessions, listPhase: tab.listPhase, starting: tab.starting, recent: tab.recent, navigatorShown: tab.navigatorShown,
                   showsScriptNavigator: tab.showsScriptNavigator)
        model.centerSplit = tab.centerSplit
        model.navigatorSplit = tab.navigatorSplit
        return model
    }

    /// A scene of `setup` with `tab` seeded into the debugger of `debuggerHost`, the selected
    /// computer by default.
    static func scene(_ name: String, _ setup: Setup, _ tab: Tab = Tab(), size: CGSize = WorkspaceScenes.size, debuggerHost: String? = nil,
                      sheet: ((AppStore, DebuggerModel) -> AnyView)? = nil) -> SnapshotScene {
        SnapshotScene(name: name, size: size) {
            let store = WorkspaceScenes.store(setup)
            let model = seed(store, hostID: debuggerHost ?? setup.selected, tab)
            if let sheet, let model {
                SheetStandIn(base: SplitStandIn(store: store)) { sheet(store, model) }
            } else {
                SplitStandIn(store: store)
            }
        }
    }

    // MARK: Sessions

    static func running() -> Seeded {
        (Program.session(hostID: dev, created: launched, targets: Program.targets(mainGen: 2, pageGen: 4) + [
            DebugTarget(id: "7D21C0A4E5B34A1F9C6B2E8D0F4A3B11", role: DebugTargetRole.renderer.rawValue, type: "service_worker",
                        url: "app://shop-admin/sw.js", title: "sw.js", attached: false),
         ], breakpoints: Program.sampleBreakpoints()),
         { session in
            session.seed(console: firstRoundConsole(), scripts: scripts, history: ["order.total"])
        })
    }

    static func paused(expanded: [VariableTree.Path] = [], frame: Int = 0, console: [ConsoleEntry]? = nil,
                       evaluations: [(expression: String, frame: String?, result: EvaluationResult)] = [], expandedResults: [Int] = [],
                       expandedEntries: Set<Int> = [], selection: Set<String> = [], draft: String = "") -> Seeded {
        let step = submitStep(api, "const payload = {")
        let state = Program.pauseState(flow: .submit, step: step, gen: 3, reason: .breakpoint, hitBreakpoints: ["bp-1"])
        return (pausedSession(step: step), { session in
            session.seed(pause: state, frame: frame, properties: properties, expanded: expanded, console: console ?? secondRoundConsole(),
                         evaluations: evaluations, expandedResults: expandedResults, expandedEntries: expandedEntries,
                         consoleSelection: selection, sources: [source(api), source(checkout), source(.index)], scripts: scripts,
                         history: ["order.total", "order.customer", "payload"], draft: draft)
        })
    }

    static func pausedOnException() -> Seeded {
        let step = Program.steps(.coupon).count - 1
        let state = Program.pauseState(flow: .coupon, step: step, gen: 5, reason: .exception)
        let info = Program.session(hostID: dev, created: launched, state: .paused,
                                   targets: Program.targets(mainGen: 2, pageGen: 5, pagePaused: true), breakpoints: Program.sampleBreakpoints())
        return (info, { session in
            session.seed(pause: state, properties: properties, expanded: [["exception"], ["0:local", "order"]], console: firstRoundConsole(),
                         sources: [source(api), source(checkout), source(.index)], scripts: scripts)
        })
    }

    static func closed() -> Seeded {
        (Program.session(hostID: dev, created: launched, state: .closed, targets: Program.targets(mainGen: 2, pageGen: 6),
                         breakpoints: Program.sampleBreakpoints(), closeReason: .processExited, exitCode: 1),
         { session in
            session.seed(console: Program.consoleEntries(start: launched))
        })
    }

    /// An attach session to VS Code's main process; `reason` closes it.
    static func attached(reason: DebugCloseReason? = nil, debugID: String = "d-code") -> Seeded {
        let target = DebugTarget(id: "c0de0000-1111-2222-3333-444455556666", role: DebugTargetRole.main.rawValue, type: "node",
                                 url: "file:///C:/Users/taeyoung/AppData/Local/Programs/Microsoft%20VS%20Code/resources/app/out/main.js",
                                 title: "Code — main", gen: 1)
        let info = DebugSession(debugID: debugID, hostID: dev, kind: .attach, name: "Code", pid: 7000, ports: DebugPorts(inspect: 9229),
                                state: reason == nil ? .running : .closed, targets: [target],
                                createdBy: DebugActor(actor: "ai", clientName: "Claude Code"),
                                createdAt: Timestamp(date: launched.addingTimeInterval(-20 * 60)),
                                closedAt: reason == nil ? nil : Timestamp(date: launched.addingTimeInterval(-2 * 60)), closeReason: reason)
        return (info, { session in
            session.seed(console: [
                ConsoleEntry(seq: 1, time: Timestamp(date: launched.addingTimeInterval(-19 * 60)), targetID: target.id, level: .info,
                             text: "[main] update#setState idle"),
            ])
        })
    }

    /// The kiosk app that ignores `--inspect`: only its renderer is attached.
    static func rendererOnly() -> Seeded {
        let info = DebugSession(debugID: "d-kiosk", hostID: dev, kind: .launch, name: "Shop Kiosk", path: Program.kioskPath,
                                args: ["--kiosk", "--store=강남점"], pid: 11820, ports: DebugPorts(remoteDebugging: 52021),
                                targets: [DebugTarget(id: "B7D2E4F6A8C0E2F4A6B8C0D2E4F6A8B0", role: DebugTargetRole.renderer.rawValue,
                                                      type: "page", url: "app://shop-kiosk/index.html", title: "Shop 키오스크", gen: 2)],
                                mainUnavailable: .inspectorDisabled, createdAt: Timestamp(date: launched))
        return (info, { session in
            session.seed(console: [
                ConsoleEntry(seq: 1, time: Timestamp(date: launched.addingTimeInterval(4)), targetID: info.targets[0].id, level: .info,
                             text: "키오스크 모드 · 강남점"),
            ])
        })
    }

    /// The tray app that turns remote debugging off: only main is attached, paused on its first line.
    static func mainOnly() -> Seeded {
        var target = Program.mainTarget(gen: 1, paused: true)
        target.title = "Shop Sync — main"
        let info = DebugSession(debugID: "d-sync", hostID: dev, kind: .launch, name: "Shop Sync", path: Program.syncPath, pauseOnStart: true,
                                pid: 11904, ports: DebugPorts(inspect: 52031), state: .paused, targets: [target],
                                rendererUnavailable: .remoteDebuggingDisabled, createdAt: Timestamp(date: launched))
        let main = Program.Script.main
        let frame = DebugCallFrame(frameID: "start-0", function: "", scriptID: main.scriptID, url: main.url, line: 1, column: 1,
                                   scopeChain: [DebugScope(type: "local", objectID: "min-local")])
        let state = DebugPauseState(targetID: target.id, gen: 1, paused: true, reason: .start, callFrames: [frame])
        return (info, { session in
            session.seed(pause: state, properties: properties, sources: [source(main)], scripts: scripts)
        })
    }

    /// Shop Admin launched with 처음부터 멈춤: main is paused on its first line while the renderer
    /// is still being attached.
    static func rendererWaiting() -> Seeded {
        let target = Program.mainTarget(gen: 1, paused: true)
        let info = DebugSession(debugID: "d-admin-start", hostID: dev, kind: .launch, name: "Shop Admin", path: Program.appPath,
                                args: ["--env=staging"], pauseOnStart: true, pid: 10244, ports: DebugPorts(inspect: 52011, remoteDebugging: 52012),
                                state: .paused, targets: [target], rendererUnavailable: .waiting, createdAt: Timestamp(date: launched))
        let main = Program.Script.main
        let frame = DebugCallFrame(frameID: "start-0", function: "", scriptID: main.scriptID, url: main.url, line: 1, column: 1,
                                   scopeChain: [DebugScope(type: "local", objectID: "min-local")])
        let state = DebugPauseState(targetID: target.id, gen: 1, paused: true, reason: .start, callFrames: [frame])
        return (info, { session in
            session.seed(pause: state, properties: properties, sources: [source(main)], scripts: scripts)
        })
    }

    /// An attach session just detached, leaving the Node inspector it turned on open.
    static func inspectorLeftOpen() -> Seeded {
        let attachedSession = attached(reason: .requested)
        return (attachedSession.info, { session in
            session.seed(notice: DiagnosticsNotice(tone: .info, message: "PC의 검사기 포트가 열린 채 남았습니다 · 앱을 다시 시작하면 닫힙니다"))
        })
    }

    /// A pause the page reported whose call stack could not be read.
    static func stateFailed() -> Seeded {
        (pausedSession(step: 0, gen: 7), { session in
            session.seed(console: secondRoundConsole(), scripts: scripts,
                         stateFailure: OperationError(code: "TIMEOUT", message: "대상이 5초 안에 호출 스택을 돌려주지 않았습니다"))
        })
    }

    /// A pause requested on a page that runs no code.
    static func pausePending() -> Seeded {
        (Program.session(hostID: dev, created: launched, targets: Program.targets(mainGen: 2, pageGen: 4, pagePausePending: true),
                         breakpoints: Program.sampleBreakpoints()),
         { session in
            session.seed(console: firstRoundConsole(), scripts: scripts)
        })
    }

    /// A pause deep in the one line of a minified bundle.
    static func minified() -> Seeded {
        let url = "app://shop-admin/renderer/vendor.min.js"
        let pieces = (0..<1800).map { index in
            index == 1200 ? "function Ze(e,t){debugger;return t>4e4?Qe(e,.1*t):e}" : "var a\(index)=function(e){return e*\(index)+0x1F};"
        }
        let text = "/*! shop-admin vendor 2.4.1 */" + pieces.joined()
        let column = (text.components(separatedBy: "debugger;").first?.utf16.count ?? 0) + 1
        let frame = DebugCallFrame(frameID: "min-0", function: "Ze", scriptID: "120", url: url, line: 1, column: column,
                                   scopeChain: [DebugScope(type: "local", objectID: "min-local")])
        let caller = DebugCallFrame(frameID: "min-1", function: "postOrder", scriptID: api.scriptID, url: api.url,
                                    line: api.line("const payload = {"), column: 5)
        let state = DebugPauseState(targetID: page, gen: 9, paused: true, reason: .other, callFrames: [frame, caller])
        return (pausedSession(step: 0, gen: 9), { session in
            session.seed(pause: state, properties: properties, console: firstRoundConsole(),
                         sources: [(page, DebugSource(scriptID: "120", url: url, text: text)), source(api)], scripts: scripts)
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

    static func attachSheet(mode: AttachSheet.Mode? = nil, selectedPID: Int = 7000) -> (AppStore, DebuggerModel) -> AnyView {
        { store, model in
            guard let host = store.host(dev) else { return AnyView(EmptyView()) }
            let worker = RemoteProcess(pid: 9330, ppid: 7656, name: "node.exe", path: "C:\\Program Files\\nodejs\\node.exe",
                                       cmd: "\"C:\\Program Files\\nodejs\\node.exe\" --inspect=9230 C:\\Shop\\worker\\index.js",
                                       memKb: 96_000, cpuPct: 0.4, user: "taeyoung", startedByDeskhand: false)
            return AnyView(AttachSheet(model: model, host: host, processes: store.backend, mode: mode,
                                       list: PreviewFixtures.processes(hostID: dev, user: "taeyoung") + [worker], selectedPID: selectedPID,
                                       dismiss: {}))
        }
    }

    static func breakpointSheet() -> (AppStore, DebuggerModel) -> AnyView {
        { _, model in
            guard let session = model.selected else { return AnyView(EmptyView()) }
            return AnyView(BreakpointSheet(session: session, script: "checkout.js", line: "31", condition: "order.total > 40000", dismiss: {}))
        }
    }

    static func conditionSheet() -> (AppStore, DebuggerModel) -> AnyView {
        { _, model in
            guard let session = model.selected, let breakpoint = session.info.breakpoints.first(where: { $0.breakpointID == "bp-2" }) else {
                return AnyView(EmptyView())
            }
            return AnyView(BreakpointSheet(session: session, editing: breakpoint, condition: "order.total > 40000 && order.coupon", dismiss: {}))
        }
    }

    // MARK: Scenes

    static var all: [SnapshotScene] {
        let none = Setup(selected: dev, tab: .debugger, inspector: nil)
        let step = submitStep(api, "const payload = {")
        let evaluate = { (expression: String) in Program.evaluate(expression, flow: .submit, step: step, gen: 3, frame: 0) }
        let console = secondRoundConsole()
        let exceptionSeq = console.first { $0.consoleSource == .exception }?.seq ?? 0
        let orderSeq = console.first { $0.text.hasPrefix("주문 제출") }?.seq ?? 0
        return [
            scene("debugger-idle", none, Tab(recent: recent)),
            scene("debugger-idle-first", Setup(selected: PreviewFixtures.meetingPCID, tab: .debugger, inspector: nil)),
            scene("debugger-launch-sheet", none, Tab(recent: recent), sheet: launchSheet(LaunchDraft(recent[0]))),
            scene("debugger-launch-picker", none, Tab(recent: recent), sheet: launchPicker()),
            scene("debugger-launch-failed", none, Tab(recent: recent), sheet: launchSheet(
                LaunchDraft(path: Program.appPath, arguments: "--env=staging"),
                error: OperationError(code: "EXITED_EARLY", message: "앱이 디버그 포트를 열기 전에 종료되었습니다 (종료 코드 0)"))),
            scene("debugger-launch-timeout", none, Tab(recent: recent), sheet: launchSheet(
                LaunchDraft(path: Program.notElectronPath),
                error: OperationError(code: "TIMEOUT", message: "20초 안에 디버그 포트가 열리지 않아 앱을 종료했습니다"))),
            scene("debugger-attach-sheet", none, Tab(recent: recent), sheet: attachSheet()),
            scene("debugger-attach-process-windows", none, Tab(recent: recent), sheet: attachSheet(mode: .process, selectedPID: 9330)),
            scene("debugger-attach-process-no-port", none, Tab(recent: recent), sheet: attachSheet(mode: .process, selectedPID: 7000)),
            scene("debugger-running", none, Tab(sessions: [running()])),
            scene("debugger-paused", none, Tab(sessions: [paused()])),
            scene("debugger-paused-exception", none, Tab(sessions: [pausedOnException()])),
            scene("debugger-variables-expanded", none, Tab(sessions: [paused(expanded: [
                ["0:local", "order"], ["0:local", "order", "customer"], ["0:local", "order", "lines"], ["0:local", "order", "lines", "0"], ["1:module"],
            ])])),
            scene("debugger-caller-frame", none, Tab(sessions: [paused(expanded: [["0:local", "order"]], frame: 1)])),
            scene("debugger-console", none, Tab(sessions: [paused(
                console: console,
                evaluations: [
                    ("order.total", "postOrder", .value(evaluate("order.total"))),
                    ("order.customer", "postOrder", .value(evaluate("order.customer"))),
                    ("order.summary.total", "postOrder", .value(evaluate("order.summary.total"))),
                    ("totl", "postOrder", .value(evaluate("totl"))),
                ],
                expandedResults: [1], expandedEntries: [exceptionSeq], selection: ["e\(orderSeq)", "e\(orderSeq + 1)"],
                draft: "order.lines.map((line) => line.sku)")],
                centerSplit: 0.3)),
            scene("debugger-breakpoint-sheet", none, Tab(sessions: [running()]), sheet: breakpointSheet()),
            scene("debugger-condition-sheet", none, Tab(sessions: [running()]), sheet: conditionSheet()),
            scene("debugger-script-navigator", none, Tab(sessions: [paused()], showsScriptNavigator: true)),
            scene("debugger-minified", none, Tab(sessions: [minified()])),
            scene("debugger-state-failed", none, Tab(sessions: [stateFailed()])),
            scene("debugger-pause-pending", none, Tab(sessions: [pausePending()])),
            scene("debugger-renderer-only", none, Tab(sessions: [rendererOnly()])),
            scene("debugger-main-only", none, Tab(sessions: [mainOnly()])),
            scene("debugger-renderer-waiting", none, Tab(sessions: [rendererWaiting()])),
            scene("debugger-inspector-left-open", none, Tab(sessions: [running(), inspectorLeftOpen()])),
            scene("debugger-closed", none, Tab(sessions: [closed()])),
            scene("debugger-detached", none, Tab(sessions: [running(), attached(reason: .requested)])),
            scene("debugger-connection-lost", none, Tab(sessions: [attached(reason: .connectionLost)])),
            scene("debugger-narrow", Setup(selected: dev, tab: .debugger), Tab(sessions: [paused()])),
            scene("debugger-narrow-navigator", Setup(selected: dev, tab: .debugger), Tab(sessions: [paused()], navigatorShown: true)),
            scene("debugger-loading", none, Tab(listPhase: .loading)),
            scene("debugger-error", none, Tab(listPhase: .failed(OperationError(ClientError.timeout(method: "debug.list"))))),
            scene("debugger-offline", Setup(selected: PreviewFixtures.designID, tab: .debugger, inspector: nil)),
            scene("debugger-offline-sessions", Setup(selected: PreviewFixtures.designID, tab: .debugger, inspector: nil),
                  Tab(sessions: [closed()])),
            scene("debugger-unsupported", Setup(selected: PreviewFixtures.qaLaptopID, tab: .debugger, inspector: nil)),
            scene("workspace-debugger-paused-elsewhere", Setup(selected: dev, tab: .screen, inspector: nil), Tab(sessions: [paused()])),
            scene("workspace-debugger-paused-other-host", Setup(selected: PreviewFixtures.meetingPCID, tab: .terminal, inspector: nil),
                  Tab(sessions: [paused()]), debuggerHost: dev),
        ]
    }
}
