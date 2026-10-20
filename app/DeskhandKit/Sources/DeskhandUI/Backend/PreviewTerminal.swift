import DeskhandClient
import Foundation
import Synchronization
import struct DeskhandClient.Host

/// Simulated shells of `PreviewBackend`: Windows PowerShell, PowerShell 7 and Command Prompt
/// with a one-line editor (typing, backspace, ↑↓ history, ⌃C, ⌃L; the prompt line is drawn again
/// after a resize, as the console host does) and canned output for `dir`,
/// `cd`, `Get-Process`, `whoami`, `hostname`, `ipconfig`, `Get-Date`, `$PSVersionTable`, `cls`,
/// `echo`, `help` and `exit [code]`. Folders come from the files tab fixtures. Each shell keeps
/// its output, at most 1 MiB, and replays it after the attached frame of every attach.
final class PreviewTerminals: Sendable {
    private struct State {
        var processes: [PreviewShellProcess] = []
        var next = 1
    }

    private let state = Mutex(State())
    /// Called with the job of a shell whose status changed.
    private let onJobChange: @Sendable (Job) -> Void

    init(hosts: [Host], includesFixtures: Bool, onJobChange: @escaping @Sendable (Job) -> Void) {
        self.onJobChange = onJobChange
        guard includesFixtures, let dev = hosts.first(where: { $0.hostID == PreviewFixtures.devPCID }) else { return }
        let process = PreviewShellProcess(
            jobID: "j-preview-pty-dev", ptyID: "p-preview-dev", host: dev, shell: .powershell, pid: 9216,
            size: TerminalSize(cols: 120, rows: 32), createdAt: Date.now.addingTimeInterval(-25 * 60),
            cwd: "C:\\Shop", summary: "PowerShell · C:\\Shop · npm run dev", openedBy: "Claude Code",
            onJobChange: onJobChange)
        process.startDevServer()
        state.withLock { $0.processes.append(process) }
    }

    func open(host: Host, shell: TerminalShell, size: TerminalSize) -> PreviewShellProcess {
        let process = state.withLock { s -> PreviewShellProcess in
            let n = s.next
            s.next += 1
            let process = PreviewShellProcess(
                jobID: "j-preview-pty-\(n)", ptyID: "p-preview-\(n)", host: host, shell: shell, pid: 4100 + n * 284,
                size: size, createdAt: .now, cwd: PreviewFixtures.remoteHome(user: host.session?.user ?? "user"),
                summary: nil, openedBy: "Deskhand", onJobChange: onJobChange)
            s.processes.append(process)
            return process
        }
        process.writeBanner()
        onJobChange(process.job)
        return process
    }

    func process(for target: PTYTarget) -> PreviewShellProcess? {
        state.withLock { s in
            s.processes.first { process in
                switch target {
                case .pty(let id): process.ptyID == id
                case .job(let id): process.jobID == id
                }
            }
        }
    }

    /// Running shells of `hostID`, newest first.
    func runningJobs(hostID: String) -> [Job] {
        let processes = state.withLock { $0.processes.filter { $0.hostID == hostID } }
        return processes.map(\.job).filter { $0.status == EnvelopeStatus.running.rawValue }.reversed()
    }

    /// Ends the attached streams of `hostID`, as a dropped connection to the computer does.
    func dropStreams(hostID: String) {
        let processes = state.withLock { $0.processes.filter { $0.hostID == hostID } }
        for process in processes { process.dropStream() }
    }
}

/// One simulated shell. Output goes to the attached stream as data frames and into the
/// scrollback; input arrives through the stream's data frames.
final class PreviewShellProcess: Sendable {
    let jobID: String
    let ptyID: String
    let hostID: String
    let shell: TerminalShell
    let pid: Int
    private let createdAt: Date
    private let computer: String
    private let user: String
    private let address: String
    private let summary: String?
    private let openedBy: String
    private let onJobChange: @Sendable (Job) -> Void

    private enum Status: Equatable {
        case running
        case exited(Int)
        case killed
    }

