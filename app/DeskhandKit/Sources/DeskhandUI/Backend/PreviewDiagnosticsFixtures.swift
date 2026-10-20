import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// Sample processes, system summaries, Windows events and crash dumps of the preview computers.
/// Times are relative to `now`.
extension PreviewFixtures {
    /// PID of the agent on every preview computer.
    public static let agentPID = 3120
    /// PID of the PowerShell the terminal tab's detached shell runs in.
    public static let deskhandShellPID = 9216
    /// A process whose `proc.kill` reply the preview loses although the process ends.
    public static let unknownKillName = "Shop.exe"
    /// A protected process the preview refuses to end.
    public static let deniedKillName = "MsMpEng.exe"

    /// How long the computer has been up.
    static func uptime(hostID: String) -> TimeInterval {
        switch hostID {
        case devPCID: 2 * 86_400 + 5 * 3_600 + 12 * 60
        case qaLaptopID: 6 * 3_600 + 40 * 60
        case stagingID: 19 * 86_400 + 2 * 3_600
        default: 3 * 86_400 + 7 * 3_600
        }
    }

    // MARK: Processes

    /// A process of the fixture tree: CPU and memory before jitter, the start as seconds after boot
    /// (negative: seconds before `now`).
    struct ProcessSeed: Sendable {
        var pid: Int
        var ppid: Int
        var name: String
        var path: String?
        var cmd: String?
        var memMB: Double
        var cpu: Double
        var user: String?
        /// Seconds after boot when positive, seconds before now when negative.
        var start: Double
        var deskhand = false
    }

