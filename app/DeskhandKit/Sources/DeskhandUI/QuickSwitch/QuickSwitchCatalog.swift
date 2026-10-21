import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// The items the quick switch offers for the store's current state: the selected computer's
/// tabs, the commands that apply now, every computer, and recent transfers, open debug sessions
/// and QA runs.
@MainActor
public enum QuickSwitchCatalog {
    /// Transfers, debug sessions and QA runs offered at most, each.
    static let workLimit = 5

    public static func items(store: AppStore) -> [QuickSwitchItem] {
        var items: [QuickSwitchItem] = []
        if let host = store.selectedHost {
            items += WorkspaceTab.allCases.map { tab(for: $0, host: host) }
            items += hostCommands(store: store, host: host)
        }
        items += appCommands(store: store)
        items += store.hosts.map(hostItem)
        items += transferItems(store: store)
        items += debugItems(store: store)
        items += qaItems(store: store)
        return items
    }

    // MARK: Tabs and computers

    private static func tab(for tab: WorkspaceTab, host: Host) -> QuickSwitchItem {
        QuickSwitchItem(id: "tab:\(tab.rawValue)", kind: .tab, title: tab.title, subtitle: nil,
                        keywords: keywords(for: tab) + [host.name], symbol: tab.symbol, tone: .accent, action: .showTab(tab))
    }

    private static func keywords(for tab: WorkspaceTab) -> [String] {
        switch tab {
        case .screen: ["screen", "display", "remote desktop", "원격 화면"]
        case .terminal: ["terminal", "shell", "console", "powershell", "cmd"]
        case .files: ["files", "transfer", "explorer", "파일 전송"]
        case .processes: ["processes", "task manager", "작업 관리자"]
        case .logs: ["logs", "events", "crash dumps", "이벤트 로그"]
        case .debugger: ["debugger", "devtools", "electron", "breakpoints", "중단점"]
        case .qa: ["qa", "test", "scenario", "시나리오"]
        }
    }

    private static func hostItem(_ host: Host) -> QuickSwitchItem {
        QuickSwitchItem(id: "host:\(host.hostID)", kind: .host, title: host.name, subtitle: host.statusLine(),
                        keywords: [host.sshAlias, host.address, host.hostID], symbol: host.osSymbol,
                        tone: host.connection == .ready || host.connection == .degraded ? .accent : .neutral,
                        connection: host.connection, action: .selectHost(host.hostID))
    }

    // MARK: Commands

    private static func hostCommands(store: AppStore, host: Host) -> [QuickSwitchItem] {
        let hostID = host.hostID
        let kind = ControlBannerKind(host: host, control: store.control(for: hostID))
        let actions = store.controlActions
        var items: [QuickSwitchItem] = []
        if TerminalAvailability(host: host) == .ready {
            items.append(command(.newTerminal, title: .ui("quickSwitch.command.newTerminal", "새 터미널"), subtitle: host.name,
                                 keywords: ["new terminal", "new shell", "powershell"], symbol: "plus.rectangle.on.rectangle",
                                 shortcut: "⌘T"))
        }
        items.append(command(.runScenario, title: .ui("quickSwitch.command.runScenario", "시나리오 실행…"), subtitle: host.name,
                             keywords: ["run scenario", "qa run", "test"], symbol: "play.rectangle"))
        items.append(command(.showTransferQueue, title: .ui("quickSwitch.command.transferQueue", "전송 큐 보기"), subtitle: host.name,
                             keywords: ["transfer queue", "downloads", "uploads"], symbol: "arrow.up.arrow.down.circle"))
        if kind.offersTakeOver, !actions.isRunning(.takeOver, on: hostID) {
            items.append(command(.takeControl, title: ControlActions.Action.takeOver.title, subtitle: host.name,
                                 keywords: ["take control", "take over"], symbol: "hand.raised", tone: .human))
        }
        if kind.offersGiveBack, !actions.isRunning(.giveBack, on: hostID) {
            items.append(command(.giveBackToAI, title: ControlActions.Action.giveBack.title, subtitle: host.name,
                                 keywords: ["give back to ai", "resume ai"], symbol: "sparkles", tone: .ai))
        }
        items.append(command(.emergencyStop, title: ControlActions.Action.stop.title, subtitle: host.name,
                             keywords: ["emergency stop", "stop ai", "panic"], symbol: "stop.circle", tone: .danger,
                             shortcut: "⇧⌘."))
        items.append(command(.runDoctor, title: .ui("quickSwitch.command.doctor", "진단 실행"), subtitle: host.name,
                             keywords: ["doctor", "diagnose"], symbol: "stethoscope", tone: .neutral, shortcut: "⇧⌘D"))
        return items
    }

    private static func appCommands(store: AppStore) -> [QuickSwitchItem] {
        var items: [QuickSwitchItem] = []
        if store.phase == .ready {
            items.append(command(.addHost, title: .ui("quickSwitch.command.addHost", "PC 추가…"), subtitle: nil,
                                 keywords: ["add computer", "pair", "new host"], symbol: "plus.circle", tone: .neutral,
                                 shortcut: "⌘N"))
        }
        items.append(command(.openSettings, title: .ui("quickSwitch.command.settings", "설정 열기"), subtitle: nil,
                             keywords: ["settings", "preferences", "환경설정"], symbol: "gearshape", tone: .neutral,
                             shortcut: "⌘,"))
        return items
    }

