import AppKit
import DeskhandClient
import Foundation
import Observation
import os

/// One remote shell shown in a terminal tab. Opens the pty, attaches its stream, feeds the
/// output to the terminal surface and sends keyboard input and size changes. When the stream
/// drops without an exit frame it attaches again by job ID with backoff, and clears the surface
/// at each attached frame for the scrollback the agent replays.
@MainActor
@Observable
public final class TerminalSession: Identifiable {
    public enum Phase: Equatable, Sendable {
        /// Not started.
        case idle
        /// `pty.open` is running.
        case opening
        /// The first attach of this shell is running, or waiting for the attached frame.
        case attaching
        case live
        /// The stream ended without an exit frame; attaching again by job ID. Carries the last
        /// attach failure.
        case reconnecting(OperationError?)
        /// The shell ended with this exit code.
        case exited(Int)
        /// The shell could not be opened, or its job cannot be attached any more.
        case failed(OperationError)
    }

    public let id = UUID()
    public let hostID: String
    public let shell: TerminalShell
    public private(set) var phase: Phase = .idle
    /// The pty's job, from the `pty.open` envelope or the attached frame.
    public private(set) var jobID: String?
    public private(set) var ptyID: String?
    /// Process ID of the shell on the remote computer.
    public private(set) var pid: Int?
    /// Title the shell set with OSC 0 or 2.
    public private(set) var remoteTitle: String?
    /// Working directory the shell reported with OSC 7, as a Windows path.
    public private(set) var directory: String?
    /// Columns and rows of the terminal surface.
    public private(set) var size: TerminalSize
    /// True once the shell sent output to this tab.
    public private(set) var hasOutput = false
    /// When the next automatic attach starts, while reconnecting.
    public private(set) var nextAttempt: Date?
    /// True once an attached frame arrived; later attaches use the job ID.
    public private(set) var hasAttached = false

    /// Receives clipboard text the person pasted; sends it right away unless replaced.
    @ObservationIgnored public var onPaste: (TerminalSession, String) -> Void = { session, text in session.paste(text) }
    /// Receives the surface size after each change.
    @ObservationIgnored public var onSizeChange: (TerminalSize) -> Void = { _ in }

    @ObservationIgnored let surface: TerminalSurfaceView
    @ObservationIgnored private let backend: any TerminalBackend
    @ObservationIgnored private let backoff: AppStore.ResubscribeBackoff
    /// How long a stream stays attached before a drop retries from the initial delay again.
    @ObservationIgnored private let stableAttach: Duration
    /// The wait before the latest automatic attach.
    @ObservationIgnored private(set) var lastRetryDelay: Duration = .zero
    @ObservationIgnored private var runTask: Task<Void, Never>?
    @ObservationIgnored private var runToken = 0
    @ObservationIgnored private var outbox: AsyncStream<OutFrame>.Continuation?
    @ObservationIgnored private var stream: (any FrameStream)?
    @ObservationIgnored private var sentSize: TerminalSize?
    @ObservationIgnored private var resizeTask: Task<Void, Never>?
    @ObservationIgnored private var waiter: CheckedContinuation<Void, Never>?
    @ObservationIgnored private var waitTimer: Task<Void, Never>?
    @ObservationIgnored private var terminating = false

    nonisolated static let log = Logger(subsystem: "io.github.yangtaeyoung.Deskhand", category: "terminal")

    /// A session for `shell` on `hostID`; with `jobID` it attaches to that running pty job
    /// instead of opening a new shell.
    public init(hostID: String, shell: TerminalShell, backend: any TerminalBackend, size: TerminalSize = .standard,
                fontSize: CGFloat = DS.Terminal.fontSize, jobID: String? = nil, backoff: AppStore.ResubscribeBackoff = .standard,
                stableAttach: Duration = .seconds(5)) {
        self.stableAttach = stableAttach
        self.hostID = hostID
        self.shell = shell
        self.backend = backend
        self.size = size
        self.jobID = jobID
        self.backoff = backoff
        surface = TerminalSurfaceView(size: size, fontSize: fontSize)
        surface.onInput = { [weak self] bytes in self?.sendInput(bytes) }
        surface.onResize = { [weak self] size in self?.resize(size) }
        surface.onTitle = { [weak self] title in self?.remoteTitle = title }
        surface.onDirectory = { [weak self] directory in self?.directory = directory.flatMap(TerminalTitle.windowsPath) }
        surface.onPaste = { [weak self] text in
            guard let self else { return }
            onPaste(self, text)
        }
        surface.acceptsPaste = { [weak self] in self?.acceptsInput ?? false }
    }

