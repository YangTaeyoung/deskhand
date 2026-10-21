import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Whether a computer can open terminals, from its connection and `pty` capability.
enum TerminalAvailability: Equatable {
    case ready
    case connecting
    case reconnecting
    case offline
    case blocked
    /// The agent reports the `pty` capability as not ready.
    case unavailable(CapabilityState, reason: String?)

    init(host: Host) {
        switch host.connection {
        case .offline: self = .offline
        case .blocked: self = .blocked
        case .connecting: self = .connecting
        case .reconnecting: self = .reconnecting
        case .ready, .degraded:
            if let pty = host.caps.pty, pty.capabilityState != .ready {
                self = .unavailable(pty.capabilityState, reason: pty.reason)
            } else {
                self = .ready
            }
        }
    }
}

/// The terminal tab: a strip of shell tabs over the selected terminal. A dropped stream of a
/// connected computer shows a bar above the terminal and an exited shell a bar below it; the
/// opening and failed states are drawn over it. Without tabs it shows the empty state with
/// the shells to open and the detached shells to reopen. It also presents the close and paste
/// confirmations.
public struct TerminalView: View {
    let store: AppStore
    let host: Host
    let chrome: WorkspaceChrome

    public init(store: AppStore, host: Host, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.host = host
        self.chrome = chrome
    }

    public var body: some View {
        let workspace = store.terminals(for: host)
        TerminalContent(workspace: workspace, host: host)
            .task(id: host.hostID) {
                guard chrome == .native else { return }
                workspace.applyFontSize()
                workspace.start()
            }
            .onChange(of: host.connection) { _, connection in
                workspace.connectionChanged(connection)
            }
    }
}

private struct TerminalContent: View {
    @Bindable var workspace: TerminalWorkspace
    let host: Host
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var availability: TerminalAvailability { TerminalAvailability(host: host) }
    private var hostConnected: Bool { host.connection == .ready || host.connection == .degraded }

    var body: some View {
        VStack(spacing: 0) {
            if workspace.sessions.isEmpty {
                TerminalEmptyState(workspace: workspace, host: host, availability: availability)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                TerminalTabStrip(workspace: workspace, host: host, canOpen: availability == .ready)
                    .layoutPriority(1)
                if let session = workspace.selected, case .reconnecting(let error) = session.phase, hostConnected {
                    StreamReconnectBar(session: session, error: error, hostConnected: hostConnected)
                        .layoutPriority(1)
                }
                ZStack {
                    DS.Color.terminalBackground
                    TerminalStack(sessions: workspace.sessions, selectedID: workspace.selected?.id,
                                  focusRequest: workspace.focusRequest, hostName: host.name,
                                  optionAsMeta: workspace.preferences.optionAsMeta)
                    if let session = workspace.selected {
                        TerminalOverlay(session: session, workspace: workspace, host: host)
                    }
                }
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                .clipped()
                if let session = workspace.selected, case .exited(let code) = session.phase {
                    ExitedBar(code: code, shell: session.shell,
                              restart: { session.restart() },
                              close: { workspace.requestClose(session) })
                        .layoutPriority(1)
                }
            }
        }
        .overlay(alignment: .bottom) {
            if let notice = workspace.notice {
                TerminalNoticeToast(notice: notice) { workspace.dismissNotice() }
                    .padding(.bottom, DS.Space.xxl + DS.Space.l)
                    .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
                    .task(id: notice.id) {
                        try? await Task.sleep(for: .seconds(8))
                        workspace.dismissNotice()
                    }
            }
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: workspace.notice)
        .alert(String.ui("terminal.close.title", "셸을 종료할까요?"),
               isPresented: Binding(get: { workspace.pendingClose != nil }, set: { if !$0 { workspace.pendingClose = nil } }),
               presenting: workspace.pendingClose) { session in
            Button(String.ui("terminal.close.detach", "분리만 하기")) {
                workspace.close(session, ending: false)
            }
            .keyboardShortcut(.defaultAction)
            Button(String.ui("terminal.close.end", "셸 종료"), role: .destructive) {
                workspace.close(session, ending: true)
            }
            Button(String.ui("action.cancel", "취소"), role: .cancel) {}
        } message: { _ in
            Text(String.ui("terminal.close.message", "원격 프로세스도 끝납니다. 분리만 하면 셸은 \(host.name)에서 계속 실행되고, + 메뉴에서 다시 열 수 있습니다."))
        }
        .sheet(item: $workspace.pendingPaste) { request in
            PasteConfirmSheet(request: request, shell: workspace.sessions.first { $0.id == request.sessionID }?.title ?? "",
                              canSend: workspace.canSendPaste(request)) { dontAsk in
                workspace.confirmPaste(request, dontAskAgain: dontAsk)
            } cancel: {
                workspace.cancelPaste()
            }
        }
    }
}

