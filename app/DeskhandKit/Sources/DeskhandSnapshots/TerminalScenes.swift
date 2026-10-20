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

    /// The 16 ANSI colors as text and as backgrounds, then status lines drawn with black text on
    /// colored backgrounds and white text on black.
    static var ansiChart: String {
        let esc = "\u{1b}["
        let names = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"]
        func cell(_ text: String, _ width: Int) -> String { text + String(repeating: " ", count: max(0, width - text.count)) }
        var lines = ["\u{1b}]7;file://DEV-PC/C:/Users/taeyoung\u{7}PS C:\\Users\\taeyoung> .\\colors.ps1", ""]
        lines.append("글자색 0–7  " + names.enumerated().map { "\(esc)3\($0.offset)m\(cell("\($0.offset) \($0.element)", 11))\(esc)0m" }.joined())
        lines.append("글자색 8–15 " + names.enumerated().map { "\(esc)9\($0.offset)m\(cell("\($0.offset + 8) \($0.element)", 11))\(esc)0m" }.joined())
        lines.append("")
        lines.append("배경색 0–7  " + names.indices.map { "\(esc)4\($0)m\(cell("  \($0)", 11))\(esc)0m" }.joined())
        lines.append("배경색 8–15 " + names.indices.map { "\(esc)10\($0)m\(cell("  \($0 + 8)", 11))\(esc)0m" }.joined())
        lines.append("검정 글자   " + names.indices.map { "\(esc)30;4\($0)m\(cell("  30/4\($0)", 11))\(esc)0m" }.joined())
        lines.append("흰 글자     " + names.indices.map { "\(esc)97;4\($0)m\(cell("  97/4\($0)", 11))\(esc)0m" }.joined())
        lines.append("")
        lines.append("\(esc)30;43m NORMAL \(esc)0m\(esc)30;47m  checkout\\Payment.tsx [+]  \(esc)0m\(esc)30;46m  utf-8 │ Ln 128, Col 17  \(esc)0m")
        lines.append("\(esc)30;42m [0] powershell* \(esc)0m\(esc)30;43m 1:npm run dev \(esc)0m\(esc)30;47m  DEV-PC  21:42 \(esc)0m")
        lines.append("\(esc)97;40m  97/40  흰 글자 · 검정 배경 — git (main) ✔  \(esc)0m \(esc)37;40m  37/40  흰 글자 · 검정 배경 — 21:42  \(esc)0m")
        lines.append("\(esc)30;46m  PID USER      PRI  NI  VIRT   RES  CPU% MEM%  Command \(esc)0m")
        lines.append(" 7412 taeyoung   20   0 1.2G  402M  12.4  4.9  \(esc)32mShop.exe\(esc)0m")
        lines.append(" 9312 taeyoung   20   0  612M 118M   3.1  1.4  \(esc)36mnode\(esc)0m vite --host")
        lines.append("")
        lines.append("\u{1b}]7;file://DEV-PC/C:/Users/taeyoung\u{7}PS C:\\Users\\taeyoung> ")
        return lines.joined(separator: "\r\n")
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
            scene("terminal-reconnecting", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .reconnecting(OperationError(code: "TIMEOUT", message: "에이전트가 5초 안에 셸 스트림을 열지 않았습니다")),
                             commands: ["cd Downloads", "dir"]),
                         Tab(shell: .cmd, commands: ["ver"])]),
            scene("terminal-exited", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .exited(0), commands: ["whoami", "hostname", "exit"]),
                         Tab(shell: .cmd, commands: ["dir"])]),
            scene("terminal-exited-full", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(phase: .exited(1), commands: ["dir", "cd Downloads", "dir", "Get-Process", "Restart-Shop -Force", "exit 1"])]),
            scene("terminal-ansi", Setup(selected: dev, tab: .terminal, inspector: nil, controls: [dev: human]),
                  tabs: [Tab(output: ansiChart)]),
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
                  selected: 3),
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