    /// A session showing fixed state and output, for snapshot stand-ins.
    public static func standIn(hostID: String, shell: TerminalShell, phase: Phase, output: String, size: TerminalSize = .standard,
                               title: String? = nil, directory: String? = nil, jobID: String? = "j-standin") -> TerminalSession {
        let session = TerminalSession(hostID: hostID, shell: shell, backend: UnavailableTerminalBackend(), size: size, jobID: jobID)
        session.phase = phase
        switch phase {
        case .live, .reconnecting, .exited: session.hasAttached = true
        case .idle, .opening, .attaching, .failed: break
        }
        session.surface.isStandIn = true
        session.surface.feed(Data(output.utf8))
        session.hasOutput = !output.isEmpty
        if let title { session.remoteTitle = title }
        if let directory { session.directory = directory }
        return session
    }

    // MARK: Presentation

    /// Tab title: the shell, or the title it set when that says more than its program path.
    public var title: String { TerminalTitle.text(shell: shell, remoteTitle: remoteTitle) }

    /// Last component of the working directory (`Downloads`, `C:\`).
    public var directoryName: String? { directory.map(TerminalTitle.leaf) }

    /// True while the shell runs on the computer: closing the tab then asks whether to end it.
    public var isShellRunning: Bool {
        switch phase {
        case .live, .reconnecting: true
        case .attaching: jobID != nil
        case .idle, .opening, .exited, .failed: false
        }
    }

    /// True while keyboard input reaches the shell.
    public var acceptsInput: Bool { phase == .live && outbox != nil }

    // MARK: Running

    /// Opens the shell, or attaches to the session's job, and keeps the stream attached until
    /// the shell exits, the session fails or it is detached.
    public func start() {
        guard runTask == nil else { return }
        terminating = false
        runToken += 1
        let token = runToken
        runTask = Task { [weak self] in
            await self?.run()
            guard let self, token == runToken else { return }
            runTask = nil
        }
    }

    /// Attaches again now: skips the wait while reconnecting, and starts over after a failure.
    public func reconnect() {
        if runTask != nil {
            wake()
        } else {
            start()
        }
    }

    /// Opens a new shell of the same kind in this tab after the old one exited or failed.
    public func restart() {
        detach()
        jobID = nil
        ptyID = nil
        hasAttached = false
        pid = nil
        remoteTitle = nil
        directory = nil
        hasOutput = false
        surface.resetForReplay()
        phase = .idle
        start()
    }

    /// Closes the stream and stops attaching; the shell keeps running on the computer.
    public func detach() {
        runToken += 1
        runTask?.cancel()
        runTask = nil
        resizeTask?.cancel()
        resizeTask = nil
        stream?.close()
        outbox?.finish()
        outbox = nil
        stream = nil
        wake()
    }

    /// Ends the shell on the computer (`jobs.cancel`) and detaches. A shell still being opened
    /// is ended once `pty.open` reports its job.
    public func terminate() async throws {
        terminating = true
        let job = jobID
        detach()
        if let job { try await backend.ptyKill(jobID: job) }
    }

    /// Sends clipboard text as typed while the shell accepts input: line breaks as Return, in
    /// bracketed-paste markers when the shell asked for them. Returns whether it was sent.
    @discardableResult
    public func paste(_ text: String) -> Bool {
        guard acceptsInput else { return false }
        surface.sendPaste(text)
        return true
    }

    /// Discards the lines above the screen; the remote shell is not told.
    public func clearScrollback() {
        surface.clearScrollback()
    }

    /// Records the terminal size. While attached, schedules a resize frame (`0x21`) that goes
    /// out `resizeDelay` later with the size at that moment, unless one is already scheduled.
    public func resize(_ size: TerminalSize) {
        guard size.cols > 0, size.rows > 0 else { return }
        if self.size != size {
            self.size = size
            onSizeChange(size)
        }
        guard acceptsInput, size != sentSize, resizeTask == nil else { return }
        resizeTask = Task { [weak self] in
            try? await Task.sleep(for: Self.resizeDelay)
            guard let self, !Task.isCancelled else { return }
            resizeTask = nil
            sendSize()
        }
    }

