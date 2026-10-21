import DeskhandClient
import Foundation
import Observation

/// A short message over the terminal tab, such as a shell that could not be ended.
public struct TerminalNotice: Equatable, Identifiable, Sendable {
    public let id = UUID()
    public var message: String
    public var detail: String?
    public var code: String?
}

/// The terminal tab of one computer: its shell tabs and the selected one, the running pty jobs
/// no tab shows (분리된 셸), the close and paste confirmations, and the text size. Lives while
/// the computer is listed and keeps its shells attached while other tabs or computers are shown.
@MainActor
@Observable
public final class TerminalWorkspace {
    public let hostID: String
    public let preferences: TerminalPreferences
    public private(set) var sessions: [TerminalSession] = []
    public var selectedID: TerminalSession.ID? {
        didSet { if selectedID != oldValue { focusRequest += 1 } }
    }
    /// Running pty jobs of the computer, newest first.
    public private(set) var runningJobs: [Job] = []
    public private(set) var jobsLoaded = false
    /// Why the last `jobs.list` failed, until a load succeeds.
    public private(set) var jobsError: OperationError?
    /// The tab whose running shell waits for 셸 종료 or 분리만 하기.
    public var pendingClose: TerminalSession?
    /// Clipboard text waiting for the paste confirmation.
    public var pendingPaste: PasteRequest?
    public private(set) var notice: TerminalNotice?
    /// Increases whenever the selected terminal should take keyboard focus.
    public private(set) var focusRequest = 0

    @ObservationIgnored private let backend: any TerminalBackend & EventsBackend
    @ObservationIgnored private let backoff: AppStore.ResubscribeBackoff
    @ObservationIgnored private var lastSize = TerminalSize.standard
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var jobsTask: Task<Void, Never>?
    /// Job updates received while a `jobs.list` call runs, applied over its result.
    @ObservationIgnored private var updatesDuringRefresh: [Job]?

    init(hostID: String, backend: any TerminalBackend & EventsBackend, preferences: TerminalPreferences,
         backoff: AppStore.ResubscribeBackoff = .standard) {
        self.hostID = hostID
        self.backend = backend
        self.preferences = preferences
        self.backoff = backoff
    }

    /// Running pty jobs that no tab shows (분리된 셸), newest first.
    public var detachedJobs: [Job] {
        let shown = Set(sessions.compactMap(\.jobID))
        return runningJobs.filter { !shown.contains($0.jobID) }
    }

    public var selected: TerminalSession? {
        sessions.first { $0.id == selectedID } ?? sessions.first
    }

    // MARK: Lifecycle

    /// Follows `job.updated` events of the computer and loads its running pty jobs. When the
    /// event stream ends it subscribes again with backoff and then reloads the jobs.
    public func start() {
        if eventsTask == nil {
            let backend = backend
            let hostID = hostID
            let backoff = backoff
            eventsTask = Task { [weak self] in
                var delay = backoff.initial
                var resubscribing = false
                while !Task.isCancelled, self != nil {
                    let stream = backend.events(kinds: [EventKind.jobUpdated], hostID: hostID)
                    if resubscribing { self?.refreshJobs() }
                    resubscribing = true
                    var received = false
                    do {
                        for try await event in stream {
                            received = true
                            self?.handle(event)
                        }
                    } catch {}
                    guard !Task.isCancelled else { return }
                    if received { delay = backoff.initial }
                    try? await Task.sleep(for: delay)
                    delay = min(delay * 2, backoff.maximum)
                }
            }
        }
        refreshJobs()
    }

    /// Detaches every tab and stops following events; the shells keep running on the computer.
    public func stop() {
        eventsTask?.cancel()
        eventsTask = nil
        jobsTask?.cancel()
        for session in sessions { session.detach() }
        sessions = []
        selectedID = nil
    }

    /// The computer's connection changed: tabs waiting to reattach try again at once when it is back.
    public func connectionChanged(_ connection: ConnectionState) {
        guard connection == .ready || connection == .degraded else { return }
        for session in sessions {
            if case .reconnecting = session.phase { session.reconnect() }
        }
        refreshJobs()
    }

    // MARK: Tabs