    /// The fixture processes of a computer.
    static func processSeeds(hostID: String, user: String) -> [ProcessSeed] {
        let sys32 = "C:\\Windows\\System32"
        let me = "\(hostID == devPCID ? "DEV-PC" : hostID.uppercased())\\\(user)"
        let system = "NT AUTHORITY\\SYSTEM"
        let local = "NT AUTHORITY\\LOCAL SERVICE"
        let network = "NT AUTHORITY\\NETWORK SERVICE"
        func exe(_ name: String, in folder: String = sys32) -> String { folder + "\\" + name }
        var seeds: [ProcessSeed] = [
            ProcessSeed(pid: 0, ppid: 0, name: "System Idle Process", memMB: 0.008, cpu: 0, user: system, start: 0),
            ProcessSeed(pid: 4, ppid: 0, name: "System", memMB: 0.14, cpu: 0.3, user: system, start: 0),
            ProcessSeed(pid: 148, ppid: 4, name: "Registry", memMB: 64, cpu: 0, user: system, start: 1),
            ProcessSeed(pid: 528, ppid: 4, name: "smss.exe", path: exe("smss.exe"), cmd: "\\SystemRoot\\System32\\smss.exe", memMB: 1.2, cpu: 0, user: system, start: 2),
            ProcessSeed(pid: 2980, ppid: 4, name: "Memory Compression", memMB: 412, cpu: 0.1, user: system, start: 9),
            ProcessSeed(pid: 720, ppid: 704, name: "csrss.exe", path: exe("csrss.exe"), cmd: "%SystemRoot%\\system32\\csrss.exe ObjectDirectory=\\Windows", memMB: 5.8, cpu: 0.1, user: system, start: 4),
            ProcessSeed(pid: 812, ppid: 704, name: "wininit.exe", path: exe("wininit.exe"), cmd: "wininit.exe", memMB: 6.9, cpu: 0, user: system, start: 4),
            ProcessSeed(pid: 960, ppid: 812, name: "services.exe", path: exe("services.exe"), cmd: "C:\\Windows\\system32\\services.exe", memMB: 11.2, cpu: 0.2, user: system, start: 5),
            ProcessSeed(pid: 980, ppid: 812, name: "lsass.exe", path: exe("lsass.exe"), cmd: "C:\\Windows\\system32\\lsass.exe", memMB: 24.6, cpu: 0.1, user: system, start: 5),
            ProcessSeed(pid: 1004, ppid: 812, name: "fontdrvhost.exe", path: exe("fontdrvhost.exe"), memMB: 3.1, cpu: 0, user: "Font Driver Host\\UMFD-0", start: 5),
            ProcessSeed(pid: 900, ppid: 888, name: "csrss.exe", path: exe("csrss.exe"), cmd: "%SystemRoot%\\system32\\csrss.exe ObjectDirectory=\\Windows", memMB: 7.4, cpu: 0.4, user: system, start: 6),
            ProcessSeed(pid: 944, ppid: 888, name: "winlogon.exe", path: exe("winlogon.exe"), cmd: "winlogon.exe", memMB: 9.8, cpu: 0, user: system, start: 6),
            ProcessSeed(pid: 1288, ppid: 944, name: "dwm.exe", path: exe("dwm.exe"), cmd: "\"dwm.exe\"", memMB: 142, cpu: 2.4, user: "Window Manager\\DWM-1", start: 7),
            ProcessSeed(pid: 1012, ppid: 944, name: "fontdrvhost.exe", path: exe("fontdrvhost.exe"), memMB: 6.2, cpu: 0, user: "Font Driver Host\\UMFD-1", start: 7),
        ]
        let svchosts: [(Int, String, String, Double, Double)] = [
            (1104, "-k DcomLaunch -p", system, 28, 0.2), (1180, "-k RPCSS -p", network, 14, 0.1),
            (1240, "-k LocalServiceNetworkRestricted -p", local, 9, 0.1), (1392, "-k netsvcs -p", system, 62, 0.4),
            (1460, "-k LocalService -p", local, 11, 0), (1532, "-k NetworkService -p", network, 19, 0.1),
            (1604, "-k LocalSystemNetworkRestricted -p", system, 22, 0.1), (1720, "-k UnistackSvcGroup", me, 16, 0),
            (1844, "-k utcsvc -p", system, 31, 0.2), (1916, "-k appmodel -p", system, 18, 0), (2032, "-s Schedule", system, 12, 0),
            (2144, "-k ClipboardSvcGroup -p", me, 7, 0),
        ]
        for (pid, args, account, mem, cpu) in svchosts {
            seeds.append(ProcessSeed(pid: pid, ppid: 960, name: "svchost.exe", path: exe("svchost.exe"),
                                     cmd: "C:\\Windows\\system32\\svchost.exe \(args)", memMB: mem, cpu: cpu, user: account, start: 8))
        }
        let defender = "C:\\ProgramData\\Microsoft\\Windows Defender\\Platform\\4.18.24090.11-0"
        seeds += [
            ProcessSeed(pid: 2260, ppid: 960, name: "MsMpEng.exe", path: defender + "\\MsMpEng.exe", cmd: "\"" + defender + "\\MsMpEng.exe\"",
                        memMB: 238, cpu: 1.1, user: system, start: 11),
            ProcessSeed(pid: 2312, ppid: 960, name: "spoolsv.exe", path: exe("spoolsv.exe"), memMB: 10.4, cpu: 0, user: system, start: 12),
            ProcessSeed(pid: 2408, ppid: 960, name: "SearchIndexer.exe", path: exe("SearchIndexer.exe"), cmd: "C:\\Windows\\system32\\SearchIndexer.exe /Embedding",
                        memMB: 46, cpu: 0.3, user: system, start: 30),
            ProcessSeed(pid: 2484, ppid: 960, name: "NVDisplay.Container.exe", path: "C:\\Windows\\System32\\DriverStore\\FileRepository\\nv_dispi.inf_amd64\\Display.NvContainer\\NVDisplay.Container.exe",
                        memMB: 21, cpu: 0.1, user: system, start: 10),
            ProcessSeed(pid: 2560, ppid: 960, name: "WmiPrvSE.exe", path: exe("wbem\\WmiPrvSE.exe"), memMB: 18, cpu: 0.6, user: network, start: -300),
            ProcessSeed(pid: 2632, ppid: 1104, name: "dllhost.exe", path: exe("dllhost.exe"), cmd: "C:\\Windows\\system32\\DllHost.exe /Processid:{973D20D7-562D-44B9-B70B-5A0F49CCDF3F}",
                        memMB: 8.2, cpu: 0, user: me, start: -1_800),
            ProcessSeed(pid: agentPID, ppid: 2032, name: "deskhand-agent.exe", path: "C:\\Program Files\\Deskhand\\deskhand-agent.exe",
                        cmd: "\"C:\\Program Files\\Deskhand\\deskhand-agent.exe\" run", memMB: 38, cpu: 1.6, user: me, start: 70),
            ProcessSeed(pid: 3188, ppid: 1104, name: "sihost.exe", path: exe("sihost.exe"), memMB: 27, cpu: 0.1, user: me, start: 64),
            ProcessSeed(pid: 3204, ppid: 1392, name: "taskhostw.exe", path: exe("taskhostw.exe"), cmd: "taskhostw.exe {222A245B-E637-4AE9-A93F-A59CA119A75E}",
                        memMB: 14, cpu: 0, user: me, start: 64),
            ProcessSeed(pid: 3380, ppid: 1104, name: "ctfmon.exe", path: exe("ctfmon.exe"), memMB: 19, cpu: 0.2, user: me, start: 66),
            ProcessSeed(pid: 3440, ppid: 1104, name: "StartMenuExperienceHost.exe",
                        path: "C:\\Windows\\SystemApps\\Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy\\StartMenuExperienceHost.exe",
                        memMB: 71, cpu: 0, user: me, start: 68),
            ProcessSeed(pid: 3512, ppid: 1104, name: "SearchHost.exe", path: "C:\\Windows\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\SearchHost.exe",
                        memMB: 118, cpu: 0, user: me, start: 68),
            ProcessSeed(pid: 3588, ppid: 1104, name: "RuntimeBroker.exe", path: exe("RuntimeBroker.exe"), cmd: "C:\\Windows\\System32\\RuntimeBroker.exe -Embedding",
                        memMB: 22, cpu: 0, user: me, start: 69),
            ProcessSeed(pid: 3660, ppid: 1104, name: "RuntimeBroker.exe", path: exe("RuntimeBroker.exe"), cmd: "C:\\Windows\\System32\\RuntimeBroker.exe -Embedding",
                        memMB: 9, cpu: 0, user: me, start: 72),
            ProcessSeed(pid: 3720, ppid: 1104, name: "TextInputHost.exe", path: "C:\\Windows\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\TextInputHost.exe",
                        memMB: 44, cpu: 0.1, user: me, start: 70),
            ProcessSeed(pid: 3804, ppid: 1104, name: "ShellExperienceHost.exe", path: "C:\\Windows\\SystemApps\\ShellExperienceHost_cw5n1h2txyewy\\ShellExperienceHost.exe",
                        memMB: 52, cpu: 0, user: me, start: 70),
            ProcessSeed(pid: 3888, ppid: 1104, name: "Widgets.exe", path: "C:\\Program Files\\WindowsApps\\MicrosoftWindows.Client.WebExperience_524.1301.0.0_x64__cw5n1h2txyewy\\Dashboard\\Widgets.exe",
                        memMB: 36, cpu: 0, user: me, start: 75),
            ProcessSeed(pid: 5520, ppid: 5480, name: "explorer.exe", path: "C:\\Windows\\explorer.exe", cmd: "C:\\Windows\\Explorer.EXE",
                        memMB: 186, cpu: 0.6, user: me, start: 63),
            ProcessSeed(pid: 5644, ppid: 5520, name: "SecurityHealthSystray.exe", path: exe("SecurityHealthSystray.exe"), memMB: 9, cpu: 0, user: me, start: 80),
            ProcessSeed(pid: 5712, ppid: 5520, name: "OneDrive.exe", path: "C:\\Users\\\(user)\\AppData\\Local\\Microsoft\\OneDrive\\OneDrive.exe",
                        cmd: "\"C:\\Users\\\(user)\\AppData\\Local\\Microsoft\\OneDrive\\OneDrive.exe\" /background", memMB: 96, cpu: 0.2, user: me, start: 82),
        ]
        guard hostID == devPCID else {
            seeds += [
                ProcessSeed(pid: 6100, ppid: 5520, name: "msedge.exe", path: "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
                            memMB: 210, cpu: 1.2, user: me, start: -5_400),
                ProcessSeed(pid: 6180, ppid: 6100, name: "msedge.exe", path: "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
                            cmd: "msedge.exe --type=renderer", memMB: 120, cpu: 0.8, user: me, start: -5_390),
                ProcessSeed(pid: 6820, ppid: 5520, name: "Shop.exe", path: "C:\\Shop\\bin\\Shop.exe", cmd: "\"C:\\Shop\\bin\\Shop.exe\"",
                            memMB: 312, cpu: 3.4, user: me, start: -2_400),
            ]
            return seeds
        }
        let chrome = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"
        let code = "C:\\Users\\\(user)\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe"
        let node = "C:\\Program Files\\nodejs\\node.exe"
        let dotnet = "C:\\Program Files\\dotnet\\dotnet.exe"
        let pwsh = "C:\\Program Files\\PowerShell\\7\\pwsh.exe"
        let powershell = exe("WindowsPowerShell\\v1.0\\powershell.exe")
        let terminal = "C:\\Program Files\\WindowsApps\\Microsoft.WindowsTerminal_1.21.2911.0_x64__8wekyb3d8bbwe"
        seeds += [
            ProcessSeed(pid: 6100, ppid: 5520, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\"", memMB: 412, cpu: 2.1, user: me, start: -18_000),
            ProcessSeed(pid: 6132, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=gpu-process --gpu-preferences=WAAAAAAAAADgAAA",
                        memMB: 286, cpu: 3.8, user: me, start: -17_990),
            ProcessSeed(pid: 6148, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=utility --utility-sub-type=network.mojom.NetworkService",
                        memMB: 64, cpu: 0.4, user: me, start: -17_990),
            ProcessSeed(pid: 6164, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=utility --utility-sub-type=storage.mojom.StorageService",
                        memMB: 22, cpu: 0, user: me, start: -17_990),
            ProcessSeed(pid: 6212, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=renderer --renderer-client-id=7 (localhost:5173 — Shop 관리자)",
                        memMB: 521, cpu: 6.2, user: me, start: -9_000),
            ProcessSeed(pid: 6240, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=renderer --renderer-client-id=9 (GitHub)",
                        memMB: 188, cpu: 0.7, user: me, start: -7_200),
            ProcessSeed(pid: 6268, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=renderer --renderer-client-id=12 (Jira — SHOP-2214)",
                        memMB: 233, cpu: 0.3, user: me, start: -3_600),
            ProcessSeed(pid: 6296, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=renderer --extension-process",
                        memMB: 97, cpu: 0.1, user: me, start: -17_980),
            ProcessSeed(pid: 6324, ppid: 6100, name: "chrome.exe", path: chrome, cmd: "\"\(chrome)\" --type=crashpad-handler", memMB: 8, cpu: 0, user: me, start: -17_995),
            ProcessSeed(pid: 7000, ppid: 5520, name: "Code.exe", path: code, cmd: "\"\(code)\"", memMB: 148, cpu: 1.4, user: me, start: -10_800),
            ProcessSeed(pid: 7036, ppid: 7000, name: "Code.exe", path: code, cmd: "\"\(code)\" --type=gpu-process", memMB: 132, cpu: 2.2, user: me, start: -10_790),
            ProcessSeed(pid: 7064, ppid: 7000, name: "Code.exe", path: code, cmd: "\"\(code)\" --type=renderer (Shop — Visual Studio Code)",
                        memMB: 604, cpu: 4.6, user: me, start: -10_785),
            ProcessSeed(pid: 7092, ppid: 7000, name: "Code.exe", path: code, cmd: "\"\(code)\" --type=utility --utility-sub-type=node.mojom.NodeService (확장 호스트)",
                        memMB: 388, cpu: 1.9, user: me, start: -10_780),
            ProcessSeed(pid: 7120, ppid: 7092, name: "node.exe", path: node,
                        cmd: "\"\(node)\" \"c:\\Users\\\(user)\\.vscode\\extensions\\ms-vscode.vscode-typescript-next\\tsserver.js\" --serverMode partialSemantic",
                        memMB: 512, cpu: 3.1, user: me, start: -10_700),
            ProcessSeed(pid: 7148, ppid: 7092, name: "dotnet.exe", path: dotnet,
                        cmd: "\"\(dotnet)\" \"c:\\Users\\\(user)\\.vscode\\extensions\\ms-dotnettools.csharp\\.roslyn\\Microsoft.CodeAnalysis.LanguageServer.dll\"",
                        memMB: 846, cpu: 2.8, user: me, start: -10_650),
            ProcessSeed(pid: 7600, ppid: 5520, name: "WindowsTerminal.exe", path: terminal + "\\WindowsTerminal.exe", memMB: 92, cpu: 0.5, user: me, start: -14_400),
            ProcessSeed(pid: 7628, ppid: 7600, name: "OpenConsole.exe", path: terminal + "\\OpenConsole.exe", memMB: 7, cpu: 0, user: me, start: -14_398),
            ProcessSeed(pid: 7656, ppid: 7600, name: "pwsh.exe", path: pwsh, cmd: "\"\(pwsh)\" -NoLogo", memMB: 78, cpu: 0, user: me, start: -14_398),
            ProcessSeed(pid: 7712, ppid: 7656, name: "dotnet.exe", path: dotnet, cmd: "\"\(dotnet)\" watch run --project C:\\Shop\\src\\Shop.Api",
                        memMB: 214, cpu: 0.9, user: me, start: -7_000),
            ProcessSeed(pid: 7740, ppid: 7712, name: "Shop.Api.exe", path: "C:\\Shop\\src\\Shop.Api\\bin\\Debug\\net9.0\\Shop.Api.exe",
                        cmd: "C:\\Shop\\src\\Shop.Api\\bin\\Debug\\net9.0\\Shop.Api.exe --urls http://localhost:5080", memMB: 296, cpu: 4.2, user: me, start: -900),
            ProcessSeed(pid: 8800, ppid: 5520, name: "Shop.exe", path: "C:\\Shop\\bin\\Shop.exe", cmd: "\"C:\\Shop\\bin\\Shop.exe\" --env=staging",
                        memMB: 438, cpu: 11.6, user: me, start: -2_400),
            ProcessSeed(pid: 8840, ppid: 8800, name: "msedgewebview2.exe", path: "C:\\Program Files (x86)\\Microsoft\\EdgeWebView\\Application\\130.0.2849.56\\msedgewebview2.exe",
                        cmd: "msedgewebview2.exe --embedded-browser-webview=1 --webview-exe-name=Shop.exe", memMB: 176, cpu: 2.6, user: me, start: -2_390),
            ProcessSeed(pid: 8920, ppid: 5520, name: "ms-teams.exe", path: "C:\\Program Files\\WindowsApps\\MSTeams_24295.605.3225.8804_x64__8wekyb3d8bbwe\\ms-teams.exe",
                        memMB: 302, cpu: 0.8, user: me, start: 90),
            ProcessSeed(pid: 9040, ppid: 5520, name: "Slack.exe", path: "C:\\Users\\\(user)\\AppData\\Local\\slack\\app-4.41.97\\slack.exe", memMB: 254, cpu: 0.4, user: me, start: 95),
            ProcessSeed(pid: 9100, ppid: 960, name: "sqlservr.exe", path: "C:\\Program Files\\Microsoft SQL Server\\MSSQL16.MSSQLSERVER\\MSSQL\\Binn\\sqlservr.exe",
                        cmd: "\"C:\\Program Files\\Microsoft SQL Server\\MSSQL16.MSSQLSERVER\\MSSQL\\Binn\\sqlservr.exe\" -sMSSQLSERVER",
                        memMB: 1_640, cpu: 0.7, user: "NT Service\\MSSQLSERVER", start: 25),
            ProcessSeed(pid: 9160, ppid: 1104, name: "vmmemWSL", memMB: 4_312, cpu: 1.8, user: me, start: -21_600),
            ProcessSeed(pid: deskhandShellPID, ppid: agentPID, name: "powershell.exe", path: powershell,
                        cmd: "powershell.exe -NoLogo -NoExit -Command \"Set-Location C:\\Shop\"", memMB: 84, cpu: 0.1, user: me, start: -1_500, deskhand: true),
            ProcessSeed(pid: 9244, ppid: deskhandShellPID, name: "conhost.exe", path: exe("conhost.exe"), cmd: "\\??\\C:\\Windows\\system32\\conhost.exe 0x4",
                        memMB: 11, cpu: 0, user: me, start: -1_499, deskhand: true),
            ProcessSeed(pid: 9272, ppid: deskhandShellPID, name: "node.exe", path: node, cmd: "\"\(node)\" C:\\Shop\\web\\node_modules\\vite\\bin\\vite.js --port 5173",
                        memMB: 318, cpu: 5.4, user: me, start: -1_380, deskhand: true),
            ProcessSeed(pid: 9300, ppid: 9272, name: "esbuild.exe", path: "C:\\Shop\\web\\node_modules\\@esbuild\\win32-x64\\esbuild.exe",
                        cmd: "esbuild.exe --service=0.21.5 --ping", memMB: 46, cpu: 0.6, user: me, start: -1_378, deskhand: true),
            ProcessSeed(pid: 9420, ppid: agentPID, name: "cmd.exe", path: exe("cmd.exe"), cmd: "cmd.exe /c dotnet test C:\\Shop\\Shop.sln --no-build",
                        memMB: 5, cpu: 0, user: me, start: -120, deskhand: true),
            ProcessSeed(pid: 9448, ppid: 9420, name: "dotnet.exe", path: dotnet, cmd: "\"\(dotnet)\" test C:\\Shop\\Shop.sln --no-build",
                        memMB: 402, cpu: 14.8, user: me, start: -118, deskhand: true),
            ProcessSeed(pid: 9476, ppid: 9448, name: "testhost.exe", path: "C:\\Shop\\tests\\Shop.Tests\\bin\\Debug\\net9.0\\testhost.exe",
                        cmd: "testhost.exe --port 51734 --endpoint 127.0.0.1:051734 --role client --parentprocessid 9448", memMB: 233, cpu: 9.3, user: me,
                        start: -100, deskhand: true),
            ProcessSeed(pid: 9580, ppid: 5520, name: "Notepad.exe", path: "C:\\Program Files\\WindowsApps\\Microsoft.WindowsNotepad_11.2409.9.0_x64__8wekyb3d8bbwe\\Notepad\\Notepad.exe",
                        cmd: "\"Notepad.exe\" C:\\Users\\\(user)\\Desktop\\작업 메모.txt", memMB: 62, cpu: 0, user: me, start: -6_000),
            ProcessSeed(pid: 9612, ppid: 1104, name: "PhoneExperienceHost.exe",
                        path: "C:\\Program Files\\WindowsApps\\Microsoft.YourPhone_1.24092.211.0_x64__8wekyb3d8bbwe\\PhoneExperienceHost.exe",
                        memMB: 58, cpu: 0, user: me, start: 120),
            ProcessSeed(pid: 9688, ppid: 2484, name: "NVIDIA Overlay.exe", path: "C:\\Program Files\\NVIDIA Corporation\\NVIDIA app\\CEF\\NVIDIA Overlay.exe",
                        memMB: 41, cpu: 0, user: me, start: 100),
            ProcessSeed(pid: 9744, ppid: 960, name: "RtkAudUService64.exe", path: exe("RtkAudUService64.exe"), memMB: 6, cpu: 0, user: system, start: 14),
            ProcessSeed(pid: 9800, ppid: 960, name: "OfficeClickToRun.exe", path: "C:\\Program Files\\Common Files\\microsoft shared\\ClickToRun\\OfficeClickToRun.exe",
                        memMB: 33, cpu: 0, user: system, start: 16),
            ProcessSeed(pid: 9856, ppid: 960, name: "com.docker.service", path: "C:\\Program Files\\Docker\\Docker\\com.docker.service", memMB: 28, cpu: 0.1, user: system, start: 18),
            ProcessSeed(pid: 9912, ppid: 1392, name: "audiodg.exe", path: exe("audiodg.exe"), memMB: 12, cpu: 0.2, user: local, start: -3_000),
        ]
        return seeds
    }