// MARK: Empty state

/// The tab without terminals: why the computer cannot open one, or the shells to open and the
/// detached shells to reopen.
private struct TerminalEmptyState: View {
    let workspace: TerminalWorkspace
    let host: Host
    let availability: TerminalAvailability

    var body: some View {
        switch availability {
        case .ready:
            GeometryReader { proxy in
                ScrollView {
                    ready
                        .frame(maxWidth: .infinity, minHeight: proxy.size.height)
                }
                .scrollBounceBehavior(.basedOnSize)
            }
        case .connecting, .reconnecting:
            VStack(spacing: DS.Space.m) {
                ProgressView()
                Text(availability == .connecting
                     ? String.ui("terminal.host.connecting", "\(host.name)에 연결하는 중…")
                     : String.ui("terminal.host.reconnecting", "\(host.name)에 다시 연결하는 중…"))
                    .font(DS.Font.headline)
                Text(String.ui("terminal.host.waiting", "연결되면 셸을 열 수 있습니다. 원격 PC에서 실행 중이던 셸은 분리된 셸에서 다시 엽니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 360)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(DS.Space.l)
            .accessibilityElement(children: .combine)
        case .offline:
            EmptyStateView(symbol: "bolt.horizontal.circle", title: String.ui("terminal.host.offline", "연결되어 있지 않습니다"),
                           message: String.ui("terminal.host.offlineMessage", "위 배너의 ‘연결’로 \(host.name)에 연결하면 셸을 열 수 있습니다."))
        case .blocked:
            EmptyStateView(symbol: "lock.trianglebadge.exclamationmark", title: String.ui("terminal.host.blocked", "다시 페어링해야 합니다"),
                           message: String.ui("terminal.host.blockedMessage", "에이전트 인증서가 페어링 때와 달라 연결을 막았습니다. 위 배너에서 다시 페어링한 뒤 셸을 열 수 있습니다."))
        case .unavailable(let state, let reason):
            EmptyStateView(symbol: "terminal",
                           title: state == .unsupported
                               ? String.ui("terminal.host.unsupported", "이 PC는 터미널을 지원하지 않습니다")
                               : String.ui("terminal.host.unavailable", "지금은 터미널을 열 수 없습니다"),
                           message: reason ?? String.ui("terminal.host.unavailableMessage", "에이전트가 터미널 기능이 준비되지 않았다고 알렸습니다. 진단을 실행해 원인을 확인하세요."))
        }
    }

    private var ready: some View {
        VStack(spacing: DS.Space.xl) {
            VStack(spacing: DS.Space.l) {
                EmptyStateView(symbol: "terminal", title: String.ui("terminal.empty.title", "원격 터미널"),
                               message: String.ui("terminal.empty.message", "\(host.name)에서 PowerShell이나 명령 프롬프트를 엽니다. 연결이 끊겨도 셸은 원격 PC에서 계속 실행되고, 다시 연결하면 놓친 출력이 이어집니다."))
                    .padding(.bottom, -DS.Space.l)
                shellButtons
            }
            if workspace.jobsError != nil {
                HStack(spacing: DS.Space.s) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(DS.Color.warning)
                        .accessibilityHidden(true)
                    Text(String.ui("terminal.detached.failedRow", "분리된 셸을 불러오지 못했습니다"))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                    Button(String.ui("action.retry", "다시 시도")) { workspace.refreshJobs() }
                        .controlSize(.small)
                }
                .accessibilityElement(children: .combine)
            } else if !workspace.detachedJobs.isEmpty {
                DetachedShellList(jobs: workspace.detachedJobs) { workspace.reopen($0) }
                    .frame(maxWidth: 560)
            }
        }
        .padding(DS.Space.xl)
    }

    private var shellButtons: some View {
        let shells = TerminalShell.available(on: host)
        let preferred = workspace.preferences.defaultShell
        let primary = shells.contains(preferred) ? preferred : .powershell
        return HStack(spacing: DS.Space.s) {
            Button {
                workspace.open(primary)
            } label: {
                Label(String.ui("terminal.open", "\(primary.title) 열기"), systemImage: primary.symbol)
            }
            .buttonStyle(.borderedProminent)
            .help(String.ui("terminal.open.help", "새 탭에서 \(primary.title) 열기 (⌘T)"))
            ForEach(shells.filter { $0 != primary }) { shell in
                Button {
                    workspace.open(shell)
                } label: {
                    Label(shell.title, systemImage: shell.symbol)
                }
                .buttonStyle(.bordered)
                .help(String.ui("terminal.openOther.help", "새 탭에서 \(shell.title) 열기"))
            }
        }
        .controlSize(.large)
    }
}

/// Running shells no tab shows, each with when and by whom it started and a reopen button.
private struct DetachedShellList: View {
    let jobs: [Job]
    let reopen: (Job) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: DS.Space.xs) {
                Text(String.ui("terminal.detached.title", "분리된 셸"))
                    .font(DS.Font.headline)
                Text("\(jobs.count)")
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                Spacer()
                Text(String.ui("terminal.detached.hint", "원격 PC에서 계속 실행 중"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.s)
            .accessibilityElement(children: .combine)
            Divider()
            ForEach(Array(jobs.enumerated()), id: \.element.jobID) { index, job in
                if index > 0 { Divider().padding(.leading, DS.Space.xxl + DS.Space.m) }
                DetachedShellRow(job: job) { reopen(job) }
            }
        }
        .background(DS.Color.surfaceRaised.opacity(0.6), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.separator, lineWidth: 1)
        )
    }
}