    private struct State {
        var output: [UInt8] = []
        var line = ""
        var cwd: String
        var history: [String] = []
        var historyIndex: Int?
        /// An escape sequence being read, after its ESC.
        var escape: String?
        var status = Status.running
        var size: TerminalSize
        /// A command that keeps running until ⌃C, such as the fixture's dev server.
        var busy = false
        var stream: AsyncThrowingStream<StreamConnection.Frame, Error>.Continuation?
        var streamToken = 0
        var updatedAt: Date
    }

    private let state: Mutex<State>
    static let scrollbackLimit = 1 << 20

    init(jobID: String, ptyID: String, host: Host, shell: TerminalShell, pid: Int, size: TerminalSize, createdAt: Date,
         cwd: String, summary: String?, openedBy: String, onJobChange: @escaping @Sendable (Job) -> Void) {
        self.jobID = jobID
        self.ptyID = ptyID
        hostID = host.hostID
        self.shell = shell
        self.pid = pid
        self.createdAt = createdAt
        computer = Self.computerName(host)
        user = host.session?.user ?? "user"
        address = host.address
        self.summary = summary
        self.openedBy = openedBy
        self.onJobChange = onJobChange
        state = Mutex(State(cwd: cwd, size: size, updatedAt: createdAt))
    }

    static func computerName(_ host: Host) -> String {
        switch host.hostID {
        case PreviewFixtures.devPCID: "DEV-PC"
        case PreviewFixtures.qaLaptopID: "QA-LAPTOP"
        case PreviewFixtures.meetingPCID: "MEETING-PC"
        case PreviewFixtures.stagingID: "STAGING-01"
        default: host.hostID.uppercased()
        }
    }

    var job: Job {
        let (status, cwd, updatedAt) = state.withLock { ($0.status, $0.cwd, $0.updatedAt) }
        let statusText: EnvelopeStatus
        var finishedAt: Timestamp?
        switch status {
        case .running: statusText = .running
        case .exited(let code):
            statusText = code == 0 ? .succeeded : .failed
            finishedAt = Timestamp(date: updatedAt)
        case .killed:
            statusText = .cancelled
            finishedAt = Timestamp(date: updatedAt)
        }
        return Job(jobID: jobID, hostID: hostID, kind: "pty", status: statusText.rawValue,
                   summary: summary ?? "\(shell.shortTitle) · \(cwd)", createdAt: Timestamp(date: createdAt),
                   updatedAt: Timestamp(date: updatedAt), finishedAt: finishedAt,
                   result: .object(["shell": .string(shell.rawValue), "pid": .int(pid), "client_name": .string(openedBy)]))
    }

    /// Everything the shell printed, as text.
    var transcript: String {
        state.withLock { String(decoding: $0.output, as: UTF8.self) }
    }

    // MARK: Streams

    /// A new stream: the attached frame, then the scrollback, then live output. A stream
    /// attached earlier ends.
    func attach() -> PreviewTerminalStream {
        let (frames, continuation) = AsyncThrowingStream.makeStream(of: StreamConnection.Frame.self)
        let token = state.withLock { s -> Int in
            s.stream?.finish()
            s.streamToken += 1
            continuation.yield((kind: TerminalFrameKind.attached,
                                payload: TerminalFrames.encode(TerminalFrames.Attached(job: jobID, pid: pid))))
            var offset = 0
            while offset < s.output.count {
                let end = min(offset + 16_384, s.output.count)
                continuation.yield((kind: TerminalFrameKind.data, payload: Data(s.output[offset..<end])))
                offset = end
            }
            if case .running = s.status {
                s.stream = continuation
            } else {
                continuation.finish()
            }
            return s.streamToken
        }
        return PreviewTerminalStream(frames: frames, continuation: continuation, process: self, token: token)
    }

    func detach(token: Int) {
        state.withLock { s in
            guard s.streamToken == token else { return }
            s.stream = nil
        }
    }

    func dropStream() {
        state.withLock { s in
            s.stream?.finish()
            s.stream = nil
        }
    }

    func kill() {
        let changed = state.withLock { s -> Bool in
            guard s.status == .running else { return false }
            s.status = .killed
            s.busy = false
            s.updatedAt = .now
            s.stream?.yield((kind: TerminalFrameKind.exit, payload: TerminalFrames.encode(TerminalFrames.Exit(code: 1))))
            s.stream?.finish()
            s.stream = nil
            return true
        }
        if changed { onJobChange(job) }
    }

