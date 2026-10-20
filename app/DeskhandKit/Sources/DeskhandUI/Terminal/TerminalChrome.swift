import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The row over the terminal: a tab per shell, the + button (click: the default shell; menu:
/// every shell and the detached shells), and at the trailing edge the terminal size and the
/// terminal actions menu.
struct TerminalTabStrip: View {
    let workspace: TerminalWorkspace
    let host: Host
    let canOpen: Bool

    static let height: CGFloat = 40
    /// Width the + menu takes after the tabs.
    private static let newTabWidth: CGFloat = 48

    /// Tabs share the strip equally, between 132 and 240 points each; narrower strips scroll.
    static func tabWidth(available: CGFloat, count: Int) -> CGFloat {
        guard count > 0 else { return 240 }
        let share = (available - newTabWidth) / CGFloat(count) - DS.Space.xs
        return min(240, max(132, share.rounded(.down)))
    }

    var body: some View {
        HStack(spacing: DS.Space.s) {
            GeometryReader { proxy in
                let width = Self.tabWidth(available: proxy.size.width, count: workspace.sessions.count)
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: DS.Space.xs) {
                        ForEach(workspace.sessions) { session in
                            TerminalTab(session: session, isSelected: session.id == workspace.selected?.id,
                                        select: { workspace.select(session) },
                                        close: { workspace.requestClose(session) },
                                        detach: { workspace.close(session, ending: false) },
                                        clear: { session.clearScrollback() })
                                .frame(width: width)
                        }
                        NewTerminalMenu(workspace: workspace, host: host)
                            .disabled(!canOpen)
                            .padding(.leading, DS.Space.xs)
                    }
                    .frame(height: proxy.size.height)
                }
            }
            if let selected = workspace.selected {
                Text(selected.size.text)
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .help(String.ui("terminal.size.help", "터미널 크기 (열×행)"))
                    .accessibilityLabel(String.ui("terminal.size.a11y", "터미널 크기 \(selected.size.cols)열 \(selected.size.rows)행"))
                TerminalActionsMenu(workspace: workspace, session: selected)
            }
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: Self.height)
        .background(DS.Color.surface)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("terminal.tabs.a11y", "터미널 탭"))
    }
}

/// One shell tab: its state, title and working directory, and a close button shown on hover
/// and on the selected tab.
private struct TerminalTab: View {
    let session: TerminalSession
    let isSelected: Bool
    let select: () -> Void
    let close: () -> Void
    let detach: () -> Void
    let clear: () -> Void
    @State private var hovering = false

    private var label: String {
        guard let directory = session.directoryName else { return session.title }
        return "\(session.title) — \(directory)"
    }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Button(action: select) {
                HStack(spacing: 6) {
                    TerminalStatusIcon(session: session)
                        .frame(width: 14, height: 14)
                    HStack(spacing: DS.Space.xs) {
                        Text(session.title)
                            .font(isSelected ? DS.Font.body.weight(.medium) : DS.Font.body)
                            .foregroundStyle(isSelected ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
                            .layoutPriority(1)
                        if let directory = session.directoryName {
                            Text("— \(directory)")
                                .font(DS.Font.body)
                                .foregroundStyle(isSelected ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tertiary))
                        }
                    }
                    .lineLimit(1)
                    .truncationMode(.tail)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("terminal.tab.a11y", "\(label), \(TerminalStatusIcon.stateText(session.phase))"))
            .accessibilityAddTraits(isSelected ? [.isSelected] : [])
            Button(action: close) {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 16, height: 16)
                    .background(hovering ? DS.Color.neutral.opacity(0.18) : .clear, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .opacity(hovering || isSelected ? 1 : 0)
            .help(String.ui("terminal.tab.close.help", "탭 닫기 (⌘W)"))
            .accessibilityLabel(String.ui("terminal.tab.close", "\(label) 탭 닫기"))
        }
        .padding(.leading, 10)
        .padding(.trailing, DS.Space.xs)
        .frame(height: 28)
        .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .overlay {
            if isSelected {
                RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                    .strokeBorder(DS.Color.separator, lineWidth: 1)
            }
        }
        .onHover { hovering = $0 }
        .help([label, session.directory].compactMap { $0 }.joined(separator: "\n"))
        .contextMenu {
            Button(String.ui("terminal.tab.closeMenu", "탭 닫기"), action: close)
            if session.isShellRunning {
                Button(String.ui("terminal.detach", "분리하기"), action: detach)
            }
            Divider()
            Button(String.ui("terminal.clearScrollback", "스크롤백 지우기"), action: clear)
        }
    }