    private static func command(_ command: QuickSwitchCommand, title: String, subtitle: String?, keywords: [String],
                                symbol: String, tone: QuickSwitchItem.Tone = .accent, shortcut: String? = nil) -> QuickSwitchItem {
        QuickSwitchItem(id: "command:\(command.rawValue)", kind: .command, title: title, subtitle: subtitle, keywords: keywords,
                        symbol: symbol, tone: tone, shortcut: shortcut, action: .command(command))
    }

    // MARK: Recent work

    private static func transferItems(store: AppStore) -> [QuickSwitchItem] {
        store.knownTransfers.prefix(workLimit).compactMap { transfer in
            guard let host = store.host(transfer.hostID) else { return nil }
            let presentation = TransferPresentation(transfer, hostName: host.name)
            let tone: QuickSwitchItem.Tone = switch presentation.tone {
            case .active: .accent
            case .success: .success
            case .warning: .warning
            case .danger: .danger
            case .neutral: .neutral
            }
            return QuickSwitchItem(id: "transfer:\(transfer.transferID)", kind: .transfer, title: presentation.name,
                                   subtitle: .ui("quickSwitch.transfer.subtitle", "\(host.name) · \(presentation.headline)"),
                                   keywords: ["transfer"],
                                   symbol: presentation.direction.isUpload ? "arrow.up.circle" : "arrow.down.circle", tone: tone,
                                   action: .showTransfer(hostID: host.hostID, transferID: transfer.transferID))
        }
    }

    private static func debugItems(store: AppStore) -> [QuickSwitchItem] {
        store.knownDebugSessions.prefix(workLimit).compactMap { session in
            guard let host = store.host(session.info.hostID) else { return nil }
            let paused = !session.pausedTargets.isEmpty
            let state = paused ? String.ui("quickSwitch.debug.paused", "일시정지됨") : .ui("quickSwitch.debug.running", "실행 중")
            return QuickSwitchItem(id: "debug:\(session.id)", kind: .debugSession, title: session.info.name,
                                   subtitle: .ui("quickSwitch.debug.subtitle", "\(host.name) · \(state)"),
                                   keywords: ["debug session"], symbol: paused ? "pause.circle" : "ladybug",
                                   tone: paused ? .warning : .accent,
                                   action: .showDebugSession(hostID: host.hostID, debugID: session.id))
        }
    }

    private static func qaItems(store: AppStore) -> [QuickSwitchItem] {
        store.qa.records.values.map(\.run)
            .sorted { $0.startedAt.date > $1.startedAt.date }
            .prefix(workLimit)
            .compactMap { run in
                guard let host = store.host(run.hostID) else { return nil }
                let status = run.runStatus
                let tone: QuickSwitchItem.Tone = switch status {
                case .running: .accent
                case .passed: .success
                case .failed, .error: .danger
                case .blocked: .warning
                case .cancelled, nil: .neutral
                }
                return QuickSwitchItem(id: "qa:\(run.runID)", kind: .qaRun, title: run.name,
                                       subtitle: .ui("quickSwitch.qa.subtitle", "\(host.name) · \(QAFormat.title(status))"),
                                       keywords: ["qa run"], symbol: QAFormat.symbol(status), tone: tone,
                                       action: .showQARun(hostID: host.hostID, runID: run.runID))
            }
    }
}

// MARK: Opening items

extension AppStore {
    /// Opens `item` from the quick switch: remembers it, closes the palette and does what it
    /// stands for. `openSettings` opens the settings window.
    public func activate(_ item: QuickSwitchItem, openSettings: () -> Void) {
        quickSwitch.record(item.id)
        quickSwitch.dismiss()
        perform(item.action, openSettings: openSettings)
    }

    private func perform(_ action: QuickSwitchAction, openSettings: () -> Void) {
        switch action {
        case .selectHost(let hostID):
            selectedHostID = hostID
        case .showTab(let tab):
            layout.tab = tab
        case .showTransfer(let hostID, let transferID):
            selectedHostID = hostID
            layout.tab = .files
            filesPreferences.queueExpanded = true
            if let host = host(hostID) { files(for: host).transfers.expanded.insert(transferID) }
        case .showDebugSession(let hostID, let debugID):
            selectedHostID = hostID
            layout.tab = .debugger
            if let host = host(hostID) { debugger(for: host).selectedSessionID = debugID }
        case .showQARun(let hostID, let runID):
            selectedHostID = hostID
            layout.tab = .qa
            qa.selection[qa.scope(for: hostID)] = runID
        case .command(let command):
            perform(command, openSettings: openSettings)
        }
    }

    private func perform(_ command: QuickSwitchCommand, openSettings: () -> Void) {
        switch command {
        case .addHost:
            presentAddHost()
            return
        case .openSettings:
            openSettings()
            return
        default:
            break
        }
        guard let hostID = selectedHostID else { return }
        switch command {
        case .newTerminal:
            let context = TerminalCommandContext(store: self)
            context.open(context.preferredShell)
        case .runScenario:
            layout.tab = .qa
            qa.presentRunSheet(hostID: hostID)
        case .showTransferQueue:
            layout.tab = .files
            filesPreferences.queueExpanded = true
        case .emergencyStop:
            controlActions.stop(hostID)
        case .takeControl:
            controlActions.takeOver(hostID)
        case .giveBackToAI:
            controlActions.giveBack(hostID)
        case .runDoctor:
            presentDoctor(for: hostID)
        case .addHost, .openSettings:
            break
        }
    }
}