    /// `proc.list` of a computer at `now`: the fixture tree with CPU and memory that move a little
    /// every two seconds, without the PIDs in `ended`. Start times are fixed relative to `anchor`.
    public static func processes(hostID: String, user: String, ended: Set<Int> = [], anchor: Date = .now, now: Date = .now) -> [RemoteProcess] {
        let boot = anchor.addingTimeInterval(-uptime(hostID: hostID))
        let tick = Int(now.timeIntervalSince1970 / 2)
        var list: [RemoteProcess] = []
        var busy = 0.0
        for seed in processSeeds(hostID: hostID, user: user) where !ended.contains(seed.pid) {
            let noise = jitter(seed.pid, tick)
            let cpu = seed.pid == 0 ? 0 : (seed.cpu * 0.3 * (0.55 + 0.9 * noise) * 10).rounded() / 10
            busy += cpu
            let memory = seed.memMB * 1024 * (seed.cpu > 1 ? 0.97 + 0.06 * jitter(seed.pid &+ 7, tick / 5) : 1)
            let started = seed.start >= 0 ? boot.addingTimeInterval(seed.start) : anchor.addingTimeInterval(seed.start)
            list.append(RemoteProcess(pid: seed.pid, ppid: seed.ppid, name: seed.name, path: seed.path, cmd: seed.cmd ?? seed.path.map { "\"\($0)\"" },
                                      memKb: max(8, Int(memory)), cpuPct: cpu, user: seed.user,
                                      started: seed.pid == 0 ? nil : Timestamp(date: started), startedByDeskhand: seed.deskhand))
        }
        if let idle = list.firstIndex(where: { $0.pid == 0 }) {
            list[idle].cpuPct = max(0, (1000 - busy * 10).rounded() / 10)
        }
        return list
    }