    /// Opens `shell` in a new tab and selects it.
    public func open(_ shell: TerminalShell) {
        let session = makeSession(shell: shell, jobID: nil)
        insert(session)
        session.start()
    }

    /// Puts a new `shell` in the place of a tab whose shell is not running.
    public func replace(_ session: TerminalSession, with shell: TerminalShell) {
        guard let index = sessions.firstIndex(where: { $0.id == session.id }) else { return }
        session.detach()
        let replacement = makeSession(shell: shell, jobID: nil)
        sessions[index] = replacement
        selectedID = replacement.id
        replacement.start()
    }

    /// Attaches a running pty job in a new tab; the agent replays its scrollback.
    public func reopen(_ job: Job) {
        if let existing = sessions.first(where: { $0.jobID == job.jobID }) {
            selectedID = existing.id
            return
        }
        let session = makeSession(shell: TerminalShell(job: job), jobID: job.jobID)
        insert(session)
        session.start()
    }

    public func select(_ session: TerminalSession) {
        selectedID = session.id
    }

    /// Selects the tab after (or, with `forward` false, before) the selected one, wrapping around.
    public func selectAdjacent(forward: Bool) {
        guard sessions.count > 1, let current = selected, let index = sessions.firstIndex(where: { $0.id == current.id }) else { return }
        let next = (index + (forward ? 1 : sessions.count - 1)) % sessions.count
        selectedID = sessions[next].id
    }

    /// Asks before closing a tab whose shell runs on the computer; closes other tabs at once,
    /// ending a shell that is still being opened.
    public func requestClose(_ session: TerminalSession? = nil) {
        guard let session = session ?? selected else { return }
        if session.isShellRunning {
            pendingClose = session
            return
        }
        switch session.phase {
        case .idle, .opening, .attaching: close(session, ending: true)
        case .live, .reconnecting, .exited, .failed: close(session, ending: false)
        }
    }

    /// Removes the tab. With `ending` the shell ends on the computer (`jobs.cancel`); otherwise
    /// it keeps running and appears under 분리된 셸.
    public func close(_ session: TerminalSession, ending: Bool) {
        if pendingClose?.id == session.id { pendingClose = nil }
        remove(session)
        if ending {
            Task { [weak self] in
                do {
                    try await session.terminate()
                } catch {
                    let failure = OperationError(error)
                    self?.notice = TerminalNotice(
                        message: .ui("terminal.kill.failed", "셸을 종료하지 못했습니다"),
                        detail: .ui("terminal.kill.failedDetail", "셸은 원격 PC에서 계속 실행됩니다. + 메뉴의 분리된 셸에서 다시 열 수 있습니다."),
                        code: failure.code)
                }
                self?.refreshJobs()
            }
        } else {
            session.detach()
            refreshJobs()
        }
    }

    // MARK: Paste

    /// Sends the clipboard text without escape sequences and control characters, or asks first
    /// when any were removed, or when it holds line breaks and the setting is on.
    func requestPaste(_ session: TerminalSession, text: String) {
        let request = PasteRequest(sessionID: session.id, clipboard: text)
        if request.removedControls == 0, !(preferences.confirmsMultilinePaste && PasteRequest.needsConfirmation(request.text)) {
            session.paste(request.text)
            return
        }
        pendingPaste = request
    }

    /// True while the request's shell accepts input and the request has text to send.
    public func canSendPaste(_ request: PasteRequest) -> Bool {
        guard !request.text.isEmpty, let session = sessions.first(where: { $0.id == request.sessionID }) else { return false }
        return session.acceptsInput
    }

    /// Sends a confirmed paste. While its shell does not accept input, the request stays
    /// pending and nothing is sent; returns whether it was sent.
    @discardableResult
    public func confirmPaste(_ request: PasteRequest, dontAskAgain: Bool) -> Bool {
        guard canSendPaste(request), let session = sessions.first(where: { $0.id == request.sessionID }) else { return false }
        if dontAskAgain { preferences.confirmsMultilinePaste = false }
        pendingPaste = nil
        session.paste(request.text)
        focusRequest += 1
        return true
    }

    public func cancelPaste() {
        pendingPaste = nil
        focusRequest += 1
    }