    // MARK: Output

    private func write(_ text: String, in s: inout State) {
        let bytes = Array(text.utf8)
        s.output.append(contentsOf: bytes)
        if s.output.count > Self.scrollbackLimit {
            var cut = s.output.count - Self.scrollbackLimit
            if let newline = s.output[cut...].firstIndex(of: 0x0A) { cut = newline + 1 }
            s.output.removeFirst(cut)
        }
        s.stream?.yield((kind: TerminalFrameKind.data, payload: Data(bytes)))
    }

    private func write(_ text: String) {
        state.withLock { write(text, in: &$0) }
    }

    private var isPowerShell: Bool { shell != .cmd }

    private func prompt(_ s: State) -> String {
        if isPowerShell {
            let uri = "file://\(computer)/" + s.cwd.replacingOccurrences(of: "\\", with: "/")
            return "\u{1b}]7;\(uri)\u{7}PS \(s.cwd)> "
        }
        return "\(s.cwd)>"
    }

    /// The console title Windows gives the shell: its program path.
    private var programTitle: String {
        switch shell {
        case .powershell: "C:\\WINDOWS\\System32\\WindowsPowerShell\\v1.0\\powershell.exe"
        case .pwsh: "C:\\Program Files\\PowerShell\\7\\pwsh.exe"
        case .cmd: "C:\\WINDOWS\\system32\\cmd.exe"
        }
    }

    func writeBanner() {
        let banner: String
        switch shell {
        case .powershell:
            banner = "Windows PowerShell\r\nCopyright (C) Microsoft Corporation. All rights reserved.\r\n\r\n"
                + "새로운 기능과 개선 사항이 포함된 최신 PowerShell을 설치하세요! https://aka.ms/PSWindows\r\n\r\n"
        case .pwsh:
            banner = "PowerShell 7.4.6\r\n"
        case .cmd:
            banner = "Microsoft Windows [Version 10.0.26100.2314]\r\n(c) Microsoft Corporation. All rights reserved.\r\n\r\n"
        }
        state.withLock { s in
            write("\u{1b}]0;\(programTitle)\u{7}" + banner + prompt(s), in: &s)
        }
    }

    /// Puts the fixture's dev server output in the scrollback and keeps it running.
    func startDevServer() {
        let esc = "\u{1b}["
        let lines = [
            "\u{1b}]0;npm run dev\u{7}PS C:\\Shop> npm run dev",
            "",
            "> shop@1.18.8 dev",
            "> vite --host",
            "",
            "",
            "  \(esc)32;1mVITE\(esc)0m \(esc)32mv6.3.5\(esc)0m  \(esc)2mready in \(esc)0m\(esc)1m812\(esc)0m\(esc)2m ms\(esc)0m",
            "",
            "  \(esc)32m➜\(esc)0m  \(esc)1mLocal\(esc)0m:   \(esc)36mhttp://localhost:\(esc)1m5173\(esc)0m\(esc)36m/\(esc)0m",
            "  \(esc)32m➜\(esc)0m  \(esc)1mNetwork\(esc)0m: \(esc)36mhttp://\(address):\(esc)1m5173\(esc)0m\(esc)36m/\(esc)0m",
            "  \(esc)2m\(esc)32m➜\(esc)0m\(esc)2m  press \(esc)0m\(esc)1mh + enter\(esc)0m\(esc)2m to show help\(esc)0m",
            "\(esc)2m14:02:11\(esc)0m \(esc)36;1m[vite]\(esc)0m \(esc)32mpage reload \(esc)0m\(esc)2msrc/checkout/Payment.tsx\(esc)0m",
            "\(esc)2m14:05:48\(esc)0m \(esc)36;1m[vite]\(esc)0m \(esc)32mhmr update \(esc)0m\(esc)2m/src/checkout/Payment.tsx\(esc)0m",
            "\(esc)2m14:09:03\(esc)0m \(esc)36;1m[vite]\(esc)0m \(esc)33m(!) Failed to load source map for /src/vendor/card-sdk.js\(esc)0m",
        ]
        state.withLock { s in
            s.busy = true
            write(lines.joined(separator: "\r\n") + "\r\n", in: &s)
        }
    }

    // MARK: Input