private struct DetachedShellRow: View {
    let job: Job
    let reopen: () -> Void

    var body: some View {
        let shell = TerminalShell(job: job)
        HStack(spacing: DS.Space.m) {
            Image(systemName: shell.symbol)
                .font(.system(size: 16))
                .foregroundStyle(.secondary)
                .frame(width: DS.Space.xl)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(job.summary)
                    .font(DS.Font.body.weight(.medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(TerminalJobText.detail(job))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: DS.Space.m)
            Button(String.ui("terminal.detached.reopen", "다시 열기"), action: reopen)
                .controlSize(.regular)
                .accessibilityLabel(String.ui("terminal.detached.reopen.a11y", "\(job.summary) 다시 열기"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .help(job.summary)
    }
}

/// Text about a pty job for lists and menus.
enum TerminalJobText {
    /// `25분 전 시작 · Claude Code · PID 9216`.
    static func detail(_ job: Job, now: Date = .now) -> String {
        let started = DS.Format.relative(job.createdAt.date, now: now)
        var parts = [String.ui("terminal.job.started", "\(started) 시작")]
        if let client = job.result?["client_name"]?.stringValue { parts.append(client) }
        if let pid = job.result?["pid"]?.intValue { parts.append("PID \(pid)") }
        return parts.joined(separator: " · ")
    }
}

// MARK: Confirmations and notices

/// Confirmation before pasting: the whole text in a scrolling monospaced box with its line and
/// character counts, how many control strings were removed, 다시 묻지 않기, and 붙여넣기 —
/// disabled with a notice when nothing is left to send or the shell cannot receive it.
public struct PasteConfirmSheet: View {
    let request: PasteRequest
    let shell: String
    let canSend: Bool
    let confirm: (Bool) -> Void
    let cancel: () -> Void
    @State private var dontAskAgain = false

    /// Lines the preview shows before it scrolls.
    static let visibleLines = 10

    /// Why 붙여넣기 is disabled.
    enum Blocker: Equatable {
        /// The text held only escape sequences and control characters.
        case nothingToSend
        /// The shell does not accept input.
        case disconnected
    }

    static func blocker(for request: PasteRequest, canSend: Bool) -> Blocker? {
        if request.text.isEmpty { return .nothingToSend }
        return canSend ? nil : .disconnected
    }

    public init(request: PasteRequest, shell: String, canSend: Bool, confirm: @escaping (Bool) -> Void, cancel: @escaping () -> Void) {
        self.request = request
        self.shell = shell
        self.canSend = canSend
        self.confirm = confirm
        self.cancel = cancel
    }

    private var title: String {
        let count = request.lineCount
        if request.removedControls > 0, count <= 1 {
            return .ui("terminal.paste.titleControls", "제어 문자가 있는 텍스트를 붙여 넣을까요?")
        }
        return count > 1
            ? .ui("terminal.paste.title", "\(count)줄을 붙여 넣을까요?")
            : .ui("terminal.paste.titleOne", "줄바꿈이 있는 텍스트를 붙여 넣을까요?")
    }

    public var body: some View {
        let lines = request.lines
        let lineCount = request.lineCount
        let characters = request.characterCount
        let lineHeight = NSFont.monospacedSystemFont(ofSize: NSFont.systemFontSize(for: .small) + 1, weight: .regular).boundingRectForFont.height
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: "doc.on.clipboard")
                    .font(.system(size: 28))
                    .foregroundStyle(DS.Color.warning)
                    .frame(width: 40)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    Text(title)
                        .font(DS.Font.title)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityAddTraits(.isHeader)
                    Text(String.ui("terminal.paste.message", "\(shell)에 붙여 넣으면 줄마다 바로 명령으로 실행될 수 있습니다."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(lines.enumerated()), id: \.offset) { index, line in
                            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                                Text("\(index + 1)")
                                    .foregroundStyle(.tertiary)
                                    .frame(minWidth: 20, alignment: .trailing)
                                Text(line.isEmpty ? " " : line)
                                    .fixedSize(horizontal: false, vertical: true)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                            }
                        }
                    }
                    .font(DS.Font.mono)
                    .textSelection(.enabled)
                    .padding(DS.Space.m)
                }
                .frame(maxHeight: (lineHeight + 2) * CGFloat(Self.visibleLines) + DS.Space.m * 2)
                .fixedSize(horizontal: false, vertical: true)
                .background(DS.Color.terminalBackground, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                        .strokeBorder(DS.Color.separator, lineWidth: 1)
                )
                .accessibilityElement(children: .combine)
                .accessibilityLabel(String.ui("terminal.paste.a11y", "붙여 넣을 텍스트, \(lineCount)줄"))
                HStack(spacing: DS.Space.s) {
                    Text(String.ui("terminal.paste.totals", "\(lineCount)줄 · \(characters)자"))
                        .monospacedDigit()
                    if request.removedControls > 0 {
                        let removed = request.removedControls
                        Label(String.ui("terminal.paste.removed", "제어 문자열 \(removed)개를 지웠습니다"), systemImage: "eraser")
                            .foregroundStyle(DS.Color.warning)
                    }
                }
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
            }
            switch Self.blocker(for: request, canSend: canSend) {
            case .nothingToSend:
                Label(String.ui("terminal.paste.nothing", "붙여 넣을 글자가 없습니다 (제어 문자만 있었습니다)"), systemImage: "text.badge.xmark")
                    .font(DS.Font.body)
                    .foregroundStyle(DS.Color.warning)
            case .disconnected:
                Label(String.ui("terminal.paste.disconnected", "연결이 끊겨 보낼 수 없습니다"), systemImage: "bolt.horizontal.circle")
                    .font(DS.Font.body)
                    .foregroundStyle(DS.Color.warning)
            case nil:
                EmptyView()
            }
            HStack {
                Toggle(String.ui("terminal.paste.dontAsk", "다시 묻지 않기"), isOn: $dontAskAgain)
                    .toggleStyle(.checkbox)
                    .disabled(request.removedControls > 0)
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("terminal.paste.confirm", "붙여넣기")) { confirm(dontAskAgain) }
                    .keyboardShortcut(.defaultAction)
                    .disabled(Self.blocker(for: request, canSend: canSend) != nil)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 560)
    }
}

/// A short message with a close button over the bottom of the terminal tab.
private struct TerminalNoticeToast: View {
    let notice: TerminalNotice
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: "exclamationmark.octagon.fill")
                .foregroundStyle(DS.Color.danger)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(notice.message)
                    .font(DS.Font.body.weight(.semibold))
                if let detail = notice.detail {
                    Text(detail)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let code = notice.code {
                    ErrorCodeLine(code: code)
                }
            }
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 18, height: 18)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("action.close", "닫기"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .frame(maxWidth: 520, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.danger.opacity(0.35), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.14), radius: 10, y: 3)
        .accessibilityElement(children: .contain)
        .onAppear {
            AccessibilityNotification.Announcement(notice.message).post()
        }
    }
}
