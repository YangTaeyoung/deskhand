import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Terminal tab scenes at 1440×900. Tabs are stand-in sessions fed with what the preview
/// shells print; nothing attaches to the backend.
@MainActor
enum TerminalScenes {
    typealias Setup = WorkspaceScenes.Setup

    struct Tab {
        var shell: TerminalShell = .powershell
        var phase: TerminalSession.Phase = .live
        var commands: [String] = []
        var cwd: String?
        /// Output shown instead of the transcript of `commands`.
        var output: String?
        var title: String?
    }

    static func session(_ tab: Tab, host: Host) -> TerminalSession {
        let output = tab.output ?? PreviewFixtures.terminalTranscript(host: host, shell: tab.shell, commands: tab.commands, cwd: tab.cwd)
        return TerminalSession.standIn(hostID: host.hostID, shell: tab.shell, phase: tab.phase, output: output,
                                       size: TerminalSize(cols: 104, rows: 40), title: tab.title)
    }

    static func scene(_ name: String, _ setup: Setup, tabs: [Tab], selected: Int = 0, detached: [Job] = [],
                      paste: String? = nil) -> SnapshotScene {
        SnapshotScene(name: name, size: WorkspaceScenes.size) {
            let store = WorkspaceScenes.store(setup)
            let _ = seed(store, hostID: setup.selected, tabs: tabs, selected: selected, detached: detached)
            if let paste, let host = store.host(setup.selected) {
                let workspace = store.terminals(for: host)
                SheetStandIn(base: SplitStandIn(store: store)) {
                    PasteConfirmSheet(request: PasteRequest(sessionID: workspace.selected?.id ?? UUID(), text: paste),
                                      shell: workspace.selected?.title ?? "PowerShell", confirm: { _ in }, cancel: {})
                }
            } else {
                SplitStandIn(store: store)
            }
        }
    }

    static func seed(_ store: AppStore, hostID: String, tabs: [Tab], selected: Int, detached: [Job]) {
        guard let host = store.host(hostID) else { return }
        store.terminalPreferences.fontSize = Double(DS.Terminal.fontSize)
        store.terminalPreferences.confirmsMultilinePaste = true
        store.terminalPreferences.defaultShell = .powershell
        let sessions = tabs.map { session($0, host: host) }
        store.terminals(for: host).seed(sessions, selected: selected, detachedJobs: detached)
    }

    static func detachedJob(hostID: String) -> Job {
        let now = Date.now
        return Job(jobID: "j-preview-pty-dev", hostID: hostID, kind: "pty", status: "running",
                   summary: "PowerShell · C:\\Shop · npm run dev", createdAt: Timestamp(date: now.addingTimeInterval(-25 * 60)),
                   updatedAt: Timestamp(date: now.addingTimeInterval(-60)),
                   result: .object(["shell": .string("powershell"), "pid": .int(9216), "client_name": .string("Claude Code")]))
    }

    static var all: [SnapshotScene] {
        let dev = PreviewFixtures.devPCID
        let human = ControlState(owner: "human", gen: 6, aiPaused: false)
        return [
            scene("terminal", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(commands: ["whoami", "cd Downloads", "dir", "Get-Process"])]),
            scene("terminal-multi-tab", Setup(selected: dev, tab: .terminal, controls: [dev: human]),
                  tabs: [
                      Tab(commands: ["cd Downloads", "dir"]),
                      Tab(shell: .pwsh, commands: ["cd C:\\Shop", "$PSVersionTable", "Get-Foo"]),
                      Tab(shell: .cmd, commands: ["ver", "whoami"]),
                      Tab(phase: .reconnecting(nil), commands: ["hostname"]),
                  ],
                  selected: 1, detached: [detachedJob(hostID: dev)]),
            scene("terminal-disconnected", Setup(selected: PreviewFixtures.buildServerID, tab: .terminal, inspector: nil),
                  tabs: [Tab(phase: .reconnecting(OperationError(code: "OFFLINE", message: "빌드 서버에 연결되어 있지 않습니다")),
                             commands: ["dir", "Get-Process"], cwd: "C:\\Shop")]),
            scene("terminal-exited", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .exited(0), commands: ["whoami", "hostname", "exit"]),
                         Tab(shell: .cmd, commands: ["dir"])]),
            scene("terminal-paste-confirm", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(commands: ["dir"], cwd: "C:\\Shop")],
                  paste: "git fetch origin\ngit switch release/1.18\nnpm ci\nnpm run build\nRemove-Item -Recurse -Force .\\dist-old\n"
                      + "Copy-Item .\\dist\\* \\\\nas\\deploy\\shop -Recurse\nWrite-Host \"배포 준비 완료\"\n"),
            scene("terminal-empty", Setup(selected: dev, tab: .terminal, controls: [dev: human]),
                  tabs: [], detached: [detachedJob(hostID: dev)]),
            scene("terminal-error", Setup(selected: PreviewFixtures.qaLaptopID, tab: .terminal, inspector: nil),
                  tabs: [Tab(shell: .pwsh, phase: .failed(OperationError(
                      code: "INVALID_ARGUMENT", message: "QA 노트북에 PowerShell 7(pwsh.exe)이 설치되어 있지 않습니다")),
                      output: "")]),
        ]
    }
}