    func receive(kind: UInt8, payload: Data) {
        switch kind {
        case TerminalFrameKind.resize:
            guard let size = TerminalFrames.decode(TerminalSize.self, from: payload) else { return }
            state.withLock { s in
                guard s.size != size else { return }
                s.size = size
                if s.status == .running, !s.busy { write("\r\u{1b}[K" + prompt(s) + s.line, in: &s) }
            }
        case TerminalFrameKind.data:
            input(payload)
        default:
            break
        }
    }

    private func input(_ payload: Data) {
        var exitCode: Int?
        let text = String(decoding: payload, as: UTF8.self)
        state.withLock { s in
            guard s.status == .running else { return }
            for scalar in text.unicodeScalars {
                if var escape = s.escape {
                    escape.unicodeScalars.append(scalar)
                    let count = escape.unicodeScalars.count
                    if count == 1, scalar != "[", scalar != "O" {
                        s.escape = nil
                    } else if count >= 2, (0x40...0x7E).contains(scalar.value) || count > 16 {
                        s.escape = nil
                        handleEscape(escape, in: &s)
                    } else {
                        s.escape = escape
                    }
                    continue
                }
                switch scalar.value {
                case 0x1B:
                    s.escape = ""
                case 0x03:
                    let title = s.busy ? "\u{1b}]0;\(programTitle)\u{7}" : ""
                    s.busy = false
                    s.line = ""
                    s.historyIndex = nil
                    write("^C\r\n" + title + prompt(s), in: &s)
                case _ where s.busy:
                    continue
                case 0x0D:
                    let line = s.line
                    s.line = ""
                    s.historyIndex = nil
                    if let code = run(line, in: &s) {
                        exitCode = code
                        return
                    }
                case 0x7F, 0x08:
                    guard let last = s.line.last else { continue }
                    s.line.removeLast()
                    let width = Self.width(of: last)
                    write(String(repeating: "\u{8}", count: width) + String(repeating: " ", count: width)
                        + String(repeating: "\u{8}", count: width), in: &s)
                case 0x0C:
                    write("\u{1b}[2J\u{1b}[3J\u{1b}[H" + prompt(s) + s.line, in: &s)
                case 0x00..<0x20:
                    continue
                default:
                    let character = String(Character(scalar))
                    s.line += character
                    write(character, in: &s)
                }
            }
        }
        if let exitCode { exit(exitCode) }
    }

    private static func width(of character: Character) -> Int {
        guard let scalar = character.unicodeScalars.first else { return 1 }
        switch scalar.value {
        case 0x1100...0x115F, 0x2E80...0xA4CF, 0xAC00...0xD7A3, 0xF900...0xFAFF, 0xFE30...0xFE4F, 0xFF00...0xFF60, 0xFFE0...0xFFE6,
             0x1F300...0x1FAFF:
            return 2
        default:
            return 1
        }
    }

    private func handleEscape(_ escape: String, in s: inout State) {
        guard !s.busy, escape == "[A" || escape == "[B" || escape == "OA" || escape == "OB" else { return }
        let history = s.history
        guard !history.isEmpty else { return }
        var index = s.historyIndex
        if escape.hasSuffix("A") {
            index = max(0, (index ?? history.count) - 1)
        } else {
            guard let current = index else { return }
            index = current + 1 < history.count ? current + 1 : nil
        }
        s.historyIndex = index
        let replacement = index.map { history[$0] } ?? ""
        let width = s.line.reduce(0) { $0 + Self.width(of: $1) }
        s.line = replacement
        write(String(repeating: "\u{8}", count: width) + "\u{1b}[K" + replacement, in: &s)
    }

    private func exit(_ code: Int) {
        let changed = state.withLock { s -> Bool in
            guard s.status == .running else { return false }
            s.status = .exited(code)
            s.updatedAt = .now
            s.stream?.yield((kind: TerminalFrameKind.exit, payload: TerminalFrames.encode(TerminalFrames.Exit(code: code))))
            s.stream?.finish()
            s.stream = nil
            return true
        }
        if changed { onJobChange(job) }
    }

    // MARK: Commands