    /// How long a new size waits before its resize frame goes out.
    static let resizeDelay = Duration.milliseconds(60)

    /// Sends the current size in a resize frame when it differs from the last one sent.
    private func sendSize() {
        guard acceptsInput, let outbox, size != sentSize else { return }
        sentSize = size
        outbox.yield(OutFrame(kind: TerminalFrameKind.resize, payload: TerminalFrames.encode(size)))
    }

    private func sendInput(_ bytes: ArraySlice<UInt8>) {
        guard acceptsInput, let outbox, !bytes.isEmpty else { return }
        outbox.yield(OutFrame(kind: TerminalFrameKind.data, payload: Data(bytes)))
    }

    private enum StreamEnd {
        case exited(Int)
        case dropped(attached: Bool)
    }

    private func run() async {
        var delay = backoff.initial
        while !Task.isCancelled {
            let target: PTYTarget
            if let jobID, hasAttached || ptyID == nil {
                target = .job(jobID)
            } else if let ptyID {
                target = .pty(ptyID)
            } else {
                phase = .opening
                do {
                    let opening = try await backend.ptyOpen(hostID: hostID, shell: shell, cols: size.cols, rows: size.rows)
                    if Task.isCancelled {
                        if terminating, let job = opening.jobID { try? await backend.ptyKill(jobID: job) }
                        return
                    }
                    ptyID = opening.ptyID
                    jobID = opening.jobID
                } catch {
                    if Task.isCancelled { return }
                    phase = .failed(OperationError(error))
                    return
                }
                continue
            }
            if case .reconnecting = phase {} else { phase = .attaching }
            let stream: any FrameStream
            do {
                stream = try await backend.ptyAttach(target)
            } catch {
                if Task.isCancelled { return }
                let failure = OperationError(error)
                if Self.isPermanent(failure) {
                    phase = .failed(failure)
                    return
                }
                phase = .reconnecting(failure)
                lastRetryDelay = delay
                await pause(delay)
                delay = min(delay * 2, backoff.maximum)
                continue
            }
            if Task.isCancelled {
                stream.close()
                return
            }
            let clock = ContinuousClock()
            let attachStart = clock.now
            let end = await withTaskCancellationHandler {
                await consume(stream)
            } onCancel: {
                stream.close()
            }
            let attachedFor = clock.now - attachStart
            stream.close()
            if Task.isCancelled { return }
            switch end {
            case .exited(let code):
                phase = .exited(code)
                return
            case .dropped(let attached):
                Self.log.info("pty stream of \(self.jobID ?? "?", privacy: .public) ended; attaching again")
                if attached, attachedFor >= stableAttach { delay = backoff.initial }
                phase = .reconnecting(nil)
                lastRetryDelay = delay
                await pause(delay)
                delay = min(delay * 2, backoff.maximum)
            }
        }
    }

    /// Reads one stream until it ends; input and resize frames go out through its outbox
    /// while it is attached.
    private func consume(_ stream: any FrameStream) async -> StreamEnd {
        let (outFrames, outbox) = AsyncStream.makeStream(of: OutFrame.self)
        let sender = Task.detached {
            for await frame in outFrames {
                try? await stream.send(kind: frame.kind, payload: frame.payload)
            }
        }
        self.stream = stream
        defer {
            outbox.finish()
            sender.cancel()
            if self.stream === stream {
                self.stream = nil
                self.outbox = nil
            }
        }
        var attached = false
        do {
            for try await frame in stream.frames {
                switch frame.kind {
                case TerminalFrameKind.attached:
                    if let info = TerminalFrames.decode(TerminalFrames.Attached.self, from: frame.payload) {
                        jobID = info.job
                        pid = info.pid
                    }
                    attached = true
                    hasAttached = true
                    surface.resetForReplay()
                    self.outbox = outbox
                    sentSize = nil
                    phase = .live
                    sendSize()
                case TerminalFrameKind.data:
                    surface.feed(frame.payload)
                    if !frame.payload.isEmpty { hasOutput = true }
                case TerminalFrameKind.exit:
                    let code = TerminalFrames.decode(TerminalFrames.Exit.self, from: frame.payload)?.code ?? 0
                    return .exited(code)
                default:
                    continue
                }
            }
        } catch {
            Self.log.info("pty stream failed: \(String(describing: error), privacy: .public)")
        }
        return .dropped(attached: attached)
    }