    private var background: AnyShapeStyle {
        if isSelected { return AnyShapeStyle(DS.Color.terminalBackground) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.12)) : AnyShapeStyle(.clear)
    }
}

/// The state of a shell as a small mark: a spinner while opening, the shell's symbol while
/// attached, and a warning, stop or failure mark otherwise.
struct TerminalStatusIcon: View {
    let session: TerminalSession

    var body: some View {
        switch session.phase {
        case .idle, .opening, .attaching:
            ProgressView()
                .controlSize(.mini)
        case .live:
            Image(systemName: session.shell.symbol)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
        case .reconnecting:
            Image(systemName: "bolt.horizontal.circle.fill")
                .font(.system(size: 11))
                .foregroundStyle(DS.Color.warning)
        case .exited(let code):
            Image(systemName: code == 0 ? "checkmark.circle" : "xmark.circle")
                .font(.system(size: 11))
                .foregroundStyle(code == 0 ? AnyShapeStyle(.secondary) : AnyShapeStyle(DS.Color.warning))
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 11))
                .foregroundStyle(DS.Color.danger)
        }
    }

    static func stateText(_ phase: TerminalSession.Phase) -> String {
        switch phase {
        case .idle, .opening: .ui("terminal.state.opening", "여는 중")
        case .attaching: .ui("terminal.state.attaching", "연결하는 중")
        case .live: .ui("terminal.state.live", "연결됨")
        case .reconnecting: .ui("terminal.state.reconnecting", "다시 연결하는 중")
        case .exited(let code): .ui("terminal.state.exited", "종료됨, 코드 \(code)")
        case .failed: .ui("terminal.state.failed", "열지 못함")
        }
    }
}

/// The + button: a click opens the default shell, the menu lists every shell of the computer
/// and the detached shells.
struct NewTerminalMenu: View {
    let workspace: TerminalWorkspace
    let host: Host

    var body: some View {
        let shells = TerminalShell.available(on: host)
        let preferred = shells.contains(workspace.preferences.defaultShell) ? workspace.preferences.defaultShell : .powershell
        Menu {
            Section(String.ui("terminal.new.section", "새 탭")) {
                ForEach(shells) { shell in
                    Button {
                        workspace.open(shell)
                    } label: {
                        Label(shell == preferred ? String.ui("terminal.new.default", "\(shell.title) (기본)") : shell.title,
                              systemImage: shell.symbol)
                    }
                }
            }
            if !workspace.detachedJobs.isEmpty {
                Section(String.ui("terminal.detached.title", "분리된 셸")) {
                    ForEach(workspace.detachedJobs, id: \.jobID) { job in
                        Button {
                            workspace.reopen(job)
                        } label: {
                            Label(job.summary, systemImage: TerminalShell(job: job).symbol)
                            Text(TerminalJobText.detail(job))
                        }
                    }
                }
            }
        } label: {
            Image(systemName: "plus")
                .font(.system(size: 12, weight: .semibold))
        } primaryAction: {
            workspace.open(preferred)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.visible)
        .fixedSize()
        .help(String.ui("terminal.new.help", "새 \(preferred.title) 탭 (⌘T) · 화살표를 누르면 셸과 분리된 셸을 고릅니다"))
        .accessibilityLabel(String.ui("terminal.new.a11y", "새 터미널 탭"))
    }
}

/// Actions on the selected shell and the terminal settings.
private struct TerminalActionsMenu: View {
    @Bindable var workspace: TerminalWorkspace
    let session: TerminalSession