    /// Runs one command line and writes its output and the next prompt; returns the exit code
    /// when the line ends the shell.
    private func run(_ line: String, in s: inout State) -> Int? {
        write("\r\n", in: &s)
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty, s.history.last != trimmed { s.history.append(trimmed) }
        let words = trimmed.split(separator: " ", omittingEmptySubsequences: true).map(String.init)
        guard let name = words.first?.lowercased() else {
            write(prompt(s), in: &s)
            return nil
        }
        let argument = words.dropFirst().joined(separator: " ").trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        if name == "exit" {
            return Int(argument) ?? 0
        }
        let output = isPowerShell ? powerShell(name, argument, line: trimmed, in: &s) : commandPrompt(name, argument, line: trimmed, in: &s)
        write(output + prompt(s), in: &s)
        return nil
    }

    private static let esc = "\u{1b}["

    private func powerShell(_ name: String, _ argument: String, line: String, in s: inout State) -> String {
        let esc = Self.esc
        switch name {
        case "dir", "ls", "gci", "get-childitem":
            let path = argument.isEmpty ? s.cwd : resolve(argument, from: s.cwd)
            guard let entries = folder(path) else { return pathError(argument, in: s) }
            return listing(path: path, entries: entries)
        case "cd", "sl", "chdir", "set-location":
            guard !argument.isEmpty else { return "" }
            let path = resolve(argument, from: s.cwd)
            guard folder(path) != nil else { return pathError(argument, in: s) }
            s.cwd = canonical(path)
            return ""
        case "pwd", "gl", "get-location":
            return "\r\n\(esc)32;1mPath\(esc)0m\r\n\(esc)32;1m----\(esc)0m\r\n\(s.cwd)\r\n\r\n"
        case "get-process", "ps", "gps":
            return processTable()
        case "whoami":
            return "\(computer.lowercased())\\\(user)\r\n"
        case "hostname":
            return "\(computer)\r\n"
        case "ipconfig":
            return ipconfig()
        case "get-date", "date":
            return "\r\n" + Self.longDate.string(from: .now) + "\r\n\r\n"
        case "cls", "clear", "clear-host":
            return "\u{1b}[2J\u{1b}[3J\u{1b}[H"
        case "echo", "write-output", "write-host":
            return argument + "\r\n"
        case "$psversiontable":
            let version = shell == .pwsh ? "7.4.6" : "5.1.26100.2161"
            let edition = shell == .pwsh ? "Core" : "Desktop"
            return "\r\n\(esc)32;1mName                           Value\(esc)0m\r\n\(esc)32;1m----                           -----\(esc)0m\r\n"
                + "PSVersion                      \(version)\r\nPSEdition                      \(edition)\r\n"
                + "BuildVersion                   10.0.26100.2161\r\nCLRVersion                     4.0.30319.42000\r\n\r\n"
        case "help", "get-help", "?":
            return String.ui("terminal.preview.help", "미리보기 셸이 아는 명령: dir, cd, Get-Process, whoami, hostname, ipconfig, Get-Date, $PSVersionTable, cls, echo, exit [코드]")
                + "\r\n"
        default:
            let word = line.split(separator: " ").first.map(String.init) ?? name
            let marks = String(repeating: "~", count: max(1, word.count))
            if shell == .pwsh {
                return "\(esc)31;1m\(word): \(esc)0m\(esc)31;1m'\(word)' 용어가 cmdlet, 함수, 스크립트 파일 또는 실행 가능한 프로그램의 이름으로 인식되지 않습니다.\r\n"
                    + "이름이 정확한지 확인하고 경로가 포함된 경우 경로가 올바른지 확인한 다음 다시 시도하세요.\(esc)0m\r\n"
            }
            return "\(esc)31;1m\(word) : '\(word)' 용어가 cmdlet, 함수, 스크립트 파일 또는 실행할 수 있는 프로그램 이름으로 인식되지 않습니다. "
                + "이름이 정확한지 확인하고 경로가 포함된 경우 경로가 올바른지 검증한 다음 다시 시도하십시오.\r\n"
                + "위치 줄:1 문자:1\r\n+ \(line)\r\n+ \(marks)\r\n"
                + "    + CategoryInfo          : ObjectNotFound: (\(word):String) [], CommandNotFoundException\r\n"
                + "    + FullyQualifiedErrorId : CommandNotFoundException\(esc)0m\r\n \r\n"
        }
    }