    // MARK: Text size

    public func adjustFontSize(by step: Double) {
        let size = min(max(preferences.fontSize + step, Double(DS.Terminal.minimumFontSize)), Double(DS.Terminal.maximumFontSize))
        setFontSize(size)
    }

    public func resetFontSize() {
        setFontSize(Double(DS.Terminal.fontSize))
    }

    /// Applies the text size setting to every tab, after another computer's terminal changed it.
    public func applyFontSize() {
        for session in sessions { session.surface.fontSize = CGFloat(preferences.fontSize) }
    }

    private func setFontSize(_ size: Double) {
        preferences.fontSize = size
        applyFontSize()
    }

    public func dismissNotice() {
        notice = nil
    }

    // MARK: Jobs

    /// Loads the computer's running pty jobs; job updates that arrive meanwhile apply over the result.
    public func refreshJobs() {
        jobsTask?.cancel()
        updatesDuringRefresh = []
        jobsTask = Task { [weak self] in
            guard let self else { return }
            do {
                let jobs = try await backend.ptyJobs(hostID: hostID)
                guard !Task.isCancelled else { return }
                runningJobs = jobs
                for job in updatesDuringRefresh ?? [] { apply(job) }
                updatesDuringRefresh = nil
                jobsError = nil
                jobsLoaded = true
            } catch {
                guard !Task.isCancelled else { return }
                updatesDuringRefresh = nil
                jobsError = OperationError(error)
                jobsLoaded = true
            }
        }
    }

    /// Applies a `job.updated` event of a pty job of this computer to the running jobs.
    func handle(_ event: DaemonEvent) {
        guard event.kind == EventKind.jobUpdated, let value = event.payload["job"],
              let job = try? LiveBackend.decode(Job.self, from: value), job.kind == "pty", job.hostID == hostID else { return }
        updatesDuringRefresh?.append(job)
        apply(job)
    }

    private func apply(_ job: Job) {
        runningJobs.removeAll { $0.jobID == job.jobID }
        if job.status == EnvelopeStatus.running.rawValue {
            runningJobs.insert(job, at: 0)
        }
    }

    // MARK: Helpers

    private func makeSession(shell: TerminalShell, jobID: String?) -> TerminalSession {
        let session = TerminalSession(hostID: hostID, shell: shell, backend: backend, size: lastSize,
                                      fontSize: CGFloat(preferences.fontSize), jobID: jobID, backoff: backoff)
        session.onPaste = { [weak self] session, text in self?.requestPaste(session, text: text) }
        session.onSizeChange = { [weak self] size in self?.lastSize = size }
        session.surface.optionAsMetaKey = preferences.optionAsMeta
        session.surface.allowsClipboardWrite = { [weak preferences] in preferences?.allowsClipboardWrite ?? false }
        return session
    }

    private func insert(_ session: TerminalSession) {
        if let current = selected, let index = sessions.firstIndex(where: { $0.id == current.id }) {
            sessions.insert(session, at: index + 1)
        } else {
            sessions.append(session)
        }
        selectedID = session.id
    }

    private func remove(_ session: TerminalSession) {
        guard let index = sessions.firstIndex(where: { $0.id == session.id }) else { return }
        sessions.remove(at: index)
        if selectedID == session.id || selected == nil {
            selectedID = sessions.isEmpty ? nil : sessions[min(index, sessions.count - 1)].id
        }
    }

    /// Seeds the tab with stand-in sessions and jobs, for snapshot scenes.
    public func seed(_ sessions: [TerminalSession], selected: Int = 0, detachedJobs: [Job] = [],
                     pendingClose: Int? = nil, pendingPaste: String? = nil) {
        self.sessions = sessions
        selectedID = sessions.indices.contains(selected) ? sessions[selected].id : nil
        runningJobs = detachedJobs
        jobsLoaded = true
        self.pendingClose = pendingClose.flatMap { sessions.indices.contains($0) ? sessions[$0] : nil }
        if let pendingPaste, let session = self.selected {
            self.pendingPaste = PasteRequest(sessionID: session.id, clipboard: pendingPaste)
        }
    }
}
