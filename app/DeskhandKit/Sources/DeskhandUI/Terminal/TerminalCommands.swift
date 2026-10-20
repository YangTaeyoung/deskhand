import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The selected computer's terminal tab as the menu commands see it.
@MainActor
struct TerminalCommandContext {
    let store: AppStore

    var host: Host? { store.selectedHost }
    var workspace: TerminalWorkspace? { host.map { store.terminals(for: $0) } }
    /// True while the window shows the terminal tab of the selected computer.
    var isShown: Bool { host != nil && store.layout.tab == .terminal }
    /// True while the terminal tab shows at least one shell.
    var hasTabs: Bool { isShown && !(workspace?.sessions.isEmpty ?? true) }
    var canOpen: Bool { host.map { TerminalAvailability(host: $0) == .ready } ?? false }

    /// The default shell when the computer offers it, otherwise PowerShell.
    var preferredShell: TerminalShell {
        let preferred = store.terminalPreferences.defaultShell
        guard let host else { return preferred }
        return TerminalShell.available(on: host).contains(preferred) ? preferred : .powershell
    }

    /// Shows the terminal tab and opens `shell` in a new tab.
    func open(_ shell: TerminalShell) {
        store.layout.tab = .terminal
        workspace?.open(shell)
    }
}

/// 파일 › 새 터미널 탭 (⌘T): shows the terminal tab and opens the default shell.
struct NewTerminalCommand: View {
    let store: AppStore

    var body: some View {
        let context = TerminalCommandContext(store: store)
        Button(String.ui("menu.terminal.newTab", "새 터미널 탭")) { context.open(context.preferredShell) }
            .keyboardShortcut("t")
            .disabled(!context.canOpen)
    }
}

/// 파일 › 탭 닫기 (⌘W) and 윈도우 닫기 (⇧⌘W) while the terminal tab shows shells; 닫기 (⌘W) otherwise.
struct CloseCommands: View {
    let store: AppStore

    var body: some View {
        let context = TerminalCommandContext(store: store)
        if context.hasTabs {
            Button(String.ui("menu.terminal.closeTab", "탭 닫기")) {
                if TerminalStackView.isShown(in: NSApp.keyWindow) {
                    context.workspace?.requestClose()
                } else {
                    NSApp.keyWindow?.performClose(nil)
                }
            }
            .keyboardShortcut("w")
            Button(String.ui("menu.closeWindow", "윈도우 닫기")) { NSApp.keyWindow?.performClose(nil) }
                .keyboardShortcut("w", modifiers: [.command, .shift])
        } else {
            Button(String.ui("menu.close", "닫기")) { NSApp.keyWindow?.performClose(nil) }
                .keyboardShortcut("w")
        }
    }
}

/// The 터미널 menu: new tabs by shell, detached shells, tab navigation (⇧⌘] ⇧⌘[), detach and
/// end, 스크롤백 지우기 (⌘L), text size (⌘= ⌘- ⌘0) and the paste confirmation setting.
/// Shortcuts other than the menu-wide ones apply only while the terminal tab is shown.
struct TerminalMenuCommands: View {
    let store: AppStore

    var body: some View {
        let context = TerminalCommandContext(store: store)
        let workspace = context.workspace
        let selected = context.hasTabs ? workspace?.selected : nil
        @Bindable var preferences = store.terminalPreferences
        Menu(String.ui("menu.terminal.newWith", "새 탭에서 열기")) {
            ForEach(context.host.map(TerminalShell.available(on:)) ?? TerminalShell.allCases.filter { $0 != .pwsh }) { shell in
                Button(shell.title) { context.open(shell) }
            }
        }
        .disabled(!context.canOpen)
        Menu(String.ui("menu.terminal.reopen", "분리된 셸 다시 열기")) {
            ForEach(workspace?.detachedJobs ?? [], id: \.jobID) { job in
                Button(job.summary) {
                    store.layout.tab = .terminal
                    workspace?.reopen(job)
                }
            }
        }
        .disabled(!context.canOpen || (workspace?.detachedJobs.isEmpty ?? true))
        Divider()
        Button(String.ui("menu.terminal.next", "다음 탭")) { workspace?.selectAdjacent(forward: true) }
            .keyboardShortcut(context.hasTabs ? KeyboardShortcut("]", modifiers: [.command, .shift]) : nil)
            .disabled((workspace?.sessions.count ?? 0) < 2 || !context.isShown)
        Button(String.ui("menu.terminal.previous", "이전 탭")) { workspace?.selectAdjacent(forward: false) }
            .keyboardShortcut(context.hasTabs ? KeyboardShortcut("[", modifiers: [.command, .shift]) : nil)
            .disabled((workspace?.sessions.count ?? 0) < 2 || !context.isShown)
        Divider()
        Button(String.ui("terminal.detach", "분리하기")) {
            if let selected { workspace?.close(selected, ending: false) }
        }
        .disabled(!(selected?.isShellRunning ?? false))
        Button(String.ui("terminal.end", "셸 종료…")) { workspace?.pendingClose = selected }
            .disabled(!(selected?.isShellRunning ?? false))
        Divider()
        Button(String.ui("terminal.clearScrollback", "스크롤백 지우기")) { selected?.clearScrollback() }
            .keyboardShortcut(context.hasTabs ? KeyboardShortcut("l") : nil)
            .disabled(selected == nil)
        Divider()
        Button(String.ui("terminal.font.bigger", "글자 크게")) { workspace?.adjustFontSize(by: 1) }
            .keyboardShortcut(context.hasTabs ? KeyboardShortcut("=") : nil)
            .disabled(selected == nil || preferences.fontSize >= Double(DS.Terminal.maximumFontSize))
        Button(String.ui("terminal.font.smaller", "글자 작게")) { workspace?.adjustFontSize(by: -1) }
            .keyboardShortcut(context.hasTabs ? KeyboardShortcut("-") : nil)
            .disabled(selected == nil || preferences.fontSize <= Double(DS.Terminal.minimumFontSize))
        Button(String.ui("terminal.font.reset", "기본 크기")) { workspace?.resetFontSize() }
            .keyboardShortcut(context.hasTabs ? KeyboardShortcut("0") : nil)
            .disabled(selected == nil)
        Divider()
        Toggle(String.ui("terminal.paste.confirmSetting", "여러 줄을 붙여 넣기 전에 확인"), isOn: $preferences.confirmsMultilinePaste)
    }
}

extension TerminalStackView {
    /// Terminal containers on screen.
    private static let shown = NSHashTable<TerminalStackView>.weakObjects()

    /// True when `window` holds a terminal container.
    static func isShown(in window: NSWindow?) -> Bool {
        guard let window else { return false }
        return shown.allObjects.contains { $0.window === window }
    }

    func register() {
        Self.shown.add(self)
    }
}