    private func commandPrompt(_ name: String, _ argument: String, line: String, in s: inout State) -> String {
        switch name {
        case "dir":
            let path = argument.isEmpty ? s.cwd : resolve(argument, from: s.cwd)
            guard let entries = folder(path) else { return "파일을 찾을 수 없습니다.\r\n\r\n" }
            return cmdListing(path: canonical(path), entries: entries)
        case "cd", "chdir":
            guard !argument.isEmpty else { return s.cwd + "\r\n\r\n" }
            let path = resolve(argument, from: s.cwd)
            guard folder(path) != nil else { return "지정된 경로를 찾을 수 없습니다.\r\n\r\n" }
            s.cwd = canonical(path)
            return "\r\n"
        case "whoami":
            return "\(computer.lowercased())\\\(user)\r\n\r\n"
        case "hostname":
            return "\(computer)\r\n\r\n"
        case "ver":
            return "\r\nMicrosoft Windows [Version 10.0.26100.2314]\r\n\r\n"
        case "ipconfig":
            return ipconfig() + "\r\n"
        case "cls":
            return "\u{1b}[2J\u{1b}[3J\u{1b}[H"
        case "echo":
            return argument + "\r\n\r\n"
        case "help":
            return String.ui("terminal.preview.helpCmd", "미리보기 셸이 아는 명령: dir, cd, whoami, hostname, ipconfig, ver, cls, echo, exit [코드]")
                + "\r\n\r\n"
        default:
            let word = line.split(separator: " ").first.map(String.init) ?? name
            return "'\(word)'은(는) 내부 또는 외부 명령, 실행할 수 있는 프로그램, 또는\r\n배치 파일이 아닙니다.\r\n\r\n"
        }
    }

    // MARK: Fixture folders

    private func folder(_ path: String) -> [FileEntry]? {
        let folders = PreviewFixtures.remoteFolders(user: user)
        let key = canonical(path).lowercased()
        return folders.first { $0.key.lowercased() == key }?.value
    }

    /// The folder's spelling in the fixtures, or `path` without a trailing separator.
    private func canonical(_ path: String) -> String {
        let folders = PreviewFixtures.remoteFolders(user: user)
        var trimmed = path
        while trimmed.count > 3, trimmed.hasSuffix("\\") { trimmed.removeLast() }
        if trimmed.count == 2, trimmed.hasSuffix(":") { trimmed += "\\" }
        return folders.keys.first { $0.lowercased() == trimmed.lowercased() } ?? trimmed
    }

    private func resolve(_ argument: String, from cwd: String) -> String {
        let normalized = argument.replacingOccurrences(of: "/", with: "\\")
        if normalized == "~" { return PreviewFixtures.remoteHome(user: user) }
        if normalized.count >= 2, normalized.dropFirst().first == ":" { return normalized }
        if normalized.hasPrefix("\\") { return "C:" + normalized }
        var parts = cwd.split(separator: "\\").map(String.init)
        for part in normalized.split(separator: "\\").map(String.init) {
            switch part {
            case ".": continue
            case "..": if parts.count > 1 { parts.removeLast() }
            default: parts.append(part)
            }
        }
        return parts.count == 1 ? parts[0] + "\\" : parts.joined(separator: "\\")
    }

    private func pathError(_ argument: String, in s: State) -> String {
        let path = resolve(argument, from: s.cwd)
        let esc = Self.esc
        return "\(esc)31;1m'\(path)' 경로는 존재하지 않으므로 찾을 수 없습니다.\(esc)0m\r\n"
    }

