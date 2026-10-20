import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// A shell a terminal tab runs on the remote computer. Raw values are `pty.open`'s `shell`.
public enum TerminalShell: String, CaseIterable, Identifiable, Sendable, Codable {
    /// Windows PowerShell 5.1 (`powershell.exe`).
    case powershell
    /// PowerShell 7 (`pwsh.exe`).
    case pwsh
    /// Command Prompt (`cmd.exe`).
    case cmd

    public var id: String { rawValue }

    /// Name in menus and the empty state.
    public var title: String {
        switch self {
        case .powershell: "PowerShell"
        case .pwsh: "PowerShell 7"
        case .cmd: .ui("terminal.shell.cmd", "명령 프롬프트")
        }
    }

    /// Name in tab titles.
    public var shortTitle: String {
        switch self {
        case .powershell: "PowerShell"
        case .pwsh: "PowerShell 7"
        case .cmd: "cmd"
        }
    }

    /// The program file, shown beside the title in menus.
    public var program: String {
        switch self {
        case .powershell: "powershell.exe"
        case .pwsh: "pwsh.exe"
        case .cmd: "cmd.exe"
        }
    }

    public var symbol: String {
        switch self {
        case .powershell: "terminal"
        case .pwsh: "terminal.fill"
        case .cmd: "chevron.forward.square"
        }
    }

    /// Shells to offer for `host`: PowerShell and Command Prompt, with PowerShell 7 when the
    /// `pty` capability lists `pwsh`.
    public static func available(on host: Host) -> [TerminalShell] {
        let reported = host.caps.pty?.shells ?? []
        return allCases.filter { $0 != .pwsh || reported.contains(TerminalShell.pwsh.rawValue) }
    }

    /// The shell of a pty job: `result.shell` when the daemon reports it, otherwise the program
    /// named in its summary, otherwise PowerShell.
    public init(job: Job) {
        if let raw = job.result?["shell"]?.stringValue, let shell = TerminalShell(rawValue: raw) {
            self = shell
            return
        }
        let summary = job.summary.lowercased()
        if summary.contains("pwsh") || summary.contains("powershell 7") {
            self = .pwsh
        } else if summary.contains("cmd") {
            self = .cmd
        } else {
            self = .powershell
        }
    }
}

/// Columns and rows of a terminal; the JSON of a resize frame (`0x21`).
public struct TerminalSize: Codable, Equatable, Hashable, Sendable {
    public var cols: Int
    public var rows: Int

    public init(cols: Int, rows: Int) {
        self.cols = cols
        self.rows = rows
    }

    public static let standard = TerminalSize(cols: 80, rows: 24)

    /// `120×32`.
    public var text: String { "\(cols)×\(rows)" }
}

/// Frame kinds of a `pty` stream (protocol.md §7).
public enum TerminalFrameKind {
    /// Raw UTF-8 bytes, both directions.
    public static let data: UInt8 = 0x20
    /// App → daemon: `{cols, rows}`.
    public static let resize: UInt8 = 0x21
    /// Daemon → app: `{code}`, the shell's exit code.
    public static let exit: UInt8 = 0x22
    /// Daemon → app: `{job, pid}`, then the scrollback as data frames.
    public static let attached: UInt8 = 0x23
}

/// JSON bodies of pty frames.
enum TerminalFrames {
    struct Attached: Codable, Equatable {
        var job: String
        var pid: Int?
    }

    struct Exit: Codable, Equatable {
        var code: Int
    }

    static func encode(_ value: some Encodable) -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return (try? encoder.encode(value)) ?? Data()
    }

    static func decode<T: Decodable>(_ type: T.Type, from data: Data) -> T? {
        try? JSONDecoder().decode(type, from: data)
    }
}
