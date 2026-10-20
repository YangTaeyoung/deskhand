import DeskhandClient
import struct DeskhandClient.Host
import SwiftUI

/// Menu bar commands: add a computer (⌘N) and a new terminal tab (⌘T), closing (⌘W closes the
/// terminal tab while the terminal tab shows shells, ⇧⌘W the window), the workspace tabs
/// (⌘1–⌘7) and inspector (⌥⌘0) in the View menu, the 컴퓨터 menu with the control actions
/// (emergency stop ⇧⌘.) and host actions for the selected computer, and the 터미널 menu. In the
/// files and processes tabs 제거… has no shortcut; ⌘⌫ there moves remote files to the recycle
/// bin or ends the selected process.
public struct DeskhandCommands: Commands {
    let store: AppStore

    public init(store: AppStore) {
        self.store = store
    }

    public var body: some Commands {
        CommandGroup(replacing: .newItem) {
            Button(String.ui("menu.addHost", "컴퓨터 추가…")) { store.presentAddHost() }
                .keyboardShortcut("n")
                .disabled(store.phase != .ready)
        }
        CommandGroup(after: .newItem) {
            NewTerminalCommand(store: store)
        }
        CommandGroup(replacing: .saveItem) {
            CloseCommands(store: store)
        }
        CommandGroup(before: .toolbar) {
            WorkspaceViewCommands(store: store, layout: store.layout)
        }
        CommandMenu(String.ui("menu.computer", "컴퓨터")) {
            if let host = store.selectedHost {
                ControlCommands(store: store, host: host)
                Divider()
                Button(String.ui("action.doctor", "진단 실행")) { store.presentDoctor(for: host.hostID) }
                    .keyboardShortcut("d", modifiers: [.command, .shift])
                switch host.connection {
                case .offline:
                    Button(String.ui("action.connect", "연결")) { store.connect(host.hostID) }
                case .blocked:
                    Button(String.ui("action.repair", "다시 페어링")) { store.repair(host.hostID) }
                case .ready, .degraded, .connecting, .reconnecting:
                    Button(String.ui("action.disconnect", "연결 끊기")) { store.disconnect(host.hostID) }
                }
                Divider()
                Button(String.ui("action.remove", "제거…")) { store.pendingRemoval = host }
                    .keyboardShortcut([.files, .processes].contains(store.layout.tab) ? nil : KeyboardShortcut(.delete, modifiers: [.command]))
            } else {
                Button(ControlActions.Action.stop.title) {}
                    .keyboardShortcut(".", modifiers: [.command, .shift])
                    .disabled(true)
                Divider()
                Button(String.ui("action.doctor", "진단 실행")) {}
                    .disabled(true)
            }
        }
        CommandMenu(String.ui("menu.terminal", "터미널")) {
            TerminalMenuCommands(store: store)
        }
    }
}

/// View menu items: one checkable item per workspace tab and the inspector panes.
private struct WorkspaceViewCommands: View {
    let store: AppStore
    @Bindable var layout: WorkspaceLayout

    var body: some View {
        let hasWorkspace = store.selectedHost != nil
        ForEach(WorkspaceTab.allCases) { tab in
            Toggle(tab.title, isOn: Binding(get: { layout.tab == tab }, set: { _ in layout.tab = tab }))
                .keyboardShortcut(tab.keyEquivalent, modifiers: .command)
                .disabled(!hasWorkspace)
        }
        Divider()
        Button(layout.showsInspector
               ? String.ui("menu.hideInspector", "인스펙터 가리기")
               : String.ui("menu.showInspector", "인스펙터 보기")) {
            layout.showsInspector.toggle()
        }
        .keyboardShortcut("0", modifiers: [.command, .option])
        .disabled(!hasWorkspace)
        Button(String.ui("menu.inspectorActivity", "활동 기록")) { layout.toggleInspector(.activity) }
            .keyboardShortcut("1", modifiers: [.command, .option])
            .disabled(!hasWorkspace)
        Button(String.ui("menu.inspectorInfo", "컴퓨터 정보")) { layout.toggleInspector(.info) }
            .keyboardShortcut("2", modifiers: [.command, .option])
            .disabled(!hasWorkspace)
        Divider()
    }
}

/// 제어권 가져오기, AI에게 넘기기 and 긴급 정지 (⇧⌘.) for the selected computer. Taking and
/// giving back follow the control banner; the emergency stop is always available.
private struct ControlCommands: View {
    let store: AppStore
    let host: Host

    var body: some View {
        let actions = store.controlActions
        let hostID = host.hostID
        let kind = ControlBannerKind(host: host, control: store.control(for: hostID))
        let contextualRunning = actions.isRunning(.takeOver, on: hostID) || actions.isRunning(.giveBack, on: hostID)
        Button(ControlActions.Action.takeOver.title) { actions.takeOver(hostID) }
            .disabled(contextualRunning || !kind.offersTakeOver)
        Button(ControlActions.Action.giveBack.title) { actions.giveBack(hostID) }
            .disabled(contextualRunning || !kind.offersGiveBack)
        Button(ControlActions.Action.stop.title) { actions.stop(hostID) }
            .keyboardShortcut(".", modifiers: [.command, .shift])
            .disabled(actions.isRunning(.stop, on: hostID))
    }
}