    /// A stable value in 0...1 for `seed` and `tick`.
    static func jitter(_ seed: Int, _ tick: Int) -> Double {
        var x = UInt64(bitPattern: Int64(seed &* 73_856_093 ^ tick &* 19_349_663))
        x ^= x >> 33
        x &*= 0xff51_afd7_ed55_8ccd
        x ^= x >> 33
        return Double(x % 10_000) / 10_000
    }

    // MARK: System summary

    /// The summary at `now` of a computer that booted `uptime(hostID:)` before `anchor`.
    public static func system(hostID: String, cpuUsage: Double, anchor: Date = .now, now: Date = .now) -> DiagSystem {
        let gib = 1_073_741_824
        let bootDate = anchor.addingTimeInterval(-uptime(hostID: hostID))
        let uptime = now.timeIntervalSince(bootDate)
        let boot = Timestamp(date: bootDate)
        let tick = Int(now.timeIntervalSince1970 / 2)
        switch hostID {
        case devPCID:
            let used = Int(Double(27 * gib) * (0.98 + 0.04 * jitter(11, tick)))
            return DiagSystem(
                os: DiagOS(name: "Windows 11 Pro", version: "24H2", build: "26100.2314", edition: "Professional"),
                computerName: "DEV-PC",
                cpu: DiagCPU(name: "AMD Ryzen 9 7950X 16-Core Processor", cores: 16, threads: 32, usagePct: cpuUsage),
                memory: DiagMemory(totalBytes: 64 * gib, usedBytes: used),
                disks: [
                    DiagDisk(root: "C:\\", label: "Windows", fs: "NTFS", totalBytes: 1_023_303_680_000, freeBytes: 212_400_000_000),
                    DiagDisk(root: "D:\\", label: "개발", fs: "NTFS", totalBytes: 2_000_396_746_752, freeBytes: 1_331_000_000_000),
                    DiagDisk(root: "E:\\", label: "백업", fs: "exFAT", totalBytes: 4_000_787_030_016, freeBytes: 162_000_000_000),
                ],
                gpus: [DiagGPU(name: "NVIDIA GeForce RTX 2070", driverVersion: "32.0.15.6636", memoryBytes: 8 * gib)],
                bootTime: boot, uptimeS: Int(uptime))
        case qaLaptopID:
            return DiagSystem(
                os: DiagOS(name: "Windows 11 Home", version: "24H2", build: "26100.2161", edition: "Core"),
                computerName: "QA-LAPTOP",
                cpu: DiagCPU(name: "Snapdragon(R) X Elite - X1E78100", cores: 12, threads: 12, usagePct: cpuUsage),
                memory: DiagMemory(totalBytes: 16 * gib, usedBytes: Int(Double(11 * gib) * (0.98 + 0.04 * jitter(12, tick)))),
                disks: [DiagDisk(root: "C:\\", label: "OS", fs: "NTFS", totalBytes: 511_000_000_000, freeBytes: 21_400_000_000)],
                gpus: [DiagGPU(name: "Qualcomm(R) Adreno(TM) X1-85 GPU", driverVersion: "31.0.96.0")],
                bootTime: boot, uptimeS: Int(uptime))
        case stagingID:
            return DiagSystem(
                os: DiagOS(name: "Windows Server 2022 Standard", version: "21H2", build: "20348.2762", edition: "ServerStandard"),
                computerName: "STAGING-01",
                cpu: DiagCPU(name: "Intel(R) Xeon(R) Gold 6338 CPU @ 2.00GHz", cores: 8, threads: 16, usagePct: cpuUsage),
                memory: DiagMemory(totalBytes: 32 * gib, usedBytes: 19 * gib),
                disks: [DiagDisk(root: "C:\\", fs: "NTFS", totalBytes: 255_000_000_000, freeBytes: 88_000_000_000)],
                gpus: [DiagGPU(name: "Microsoft Remote Display Adapter")],
                bootTime: boot, uptimeS: Int(uptime))
        default:
            return DiagSystem(
                os: DiagOS(name: "Windows 11 Pro", version: "23H2", build: "22631.4317", edition: "Professional"),
                computerName: hostID.uppercased(),
                cpu: DiagCPU(name: "Intel(R) Core(TM) i5-1240P", cores: 12, threads: 16, usagePct: cpuUsage),
                memory: DiagMemory(totalBytes: 16 * gib, usedBytes: 7 * gib),
                disks: [DiagDisk(root: "C:\\", label: "Windows", fs: "NTFS", totalBytes: 511_000_000_000, freeBytes: 240_000_000_000)],
                gpus: [DiagGPU(name: "Intel(R) Iris(R) Xe Graphics", driverVersion: "31.0.101.5592", memoryBytes: 128 * 1_048_576)],
                bootTime: boot, uptimeS: Int(uptime))
        }
    }

