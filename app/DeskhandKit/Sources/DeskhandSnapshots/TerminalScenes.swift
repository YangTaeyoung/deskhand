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
                    PasteConfirmSheet(request: PasteRequest(sessionID: workspace.selected?.id ?? UUID(), clipboard: paste),
                                      shell: workspace.selected?.title ?? "PowerShell", canSend: true, confirm: { _ in }, cancel: {})
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

    /// A deploy script copied from a terminal with a color escape still in it.
    static let deployScript = [
        "cd C:\\Shop", "git fetch origin", "git switch release/1.18", "git pull --ff-only", "npm ci",
        "npm run lint", "npm run test -- --reporter=dot", "npm run build", "Remove-Item -Recurse -Force .\\dist-old",
        "Rename-Item .\\dist-current dist-old", "Copy-Item .\\dist .\\dist-current -Recurse",
        "Copy-Item .\\dist\\* \\\\nas\\deploy\\shop -Recurse -Force",
        "Write-Host \"\u{1b}[32m배포 준비 완료\u{1b}[0m — 승인 요청은 #release 채널에 남겨 주세요. 확인 전에는 운영 서버에 반영하지 않습니다.\"",
        "Get-ChildItem \\\\nas\\deploy\\shop | Measure-Object",
    ].joined(separator: "\n") + "\n"

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
            scene("terminal-reconnecting", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .reconnecting(OperationError(code: "TIMEOUT", message: "에이전트가 5초 안에 셸 스트림을 열지 않았습니다")),
                             commands: ["cd Downloads", "dir"]),
                         Tab(shell: .cmd, commands: ["ver"])]),
            scene("terminal-exited", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .exited(0), commands: ["whoami", "hostname", "exit"]),
                         Tab(shell: .cmd, commands: ["dir"])]),
            scene("terminal-exited-full", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .exited(1), commands: ["dir", "cd Downloads", "dir", "Get-Process", "Restart-Shop -Force", "exit 1"])]),
            scene("terminal-many-tabs", Setup(selected: dev, tab: .terminal, controls: [dev: human]),
                  tabs: [
                      Tab(commands: ["cd Downloads", "dir"]),
                      Tab(commands: ["dir"], cwd: "C:\\Shop"),
                      Tab(shell: .pwsh, commands: ["cd Documents"]),
                      Tab(commands: ["cd Desktop"]),
                      Tab(commands: ["dir"], cwd: "C:\\Shop\\logs"),
                      Tab(shell: .cmd, commands: ["ver"]),
                      Tab(shell: .pwsh, commands: ["cd C:\\qa", "Get-Process"]),
                      Tab(phase: .reconnecting(nil), commands: ["dir"], cwd: "C:\\ProgramData"),
                  ],
                  selected: 6),
            scene("terminal-paste-confirm", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(commands: ["dir"], cwd: "C:\\Shop")],
                  paste: deployScript),
            scene("terminal-empty", Setup(selected: dev, tab: .terminal, controls: [dev: human]),
                  tabs: [], detached: [detachedJob(hostID: dev)]),
            scene("terminal-error", Setup(selected: PreviewFixtures.qaLaptopID, tab: .terminal, inspector: nil),
                  tabs: [Tab(shell: .pwsh, phase: .failed(OperationError(
                      code: "INVALID_ARGUMENT", message: "QA 노트북에 PowerShell 7(pwsh.exe)이 설치되어 있지 않습니다")),
                      output: "")]),
        ]
    }
}
