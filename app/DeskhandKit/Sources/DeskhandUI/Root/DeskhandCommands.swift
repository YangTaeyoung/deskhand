import AppKit
import DeskhandClient
import struct DeskhandClient.Host
import SwiftUI

/// Menu bar commands: add a computer (⌘N) and a new terminal tab (⌘T), closing (⌘W closes the
/// terminal tab while the terminal tab shows shells, ⇧⌘W the window), the quick switch (⌘K),
/// the workspace tabs (⌘1–⌘7, which also close the palette) and inspector (⌥⌘0) in the View
/// menu, the PC menu with the control actions
/// (emergency stop ⇧⌘.) and host actions for the selected computer, the 터미널 menu and the 디버그
/// menu (F8, F10, F11, ⇧F11 while the debugger tab is shown). 제거… takes ⌘⌫ only while the
/// sidebar's PC list has focus (`RemoveHostShortcut`); elsewhere ⌘⌫ stays with text fields and
/// the files and processes tables. The Edit menu keeps its native 찾기 submenu; its 찾기… (⌘F)
/// reaches `FindCommandResponder`, which focuses the visible tab's search field.
public struct DeskhandCommands: Commands {
    let store: AppStore

    public init(store: AppStore) {
        self.store = store
    }

    public var body: some Commands {
        CommandGroup(replacing: .newItem) {
            Button(String.ui("menu.addHost", "PC 추가…")) { store.presentAddHost() }
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
        CommandMenu(String.ui("menu.computer", "PC")) {
            if let host = store.selectedHost {
                ControlCommands(store: store, host: host)
                Divider()
                Button(String.ui("action.doctor", "진단 실행")) { store.presentDoctor(for: host.hostID) }
                    .keyboardShortcut("d", modifiers: [.command, .shift])
                switch host.connection {
                case .offline:
                    Button(String.ui("action.connect", "연결")) { store.connect(host.hostID) }
                case .blocked:
                    Button(String.ui("action.repair.ask", "다시 페어링…")) { store.requestRepair(host) }
                        .disabled(store.isRepairing(host.hostID))
                case .ready, .degraded, .connecting, .reconnecting:
                    Button(String.ui("action.disconnect.ask", "연결 끊기…")) { store.requestDisconnect(host) }
                }
                Divider()
                Button(String.ui("action.remove", "제거…")) { store.pendingRemoval = host }
                    .keyboardShortcut(RemoveHostShortcut.shortcut(
                        sidebarFocused: store.layout.sidebarFocused,
                        editingText: RemoveHostShortcut.isEditingText(NSApp.keyWindow?.firstResponder)))
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
        CommandMenu(String.ui("menu.debug", "디버그")) {
            DebuggerMenuCommands(store: store)
        }
        CommandMenu(String.ui("menu.qa", "QA")) {
            QAMenuCommands(store: store, layout: store.layout)
        }
    }
}

/// When ⌘⌫ removes the selected PC.
enum RemoveHostShortcut {
    /// ⌘⌫ while the sidebar's PC list has focus and no text is being edited; otherwise none.
    static func shortcut(sidebarFocused: Bool, editingText: Bool) -> KeyboardShortcut? {
        sidebarFocused && !editingText ? KeyboardShortcut(.delete, modifiers: .command) : nil
    }

    /// True for a text view, including the field editor of a text field being edited, and for a
    /// text field.
    static func isEditingText(_ responder: NSResponder?) -> Bool {
        responder is NSText || responder is NSTextField
    }
}

/// View menu items: the quick switch, one checkable item per workspace tab, the inspector panes
/// and 새로 고침 — disabled on tabs without it, and without ⌘R on the QA tab. Choosing a tab
/// closes the quick switch.
private struct WorkspaceViewCommands: View {
    let store: AppStore
    @Bindable var layout: WorkspaceLayout

    var body: some View {
        let hasWorkspace = store.selectedHost != nil
        let palette = store.quickSwitch
        Button(String.ui("menu.quickSwitch", "빠른 전환…")) { palette.toggle() }
            .keyboardShortcut("k")
            .disabled(store.phase != .ready && store.phase != .reconnecting)
        Divider()
        ForEach(WorkspaceTab.allCases) { tab in
            Toggle(tab.title, isOn: Binding(get: { layout.tab == tab }, set: { _ in store.showTab(tab) }))
                .keyboardShortcut(tab.keyEquivalent, modifiers: .command)
                .disabled(!hasWorkspace)
        }
        Divider()
        Button(layout.isInspectorVisible
               ? String.ui("menu.hideInspector", "인스펙터 가리기")
               : String.ui("menu.showInspector", "인스펙터 보기")) {
            layout.toggleInspectorVisibility()
        }
        .keyboardShortcut("0", modifiers: [.command, .option])
        .disabled(!hasWorkspace)
        Button(String.ui("menu.inspectorActivity", "활동 기록")) { layout.toggleInspector(.activity) }
            .keyboardShortcut("1", modifiers: [.command, .option])
            .disabled(!hasWorkspace)
        Button(String.ui("menu.inspectorInfo", "PC 정보")) { layout.toggleInspector(.info) }
            .keyboardShortcut("2", modifiers: [.command, .option])
            .disabled(!hasWorkspace)
        Divider()
        Button(String.ui("menu.refresh", "새로 고침")) { layout.requestRefresh() }
            .keyboardShortcut(WorkspaceLayout.refreshShortcut(layout.tab))
            .disabled(!hasWorkspace || !WorkspaceLayout.canRefresh(layout.tab))
        Divider()
    }
}

/// The QA menu: 시나리오 실행… (⌘R while the QA tab shows), 다시 실행… (⇧⌘R) and QA 취소 (⌘.)
/// for the run selected in the QA tab. QA 취소 sends only the first cancel; stopping the teardown
/// asks in the tab.
private struct QAMenuCommands: View {
    let store: AppStore
    @Bindable var layout: WorkspaceLayout

    var body: some View {
        let host = store.selectedHost
        let inTab = host != nil && layout.tab == .qa
        let run = host.flatMap { store.qa.selectedRun(in: store.qa.scope(for: $0.hostID)) }
        Button(String.ui("qa.run", "시나리오 실행…")) {
            if let host { store.qa.presentRunSheet(hostID: host.hostID) }
        }
        .keyboardShortcut(inTab ? KeyboardShortcut("r") : nil)
        .disabled(host == nil)
        Button(String.ui("qa.action.rerun", "다시 실행…")) {
            if let run { store.qa.presentRunSheet(hostID: run.hostID, rerun: run) }
        }
        .keyboardShortcut(inTab ? KeyboardShortcut("r", modifiers: [.command, .shift]) : nil)
        .disabled(!inTab || run?.scenarioPath == nil)
        Divider()
        Button(String.ui("menu.qa.cancel", "QA 취소")) {
            if let run { Task { await store.qa.cancel(run.runID) } }
        }
        .keyboardShortcut(inTab ? KeyboardShortcut(".") : nil)
        .disabled(!inTab || run.map { store.qa.cancelState(of: $0.runID) != .available } ?? true)
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
    }
}