    // MARK: Events

    /// One kind of event the fixture log repeats.
    private struct EventTemplate {
        var level: EventLevel
        var provider: String
        var eventID: Int
        var messages: [String]
        /// Roughly how many times it appears over seven days.
        var count: Int
    }

    private static let systemTemplates: [EventTemplate] = [
        EventTemplate(level: .information, provider: "Service Control Manager", eventID: 7036, messages: [
            "Windows Update 서비스가 실행 중 상태로 전환되었습니다.",
            "Windows Update 서비스가 중지됨 상태로 전환되었습니다.",
            "Background Intelligent Transfer Service 서비스가 실행 중 상태로 전환되었습니다.",
            "WinHTTP 웹 프록시 자동 검색 서비스 서비스가 중지됨 상태로 전환되었습니다.",
            "Microsoft Store 설치 서비스 서비스가 실행 중 상태로 전환되었습니다.",
            "Deskhand Agent 서비스가 실행 중 상태로 전환되었습니다.",
        ], count: 150),
        EventTemplate(level: .information, provider: "Service Control Manager", eventID: 7040, messages: [
            "Background Intelligent Transfer Service 서비스의 시작 유형을 자동 시작에서 요청 시 시작(으)로 변경했습니다.",
        ], count: 8),
        EventTemplate(level: .error, provider: "Service Control Manager", eventID: 7031, messages: [
            "Shop Sync Service 서비스가 예기치 않게 종료되었습니다. 이 오류가 1번 발생했습니다. 다음 수정 작업이 60000밀리초 안에 수행됩니다: 서비스 다시 시작.",
        ], count: 4),
        EventTemplate(level: .error, provider: "Service Control Manager", eventID: 7000, messages: [
            "Shop Sync Service 서비스가 다음 오류 때문에 시작되지 못했습니다.\n서비스가 시작 또는 제어 요청에 적시에 응답하지 않았습니다.",
        ], count: 2),
        EventTemplate(level: .warning, provider: "Microsoft-Windows-DistributedCOM", eventID: 10016, messages: [
            "응용 프로그램별 권한 설정에서 CLSID가 {2593F8B9-4EAF-457C-B68A-50F6B8EA6B54}이고 APPID가 {15C20B67-12E7-4BB6-92BB-7AFF07997402}인 COM 서버 응용 프로그램에 대한 로컬 활성화 권한을 응용 프로그램 컨테이너 사용할 수 없음 SID(사용할 수 없음)에서 실행 중인 주소 LocalHost(LRPC 사용)의 사용자 DEV-PC\\taeyoung SID(S-1-5-21-3623811015-3361044348-30300820-1013)에게 부여하지 않았습니다. 구성 요소 서비스 관리 도구를 사용하여 이 보안 권한을 수정할 수 있습니다.",
        ], count: 14),
        EventTemplate(level: .information, provider: "Microsoft-Windows-WindowsUpdateClient", eventID: 19, messages: [
            "설치 성공: Windows에서 다음 업데이트를 설치했습니다: 2026-10 x64 기반 시스템용 Windows 11 Version 24H2 누적 업데이트(KB5044284)",
            "설치 성공: Windows에서 다음 업데이트를 설치했습니다: Microsoft Defender 바이러스 백신에 대한 보안 인텔리전스 업데이트 - KB2267602(버전 1.419.341.0)",
        ], count: 6),
        EventTemplate(level: .error, provider: "Microsoft-Windows-WindowsUpdateClient", eventID: 20, messages: [
            "설치 실패: Windows에서 0x800F0922 오류로 인해 다음 업데이트를 설치하지 못했습니다: 2026-10 x64 기반 시스템용 .NET Framework 3.5 및 4.8.1 누적 업데이트(KB5044033)",
        ], count: 2),
        EventTemplate(level: .warning, provider: "Microsoft-Windows-Time-Service", eventID: 134, messages: [
            "NtpClient에서 수동 피어 'time.windows.com,0x9'을(를) 시간 원본으로 사용하도록 설정하는 중에 DNS 확인 오류가 발생했습니다. 15분 후에 다시 시도하며 그 후 다시 시도할 때마다 간격을 두 배로 늘립니다. 오류: 호스트를 알 수 없습니다. (0x80072AF9)",
        ], count: 5),
        EventTemplate(level: .information, provider: "Microsoft-Windows-Kernel-General", eventID: 16, messages: [
            "액세스 기록 하이브가 존재하지 않는 레지스트리 키를 지우려고 했습니다.",
        ], count: 10),
        EventTemplate(level: .warning, provider: "Microsoft-Windows-Resource-Exhaustion-Detector", eventID: 2004, messages: [
            "Windows에서 가상 메모리 부족 상태를 진단했습니다. 다음 프로그램이 가상 메모리를 가장 많이 사용했습니다: node.exe (7120)이(가) 6,412,881,920바이트, Code.exe (7064)이(가) 2,103,447,552바이트, vmmemWSL (9160)이(가) 1,904,312,320바이트를 사용했습니다.",
        ], count: 2),
        EventTemplate(level: .warning, provider: "disk", eventID: 153, messages: [
            "디스크 2의 논리 블록 주소 0x1a4c2f80에서 IO 작업이 다시 시도되었습니다.",
        ], count: 4),
        EventTemplate(level: .warning, provider: "Display", eventID: 4101, messages: [
            "디스플레이 드라이버 nvlddmkm이(가) 응답을 멈추었으나 복구되었습니다.",
        ], count: 2),
        EventTemplate(level: .critical, provider: "Microsoft-Windows-Kernel-Power", eventID: 41, messages: [
            "시스템이 정상적으로 종료되기 전에 다시 부팅되었습니다. 이 오류는 시스템이 응답을 멈추었거나, 충돌했거나, 예기치 않게 전원이 끊어졌을 때 발생할 수 있습니다.",
        ], count: 1),
        EventTemplate(level: .error, provider: "Microsoft-Windows-WER-SystemErrorReporting", eventID: 1001, messages: [
            "다시 부팅하기 전에 컴퓨터에 버그 검사가 발생했습니다. 버그 검사: 0x0000009f (0x0000000000000003, 0xffffb50d2c6c4060, 0xfffff8017b867850, 0xffffb50d33a5a8a0). 덤프 파일 저장 위치: C:\\Windows\\MEMORY.DMP. 보고서 ID: 6f1c8f2e-5a4b-4f7e-9a39-2b7c9d1e4a10.",
        ], count: 1),
        EventTemplate(level: .information, provider: "EventLog", eventID: 6013, messages: [
            "시스템 가동 시간이 187212초입니다.",
        ], count: 7),
        EventTemplate(level: .information, provider: "Microsoft-Windows-Dhcp-Client", eventID: 50036, messages: [
            "DHCPv4 클라이언트 서비스가 시작되었습니다.",
        ], count: 3),
        EventTemplate(level: .warning, provider: "Tcpip", eventID: 4199, messages: [
            "시스템에서 네트워크 하드웨어 주소가 00-1A-2B-3C-4D-5E인 다른 시스템과의 IP 주소 192.168.0.24 충돌을 감지했습니다. 이 시스템의 네트워크 작업이 중단될 수 있습니다.",
        ], count: 1),
        EventTemplate(level: .verbose, provider: "Microsoft-Windows-Kernel-Processor-Power", eventID: 55, messages: [
            "그룹 0의 프로세서 15에서 전원 관리 기능을 노출합니다. 프로세서 유형: AMD Ryzen 9 7950X 16-Core Processor, 최대 성능 상태: 4501MHz, 최소 성능 상태: 545MHz.",
        ], count: 3),
    ]