    /// Waits `delay`, or until `reconnect` or cancellation wakes it.
    private func pause(_ delay: Duration) async {
        let seconds = Double(delay.components.seconds) + Double(delay.components.attoseconds) / 1e18
        nextAttempt = Date.now.addingTimeInterval(seconds)
        await withTaskCancellationHandler {
            await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
                if Task.isCancelled {
                    continuation.resume()
                    return
                }
                waiter = continuation
                waitTimer = Task { [weak self] in
                    try? await Task.sleep(for: delay)
                    self?.wake()
                }
            }
        } onCancel: {
            Task { @MainActor [weak self] in self?.wake() }
        }
        nextAttempt = nil
    }

    private func wake() {
        waitTimer?.cancel()
        waitTimer = nil
        let waiter = waiter
        self.waiter = nil
        waiter?.resume()
    }

    /// Error codes for which attaching again cannot succeed without a change by the person.
    static func isPermanent(_ error: OperationError) -> Bool {
        switch error.code {
        case "NOT_FOUND", "INVALID_ARGUMENT", "CAPABILITY_UNSUPPORTED", "PERMISSION_DENIED", "AUTH_REQUIRED", "ELEVATION_REQUIRED":
            true
        default:
            false
        }
    }
}

/// Tab titles and working directories from what the shell reports.
enum TerminalTitle {
    /// The shell's name, or the title the shell set. A title that is the path of a shell
    /// program becomes the shell's name, keeping what follows it
    /// (`C:\WINDOWS\system32\cmd.exe - ping localhost` → `cmd · ping localhost`).
    static func text(shell: TerminalShell, remoteTitle: String?) -> String {
        guard let raw = remoteTitle?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty else { return shell.shortTitle }
        let lower = raw.lowercased()
        for program in TerminalShell.allCases.map(\.program) {
            guard let range = lower.range(of: program) else { continue }
            let prefix = lower[..<range.lowerBound]
            guard prefix.isEmpty || prefix.hasSuffix("\\") || prefix.hasSuffix("/") || prefix.hasSuffix(": ") else { continue }
            let offset = lower.distance(from: lower.startIndex, to: range.upperBound)
            let rest = String(raw.dropFirst(offset)).trimmingCharacters(in: CharacterSet(charactersIn: " -"))
            return rest.isEmpty ? shell.shortTitle : "\(shell.shortTitle) · \(rest)"
        }
        if ["windows powershell", "powershell", "command prompt", "명령 프롬프트"].contains(lower) { return shell.shortTitle }
        return raw
    }

    /// A Windows path from an OSC 7 value: `file://PC/C:/Users/taeyoung` → `C:\Users\taeyoung`.
    /// Other values are kept as they are.
    static func windowsPath(_ value: String) -> String {
        guard let url = URL(string: value), url.scheme?.lowercased() == "file" else { return value }
        var path = url.path(percentEncoded: false)
        if path.hasPrefix("/"), path.dropFirst().prefix(2).last == ":" { path.removeFirst() }
        path = path.replacingOccurrences(of: "/", with: "\\")
        if path.count == 2, path.last == ":" { path += "\\" }
        return path
    }

    /// The last component of a Windows or POSIX path; a drive root stays whole.
    static func leaf(_ path: String) -> String {
        let trimmed = path.count > 3 ? path.trimmingCharacters(in: CharacterSet(charactersIn: "\\/")) : path
        guard let separator = trimmed.lastIndex(where: { $0 == "\\" || $0 == "/" }) else { return trimmed }
        let leaf = trimmed[trimmed.index(after: separator)...]
        return leaf.isEmpty ? trimmed : String(leaf)
    }
}

/// Backend of stand-in sessions, which never attach.
private struct UnavailableTerminalBackend: TerminalBackend {
    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        throw ClientError.notConnected
    }

    func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream {
        throw ClientError.notConnected
    }

    func ptyJobs(hostID: String) async throws -> [Job] { [] }

    func ptyKill(jobID: String) async throws {}
}