    private static let listDate: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ko_KR")
        f.dateFormat = "yyyy-MM-dd   a h:mm"
        return f
    }()

    private static let cmdDate: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ko_KR")
        f.dateFormat = "yyyy-MM-dd  a hh:mm"
        return f
    }()

    private static let longDate: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ko_KR")
        f.dateFormat = "yyyy년 M월 d일 EEEE a h:mm:ss"
        return f
    }()

    private func listing(path: String, entries: [FileEntry]) -> String {
        let esc = Self.esc
        let visible = entries.filter { $0.hidden != true }.sorted { ($0.dir ? 0 : 1, $0.name.lowercased()) < ($1.dir ? 0 : 1, $1.name.lowercased()) }
        var out = "\r\n\r\n    디렉터리: \(canonical(path))\r\n\r\n\r\n"
        out += "\(esc)32;1mMode                 LastWriteTime         Length Name\(esc)0m\r\n"
        out += "\(esc)32;1m----                 -------------         ------ ----\(esc)0m\r\n"
        for entry in visible {
            let mode = entry.dir ? "d-----" : (entry.readonly == true ? "-ar---" : "-a----")
            let date = Self.listDate.string(from: entry.mtime.date)
            let length = entry.dir ? "" : String(entry.size)
            let name = entry.dir ? "\(esc)34;1m\(entry.name)\(esc)0m" : (entry.name.lowercased().hasSuffix(".exe") || entry.name.lowercased().hasSuffix(".msi")
                ? "\(esc)32;1m\(entry.name)\(esc)0m" : entry.name)
            out += mode + Self.pad(date, 24) + Self.pad(length, 15) + " " + name + "\r\n"
        }
        return out + "\r\n"
    }

    private func cmdListing(path: String, entries: [FileEntry]) -> String {
        let visible = entries.filter { $0.hidden != true }
        var out = " C 드라이브의 볼륨에는 이름이 없습니다.\r\n 볼륨 일련 번호: 5A3C-91F2\r\n\r\n \(path) 디렉터리\r\n\r\n"
        var files = 0
        var bytes = 0
        for entry in visible {
            let date = Self.cmdDate.string(from: entry.mtime.date)
            if entry.dir {
                out += date + "    <DIR>          " + entry.name + "\r\n"
            } else {
                files += 1
                bytes += entry.size
                out += date + Self.pad(entry.size.formatted(.number.locale(Locale(identifier: "en_US"))), 18) + " " + entry.name + "\r\n"
            }
        }
        let dirs = visible.count - files
        out += Self.pad("\(files)", 16) + "개 파일" + Self.pad(bytes.formatted(.number.locale(Locale(identifier: "en_US"))), 20) + " 바이트\r\n"
        out += Self.pad("\(dirs)", 16) + "개 디렉터리  412,881,018,880 바이트 남음\r\n\r\n"
        return out
    }

    private func processTable() -> String {
        let esc = Self.esc
        let rows: [(Int, Int, Int, Int, Double, Int, String)] = [
            (1_284, 92, 412_336, 386_120, 812.44, 7_412, "Shop"),
            (412, 38, 182_904, 204_116, 96.17, 11_208, "Code"),
            (2_108, 112, 184_320, 246_712, 38.11, 7_724, "explorer"),
            (318, 24, 96_412, 121_048, 51.09, 9_312, "node"),
            (652, 41, 58_120, 73_344, 12.73, 2_116, "deskhand-agent"),
            (245, 18, 6_120, 21_844, 0.42, pid + 12, "conhost"),
            (598, 33, 61_208, 78_412, 1.88, pid, shell == .pwsh ? "pwsh" : "powershell"),
            (1_904, 64, 23_408, 39_016, 104.52, 1_448, "svchost"),
        ]
        var out = "\r\n\(esc)32;1mHandles  NPM(K)    PM(K)      WS(K)     CPU(s)     Id  SI ProcessName\(esc)0m\r\n"
        out += "\(esc)32;1m-------  ------    -----      -----     ------     --  -- -----------\(esc)0m\r\n"
        for row in rows {
            out += Self.pad("\(row.0)", 7) + Self.pad("\(row.1)", 8) + Self.pad("\(row.2)", 9) + Self.pad("\(row.3)", 11)
                + Self.pad(String(format: "%.2f", row.4), 11) + Self.pad("\(row.5)", 7) + Self.pad("1", 4) + " " + row.6 + "\r\n"
        }
        return out + "\r\n"
    }

    private func ipconfig() -> String {
        "\r\nWindows IP 구성\r\n\r\n\r\n이더넷 어댑터 이더넷:\r\n\r\n"
            + "   연결별 DNS 접미사. . . . : lan\r\n"
            + "   링크-로컬 IPv6 주소 . . . . : fe80::4c1a:8e2f:93b1:7a20%12\r\n"
            + "   IPv4 주소 . . . . . . . . . : \(address)\r\n"
            + "   서브넷 마스크 . . . . . . . : 255.255.255.0\r\n"
            + "   기본 게이트웨이 . . . . . . : 192.168.0.1\r\n"
    }

    private static func pad(_ text: String, _ width: Int) -> String {
        text.count >= width ? text : String(repeating: " ", count: width - text.count) + text
    }
}