    private static let applicationTemplates: [EventTemplate] = [
        EventTemplate(level: .error, provider: "Application Error", eventID: 1000, messages: [
            "오류 있는 응용 프로그램 이름: Shop.exe, 버전: 1.18.0.1188, 타임스탬프: 0x6712a3f0\n오류 있는 모듈 이름: KERNELBASE.dll, 버전: 10.0.26100.2314, 타임스탬프: 0x4f1a7a21\n예외 코드: 0xe0434352\n오류 오프셋: 0x000000000005f39c\n오류 있는 프로세스 ID: 0x2260\n오류 있는 응용 프로그램 시작 시간: 0x1db22f6c4b1e9a2\n오류 있는 응용 프로그램 경로: C:\\Shop\\bin\\Shop.exe\n오류 있는 모듈 경로: C:\\Windows\\System32\\KERNELBASE.dll\n보고서 ID: 0c9e5c1d-77f2-4a7b-9c63-4f0d1e2a8b55\n오류 있는 패키지 전체 이름: \n오류 있는 패키지에 상대적인 응용 프로그램 ID: ",
        ], count: 4),
        EventTemplate(level: .error, provider: ".NET Runtime", eventID: 1026, messages: [
            "응용 프로그램: Shop.exe\nCoreCLR 버전: 9.0.24.52809\n.NET 버전: 9.0.0\n설명: 처리되지 않은 예외로 인해 프로세스가 종료되었습니다.\n예외 정보: System.InvalidOperationException: 결제 승인 응답을 받기 전에 주문 확인 화면이 닫혔습니다.\n   위치: Shop.Checkout.OrderConfirmViewModel.Submit() 파일 C:\\Shop\\src\\Shop.Checkout\\OrderConfirmViewModel.cs: 줄 214\n   위치: Shop.Checkout.OrderConfirmViewModel.<OnApprovedAsync>b__42_0()\n   위치: System.Threading.Tasks.Task.<>c.<ThrowAsync>b__128_0(Object state)\n   위치: System.Windows.Threading.ExceptionWrapper.InternalRealCall(Delegate callback, Object args, Int32 numArgs)\n   위치: System.Windows.Threading.ExceptionWrapper.TryCatchWhen(Object source, Delegate callback, Object args, Int32 numArgs, Delegate catchHandler)\n   위치: System.Windows.Threading.DispatcherOperation.InvokeImpl()\n   위치: MS.Internal.CulturePreservingExecutionContext.CallbackWrapper(Object obj)\n   위치: System.Threading.ExecutionContext.RunInternal(ExecutionContext executionContext, ContextCallback callback, Object state)\n   위치: System.Windows.Threading.DispatcherOperation.Invoke()\n   위치: System.Windows.Threading.Dispatcher.ProcessQueue()\n   위치: System.Windows.Threading.Dispatcher.WndProcHook(IntPtr hwnd, Int32 msg, IntPtr wParam, IntPtr lParam, Boolean& handled)\n   위치: MS.Win32.HwndWrapper.WndProc(IntPtr hwnd, Int32 msg, IntPtr wParam, IntPtr lParam, Boolean& handled)\n   위치: MS.Win32.HwndSubclass.DispatcherCallbackOperation(Object o)\n   위치: System.Windows.Threading.ExceptionWrapper.InternalRealCall(Delegate callback, Object args, Int32 numArgs)\n   위치: System.Windows.Application.RunDispatcher(Object ignore)\n   위치: System.Windows.Application.Run()\n   위치: Shop.App.Main()",
        ], count: 3),
        EventTemplate(level: .information, provider: "Windows Error Reporting", eventID: 1001, messages: [
            "오류 버킷 1859482734906672113, 유형 4\n이벤트 이름: APPCRASH\n응답: 사용할 수 없음\nCab ID: 0\n\n문제 서명:\nP1: Shop.exe\nP2: 1.18.0.1188\nP3: 6712a3f0\nP4: KERNELBASE.dll\nP5: 10.0.26100.2314\nP6: 4f1a7a21\nP7: e0434352\nP8: 000000000005f39c\n\n다음 파일에 이 오류에 대한 추가 정보가 있을 수 있습니다:\nC:\\ProgramData\\Shop\\CrashDumps\\Shop.exe.4013.dmp\nC:\\ProgramData\\Microsoft\\Windows\\WER\\ReportArchive\\AppCrash_Shop.exe_3f2a9c1d\\Report.wer",
        ], count: 4),
        EventTemplate(level: .error, provider: "Application Hang", eventID: 1002, messages: [
            "프로그램 Code.exe 버전 1.95.1이(가) Windows와의 상호 작용을 중지하여 닫혔습니다. 문제에 대한 추가 정보가 있는지 확인하려면 관리 센터 제어판에서 문제 기록을 확인하십시오.\n프로세스 ID: 1b88\n시작 시간: 01db22f1a6c04e3b\n종료 시간: 4294967295\n응용 프로그램 경로: C:\\Users\\taeyoung\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe\n보고서 ID: 5d2f8e7a-1c3b-4b8e-a0f4-7e9c2d6b1a33",
        ], count: 1),
        EventTemplate(level: .warning, provider: "Shop", eventID: 3102, messages: [
            "결제 승인 응답이 30초를 넘었습니다. 주문 2026-1020-0342, 카드사 KB, 응답 대기 31.8초.",
            "결제 승인 응답이 30초를 넘었습니다. 주문 2026-1020-0361, 카드사 신한, 응답 대기 34.2초.",
        ], count: 9),
        EventTemplate(level: .information, provider: "Shop", eventID: 1000, messages: [
            "Shop 1.18.0.1188을(를) 시작했습니다. 환경: staging, 설정 파일: C:\\Shop\\bin\\appsettings.json",
        ], count: 8),
        EventTemplate(level: .information, provider: "Deskhand Agent", eventID: 1, messages: [
            "에이전트 0.1.0을(를) 시작했습니다. UDP 47600에서 연결을 기다립니다.",
            "이 Mac(taeyoung-mbp)과 연결했습니다. 세대 4.",
        ], count: 6),
        EventTemplate(level: .information, provider: "MsiInstaller", eventID: 11707, messages: [
            "제품: Deskhand Agent -- 설치를 완료했습니다.",
            "제품: Microsoft .NET Runtime - 9.0.0 (x64) -- 설치를 완료했습니다.",
        ], count: 3),
        EventTemplate(level: .information, provider: "SecurityCenter", eventID: 15, messages: [
            "Windows Defender 상태를 SECURITY_PRODUCT_STATE_ON(으)로 업데이트했습니다.",
        ], count: 6),
        EventTemplate(level: .information, provider: "VSS", eventID: 8224, messages: [
            "VSS 서비스를 유휴 시간 제한 때문에 종료하는 중입니다.",
        ], count: 10),
        EventTemplate(level: .warning, provider: "MSSQLSERVER", eventID: 833, messages: [
            "SQL Server에서 데이터베이스 ID 7의 파일 [D:\\SQLData\\Shop.mdf]에 대한 I/O 요청이 15초 이상 걸린 경우가 1번 발생했습니다. OS 파일 핸들은 0x0000000000000C3C입니다.",
        ], count: 2),
        EventTemplate(level: .information, provider: "MSSQLSERVER", eventID: 17137, messages: [
            "데이터베이스 'Shop'을(를) 시작하는 중입니다.",
        ], count: 5),
        EventTemplate(level: .warning, provider: "Microsoft-Windows-User Profiles Service", eventID: 1530, messages: [
            "Windows에서 레지스트리 파일이 다른 응용 프로그램이나 서비스에서 아직 사용되고 있음을 감지했습니다. 이 파일은 이제 언로드됩니다. 레지스트리 파일을 사용 중인 응용 프로그램이나 서비스가 이후에 제대로 작동하지 않을 수 있습니다.",
        ], count: 3),
        EventTemplate(level: .information, provider: "Microsoft-Windows-Security-SPP", eventID: 16384, messages: [
            "소프트웨어 보호 서비스 다시 시작 예약 성공: 2026-10-21T04:12:09Z. 이유: RulesEngine.",
        ], count: 8),
        EventTemplate(level: .information, provider: "Microsoft-Windows-RestartManager", eventID: 10000, messages: [
            "세션 0을(를) 2026-10-20 오전 9:41:02에 시작했습니다.",
        ], count: 6),
    ]