    var body: some View {
        @Bindable var preferences = workspace.preferences
        Menu {
            Button(String.ui("terminal.clearScrollback", "스크롤백 지우기")) { session.clearScrollback() }
            Divider()
            Button(String.ui("terminal.font.bigger", "글자 크게")) { workspace.adjustFontSize(by: 1) }
            Button(String.ui("terminal.font.smaller", "글자 작게")) { workspace.adjustFontSize(by: -1) }
            Button(String.ui("terminal.font.reset", "기본 크기")) { workspace.resetFontSize() }
            Divider()
            Toggle(String.ui("terminal.paste.confirmSetting", "여러 줄을 붙여 넣기 전에 확인"), isOn: $preferences.confirmsMultilinePaste)
            Divider()
            Button(String.ui("terminal.detach", "분리하기")) { workspace.close(session, ending: false) }
                .disabled(!session.isShellRunning)
            Button(String.ui("terminal.end", "셸 종료…")) { workspace.pendingClose = session }
                .disabled(!session.isShellRunning)
        } label: {
            Image(systemName: "ellipsis.circle")
                .font(.system(size: 13))
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(String.ui("terminal.actions.help", "터미널 동작"))
        .accessibilityLabel(String.ui("terminal.actions.help", "터미널 동작"))
    }
}

// MARK: States over the terminal

/// What the selected shell shows over its output: opening, reconnecting, exited or failed.
struct TerminalOverlay: View {
    let session: TerminalSession
    let workspace: TerminalWorkspace
    let host: Host

    var body: some View {
        switch session.phase {
        case .idle, .opening:
            TerminalProgressCard(title: String.ui("terminal.opening", "\(session.shell.title) 여는 중…"),
                                 detail: String.ui("terminal.opening.detail", "\(host.name) · \(session.size.text)"))
        case .attaching:
            if session.hasOutput {
                Color.clear
            } else {
                TerminalProgressCard(title: String.ui("terminal.attaching", "셸에 연결하는 중…"),
                                     detail: session.jobID == nil
                                         ? host.name
                                         : String.ui("terminal.attaching.replay", "\(host.name) · 놓친 출력을 함께 가져옵니다"))
            }
        case .live:
            Color.clear
                .allowsHitTesting(false)
        case .reconnecting(let error):
            ZStack(alignment: .top) {
                DS.Color.terminalBackground.opacity(0.45)
                    .allowsHitTesting(false)
                ReconnectBanner(session: session, host: host, error: error)
                    .padding(.top, DS.Space.l)
                    .padding(.horizontal, DS.Space.l)
            }
        case .exited(let code):
            VStack {
                Spacer()
                ExitedBar(code: code, shell: session.shell,
                          restart: { session.restart() },
                          close: { workspace.requestClose(session) })
            }
        case .failed(let error):
            ZStack {
                if session.hasOutput {
                    DS.Color.terminalBackground.opacity(0.7)
                }
                FailureCard(session: session, error: error,
                            retry: { session.hasOutput ? session.reconnect() : session.restart() },
                            openNew: { session.restart() },
                            openPowerShell: { workspace.replace(session, with: .powershell) },
                            close: { workspace.requestClose(session) })
                    .padding(DS.Space.xl)
            }
        }
    }
}

/// A spinner with a title and a detail line, centered on the empty terminal.
private struct TerminalProgressCard: View {
    let title: String
    let detail: String

    var body: some View {
        VStack(spacing: DS.Space.m) {
            ProgressView()
                .controlSize(.regular)
            VStack(spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.headline)
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(DS.Space.xl)
        .accessibilityElement(children: .combine)
    }
}

/// The notice while the stream is gone: what happened, when the next attempt starts and
/// 다시 연결 to try now.
private struct ReconnectBanner: View {
    let session: TerminalSession
    let host: Host
    let error: OperationError?

    private var hostDown: Bool { host.connection != .ready && host.connection != .degraded }

    private var message: String {
        if hostDown {
            return .ui("terminal.reconnect.hostDown", "\(host.name) 연결이 끊겼습니다. 다시 연결되면 셸에 자동으로 다시 붙고 놓친 출력을 보여 줍니다.")
        }
        if let error, error.code != "OFFLINE" {
            return error.message
        }
        return .ui("terminal.reconnect.message", "셸은 원격 PC에서 계속 실행 중입니다. 다시 붙으면 놓친 출력이 이어집니다.")
    }