/// A stream attached to a `PreviewShellProcess`.
final class PreviewTerminalStream: FrameStream {
    let frames: AsyncThrowingStream<StreamConnection.Frame, Error>
    private let continuation: AsyncThrowingStream<StreamConnection.Frame, Error>.Continuation
    private let process: PreviewShellProcess
    private let token: Int

    init(frames: AsyncThrowingStream<StreamConnection.Frame, Error>,
         continuation: AsyncThrowingStream<StreamConnection.Frame, Error>.Continuation, process: PreviewShellProcess, token: Int) {
        self.frames = frames
        self.continuation = continuation
        self.process = process
        self.token = token
    }

    func send(kind: UInt8, payload: Data) async throws {
        process.receive(kind: kind, payload: payload)
    }

    func close() {
        continuation.finish()
        process.detach(token: token)
    }
}

extension PreviewFixtures {
    /// What a simulated shell on `host` prints when `commands` are typed one after another:
    /// the banner, each command's output and the prompts, starting in `cwd` (the home folder
    /// when nil).
    public static func terminalTranscript(host: Host, shell: TerminalShell, commands: [String], cwd: String? = nil) -> String {
        let process = PreviewShellProcess(
            jobID: "j-transcript", ptyID: "p-transcript", host: host, shell: shell, pid: 6204, size: .standard, createdAt: .now,
            cwd: cwd ?? remoteHome(user: host.session?.user ?? "user"), summary: nil, openedBy: "Deskhand", onJobChange: { _ in })
        process.writeBanner()
        for command in commands {
            process.receive(kind: TerminalFrameKind.data, payload: Data((command + "\r").utf8))
        }
        return process.transcript
    }
}

// MARK: Backend

extension PreviewBackend {
    public func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        let host = try listedHost(hostID)
        try requireTerminal(host)
        if shell == .pwsh, !TerminalShell.available(on: host).contains(.pwsh) {
            throw RPCErrorInfo(message: "\(host.name)에 PowerShell 7(pwsh.exe)이 설치되어 있지 않습니다", code: "INVALID_ARGUMENT")
        }
        await pause(fraction: 0.5)
        let process = terminals.open(host: host, shell: shell, size: TerminalSize(cols: cols, rows: rows))
        return PTYOpening(ptyID: process.ptyID, jobID: process.jobID)
    }

    public func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream {
        guard let process = terminals.process(for: target) else {
            throw RPCErrorInfo(message: "셸을 찾을 수 없습니다. 이미 종료되었을 수 있습니다", code: "NOT_FOUND")
        }
        try requireTerminal(try listedHost(process.hostID))
        await pause(fraction: 0.3)
        return process.attach()
    }

    public func ptyJobs(hostID: String) async throws -> [Job] {
        _ = try listedHost(hostID)
        await pause(fraction: 0.2)
        return terminals.runningJobs(hostID: hostID)
    }

    public func ptyKill(jobID: String) async throws {
        guard let process = terminals.process(for: .job(jobID)) else {
            throw RPCErrorInfo(message: "셸을 찾을 수 없습니다. 이미 종료되었을 수 있습니다", code: "NOT_FOUND")
        }
        await pause(fraction: 0.2)
        process.kill()
    }

    /// Throws `OFFLINE` unless the computer is connected, and `CAPABILITY_UNSUPPORTED` unless
    /// its `pty` capability is ready.
    private func requireTerminal(_ host: Host) throws {
        guard host.connection == .ready || host.connection == .degraded else {
            throw RPCErrorInfo(message: "\(host.name)에 연결되어 있지 않습니다", code: "OFFLINE", retryable: true)
        }
        if let pty = host.caps.pty, pty.capabilityState != .ready {
            throw RPCErrorInfo(message: pty.reason ?? "에이전트가 터미널을 열 수 없다고 알렸습니다", code: "CAPABILITY_UNSUPPORTED")
        }
    }
}