    /// The fixture events of `log`, newest first, spread over the last seven days with more of them
    /// in the last day.
    public static func events(hostID: String, log: EventLogName, now: Date = .now) -> [EventLogEntry] {
        let templates = log == .system ? systemTemplates : applicationTemplates
        let scale = hostID == devPCID ? 1.0 : 0.35
        var stamped: [(Date, EventTemplate, String)] = []
        for (t, template) in templates.enumerated() {
            let count = max(1, Int((Double(template.count) * scale).rounded()))
            for n in 0..<count {
                let r = jitter(t * 131 + n * 17 + (log == .system ? 0 : 7_777), 3)
                let recent = jitter(t * 29 + n, 5) < 0.42
                let span: Double = recent ? 86_400 : 7 * 86_400
                var age = r * span
                if template.level == .critical || template.provider.hasPrefix("Microsoft-Windows-WER") { age = 28 * 3_600 + Double(t) * 60 }
                if template.provider == "Application Error" || template.provider == ".NET Runtime" || template.provider == "Windows Error Reporting" {
                    age = [2_900.0, 70_000, 190_000, 400_000][n % 4] + Double(t) * 4
                }
                let message = template.messages[n % template.messages.count]
                stamped.append((now.addingTimeInterval(-age - 5), template, message))
            }
        }
        stamped.sort { $0.0 > $1.0 }
        let firstRecord = log == .system ? 88_000 : 41_000
        return stamped.enumerated().map { index, item in
            EventLogEntry(recordID: firstRecord + stamped.count - index, time: Timestamp(date: item.0), level: item.1.level.rawValue,
                          provider: item.1.provider, eventID: item.1.eventID, message: item.2)
        }
    }