    var body: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            ProgressView()
                .controlSize(.small)
            VStack(alignment: .leading, spacing: 2) {
                Text(String.ui("terminal.reconnect.title", "연결이 끊겨 다시 연결하는 중…"))
                    .font(DS.Font.body.weight(.semibold))
                Text(message)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let next = session.nextAttempt {
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        let seconds = max(0, Int(next.timeIntervalSince(context.date).rounded(.up)))
                        Text(seconds > 0
                             ? String.ui("terminal.reconnect.next", "\(seconds)초 후 다시 시도합니다")
                             : String.ui("terminal.reconnect.trying", "다시 시도하는 중…"))
                            .font(DS.Font.caption.monospacedDigit())
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            Spacer(minLength: DS.Space.m)
            Button(String.ui("terminal.reconnect.now", "다시 연결")) { session.reconnect() }
                .controlSize(.regular)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s + 2)
        .frame(maxWidth: 560)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.warning.opacity(0.4), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.12), radius: 8, y: 2)
        .accessibilityElement(children: .contain)
        .onAppear {
            AccessibilityNotification.Announcement(String.ui("terminal.reconnect.title", "연결이 끊겨 다시 연결하는 중…")).post()
        }
    }
}

/// The bar under the output of a shell that ended: its exit code, 새로 시작 and 탭 닫기.
private struct ExitedBar: View {
    let code: Int
    let shell: TerminalShell
    let restart: () -> Void
    let close: () -> Void

    var body: some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: code == 0 ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                .font(.system(size: 16))
                .foregroundStyle(code == 0 ? DS.Color.success : DS.Color.warning)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(String.ui("terminal.exited.title", "\(shell.title)이(가) 종료되었습니다"))
                    .font(DS.Font.body.weight(.semibold))
                Text(String.ui("terminal.exited.code", "종료 코드 \(code)"))
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            Spacer()
            Button(String.ui("terminal.tab.closeMenu", "탭 닫기"), action: close)
            Button(String.ui("terminal.restart", "새로 시작"), action: restart)
                .buttonStyle(.borderedProminent)
        }
        .controlSize(.regular)
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.s + 2)
        .background(.bar)
        .overlay(alignment: .top) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
        .onAppear {
            AccessibilityNotification.Announcement(String.ui("terminal.exited.title", "\(shell.title)이(가) 종료되었습니다")).post()
        }
    }
}

/// Why the shell could not be opened or attached again, with the next steps: attach again or
/// open a new shell, PowerShell instead of a shell the computer cannot start, or close the tab.
private struct FailureCard: View {
    let session: TerminalSession
    let error: OperationError
    let retry: () -> Void
    let openNew: () -> Void
    let openPowerShell: () -> Void
    let close: () -> Void

    /// True when the shell ran in this tab before and its job cannot be attached any more.
    private var lostJob: Bool { session.hasOutput || error.code == "NOT_FOUND" }

    /// True when opening the same shell again cannot work.
    private var shellUnavailable: Bool {
        !lostJob && session.shell != .powershell && TerminalSession.isPermanent(error)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
                Image(systemName: "exclamationmark.octagon.fill")
                    .font(DS.Font.headline)
                    .foregroundStyle(DS.Color.danger)
                    .frame(width: DS.Space.l)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(lostJob
                         ? String.ui("terminal.failed.reattach", "셸에 다시 연결하지 못했습니다")
                         : String.ui("terminal.failed.open", "\(session.shell.title)을(를) 열지 못했습니다"))
                        .font(DS.Font.headline)
                    Text(error.message)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                    if let fix = error.fix {
                        Text(fix)
                            .font(DS.Font.body)
                            .foregroundStyle(.secondary)
                    }
                    if let code = error.code {
                        ErrorCodeLine(code: code)
                            .padding(.top, DS.Space.xs)
                    }
                }
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityElement(children: .combine)
            }
            HStack(spacing: DS.Space.s) {
                if lostJob {
                    Button(String.ui("terminal.failed.openNew", "새 셸 열기"), action: openNew)
                        .buttonStyle(.borderedProminent)
                    if error.code != "NOT_FOUND" {
                        Button(String.ui("action.retry", "다시 시도"), action: retry)
                    }
                } else if shellUnavailable {
                    Button(String.ui("terminal.failed.openPowerShell", "PowerShell 열기"), action: openPowerShell)
                        .buttonStyle(.borderedProminent)
                } else {
                    Button(String.ui("action.retry", "다시 시도"), action: retry)
                        .buttonStyle(.borderedProminent)
                }
                Button(String.ui("terminal.tab.closeMenu", "탭 닫기"), action: close)
            }
            .controlSize(.regular)
            .padding(.leading, DS.Space.l + DS.Space.m)
        }
        .padding(DS.Space.l)
        .frame(maxWidth: 480, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous)
                .strokeBorder(DS.Color.danger.opacity(0.3), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.1), radius: 12, y: 4)
    }
}