    // MARK: Crash dumps

    public static func crashDumps(hostID: String, user: String, now: Date = .now) -> [CrashDump] {
        func ago(_ hours: Double) -> Timestamp { Timestamp(date: now.addingTimeInterval(-hours * 3_600)) }
        switch hostID {
        case devPCID:
            return [
                CrashDump(path: "C:\\ProgramData\\Shop\\CrashDumps\\Shop.exe.4013.dmp", app: "Shop.exe", time: ago(0.81),
                          sizeBytes: 51_100_000, kind: CrashDumpKind.minidump.rawValue),
                CrashDump(path: "C:\\Users\\\(user)\\AppData\\Local\\CrashDumps\\dotnet.exe.7712.dmp", app: "dotnet.exe", time: ago(19.4),
                          sizeBytes: 1_482_418_176, kind: CrashDumpKind.full.rawValue),
                CrashDump(path: crashReportFolder + "\\Report.wer", app: "Code.exe", time: ago(52),
                          sizeBytes: 41_872, kind: CrashDumpKind.report.rawValue),
            ]
        case qaLaptopID:
            return [
                CrashDump(path: "C:\\ProgramData\\Shop\\CrashDumps\\Shop.exe.4026.dmp", app: "Shop.exe", time: ago(3.2),
                          sizeBytes: 54_200_000, kind: CrashDumpKind.minidump.rawValue),
            ]
        default:
            return []
        }
    }

    /// Folder of the Windows Error Reporting report in the crash dump fixtures.
    static let crashReportFolder = "C:\\ProgramData\\Microsoft\\Windows\\WER\\ReportArchive\\AppCrash_Code.exe_5d2f8e7a1c3b4b8e"
}
