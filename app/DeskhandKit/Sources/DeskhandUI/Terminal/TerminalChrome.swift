import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The row over the terminal: the scrolling tabs, the + button pinned after them (click: the
/// default shell; chevron: every shell and the detached shells), and at the trailing edge the
/// terminal size of a shell that attached and the terminal actions menu.
struct TerminalTabStrip: View {
    let workspace: TerminalWorkspace
    let host: Host
    let canOpen: Bool

    static let height: CGFloat = 40
    static let maximumTabWidth: CGFloat = 220
    static let minimumTabWidth: CGFloat = 112

    /// Tabs share the row equally, between `minimumTabWidth` and `maximumTabWidth` each; when
    /// they no longer fit at the minimum the row scrolls.
    static func tabWidth(available: CGFloat, count: Int) -> CGFloat {
        guard count > 0 else { return maximumTabWidth }
        let share = (available - CGFloat(count - 1) * DS.Space.xs) / CGFloat(count)
        return min(maximumTabWidth, max(minimumTabWidth, share.rounded(.down)))
    }

    var body: some View {
        HStack(spacing: DS.Space.s) {
            TerminalTabRow(workspace: workspace)
            NewTabButton(workspace: workspace, host: host)
                .disabled(!canOpen)
            if let selected = workspace.selected {
                if selected.hasAttached {
                    Text(selected.size.text)
                        .font(DS.Font.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .help(String.ui("terminal.size.help", "터미널 크기 (열×행)"))
                        .accessibilityLabel(String.ui("terminal.size.a11y", "터미널 크기 \(selected.size.cols)열 \(selected.size.rows)행"))
                }
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

/// The tabs in a horizontal scroll view that scrolls the selected tab to the center of the
/// row (within the scroll range), with faded edges on the sides that have hidden tabs.
private struct TerminalTabRow: View {
    let workspace: TerminalWorkspace
    @State private var position = ScrollPosition(edge: .leading)
    @State private var overflow = TabStripScroll.Overflow()
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    static let fadeWidth: CGFloat = 24

    var body: some View {
        GeometryReader { proxy in
            let width = TerminalTabStrip.tabWidth(available: proxy.size.width, count: workspace.sessions.count)
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: TabStripScroll.spacing) {
                    ForEach(workspace.sessions) { session in
                        TerminalTab(session: session, isSelected: session.id == workspace.selected?.id,
                                    select: { workspace.select(session) },
                                    close: { workspace.requestClose(session) },
                                    detach: { workspace.close(session, ending: false) },
                                    clear: { session.clearScrollback() })
                            .frame(width: width)
                    }
                }
                .frame(height: proxy.size.height)
            }
            .scrollPosition($position)
            .onScrollGeometryChange(for: TabStripScroll.Overflow.self) { geometry in
                TabStripScroll.overflow(contentWidth: geometry.contentSize.width, visibleWidth: geometry.containerSize.width,
                                        offset: geometry.contentOffset.x)
            } action: { _, new in
                overflow = new
            }
            .mask(fade)
            .onAppear {
                position.scrollTo(x: target(tabWidth: width, visibleWidth: proxy.size.width))
            }
            .onChange(of: workspace.selected?.id) {
                withAnimation(reduceMotion ? nil : .snappy(duration: 0.2)) {
                    position.scrollTo(x: target(tabWidth: width, visibleWidth: proxy.size.width))
                }
            }
        }
    }

    private func target(tabWidth: CGFloat, visibleWidth: CGFloat) -> CGFloat {
        let index = workspace.sessions.firstIndex { $0.id == workspace.selected?.id } ?? 0
        return TabStripScroll.targetOffset(selected: index, count: workspace.sessions.count, tabWidth: tabWidth,
                                           spacing: TabStripScroll.spacing, visibleWidth: visibleWidth)
    }

    private var fade: some View {
        HStack(spacing: 0) {
            LinearGradient(colors: [.clear, .black], startPoint: .leading, endPoint: .trailing)
                .frame(width: overflow.leading ? Self.fadeWidth : 0)
            Rectangle().fill(.black)
            LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing)
                .frame(width: overflow.trailing ? Self.fadeWidth : 0)
        }
    }
}

/// Scroll geometry of the tab row: which edges have hidden tabs, and the offset that centers
/// the selected tab within the scroll range.
enum TabStripScroll {
    /// Space between tabs.
    static let spacing: CGFloat = DS.Space.xs

    struct Overflow: Equatable {
        var leading = false
        var trailing = false
    }

    /// Hidden tabs before and after the visible part of a row `contentWidth` wide, shown
    /// `visibleWidth` wide from `offset`.
    static func overflow(contentWidth: CGFloat, visibleWidth: CGFloat, offset: CGFloat) -> Overflow {
        Overflow(leading: offset > 1, trailing: offset + visibleWidth < contentWidth - 1)
    }

    /// The offset that puts tab `selected` of `count` in the middle of the visible width,
    /// clamped to the scroll range.
    static func targetOffset(selected: Int, count: Int, tabWidth: CGFloat, spacing: CGFloat, visibleWidth: CGFloat) -> CGFloat {
        guard count > 0 else { return 0 }
        let content = CGFloat(count) * tabWidth + CGFloat(count - 1) * spacing
        let center = CGFloat(selected) * (tabWidth + spacing) + tabWidth / 2
        return min(max(0, center - visibleWidth / 2), max(0, content - visibleWidth))
    }
}

/// One shell tab: its state or shell symbol, the working directory's last folder (or the
/// title when the shell reports no folder) with the title as a smaller second label while it
/// fits, and a close button shown on hover and on the selected tab. The tooltip has the full path.
private struct TerminalTab: View {
    let session: TerminalSession
    let isSelected: Bool
    let select: () -> Void
    let close: () -> Void
    let detach: () -> Void
    let clear: () -> Void
    @State private var hovering = false

    private var primary: String { session.directoryName ?? session.title }
    private var secondary: String? { session.directoryName == nil ? nil : session.title }

    private var label: String {
        guard let secondary else { return primary }
        return "\(primary), \(secondary)"
    }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Button(action: select) {
                HStack(spacing: 6) {
                    TerminalStatusIcon(session: session)
                        .frame(width: 14, height: 14)
                    ViewThatFits(in: .horizontal) {
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            primaryText
                            if let secondary {
                                Text(secondary)
                                    .font(DS.Font.caption)
                                    .foregroundStyle(.tertiary)
                            }
                        }
                        .fixedSize()
                        .frame(maxWidth: .infinity, alignment: .leading)
                        primaryText
                            .truncationMode(.tail)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .lineLimit(1)
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
        .help([session.directory, session.title].compactMap { $0 }.joined(separator: "\n"))
        .contextMenu {
            Button(String.ui("terminal.tab.closeMenu", "탭 닫기"), action: close)
            if session.isShellRunning {
                Button(String.ui("terminal.detach", "분리하기"), action: detach)
            }
            Divider()
            Button(String.ui("terminal.clearScrollback", "스크롤백 지우기"), action: clear)
        }
    }

    private var primaryText: some View {
        Text(primary)
            .font(isSelected ? DS.Font.body.weight(.medium) : DS.Font.body)
            .foregroundStyle(isSelected ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
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

/// The + button and its chevron in one hover container: + opens the default shell, the
/// chevron lists every shell of the computer and the detached shells, or a retry row when
/// they could not be loaded.
struct NewTabButton: View {
    let workspace: TerminalWorkspace
    let host: Host
    @State private var hovering = false
    @Environment(\.isEnabled) private var isEnabled

    var body: some View {
        let shells = TerminalShell.available(on: host)
        let preferred = shells.contains(workspace.preferences.defaultShell) ? workspace.preferences.defaultShell : .powershell
        HStack(spacing: 0) {
            Button {
                workspace.open(preferred)
            } label: {
                Image(systemName: "plus")
                    .font(.system(size: 12, weight: .semibold))
                    .frame(width: 26, height: 24)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(String.ui("terminal.new.help", "새 \(preferred.title) 탭 (⌘T)"))
            .accessibilityLabel(String.ui("terminal.new.a11y", "새 터미널 탭"))
            Rectangle()
                .fill(DS.Color.separator)
                .frame(width: 1, height: 14)
                .opacity(hovering ? 1 : 0)
            Menu {
                NewTabMenuItems(workspace: workspace, shells: shells, preferred: preferred)
            } label: {
                Image(systemName: "chevron.down")
                    .font(.system(size: 9, weight: .bold))
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .frame(width: 20, height: 24)
            .help(String.ui("terminal.new.menu.help", "셸 고르기와 분리된 셸"))
            .accessibilityLabel(String.ui("terminal.new.menu.a11y", "셸 고르기"))
        }
        .foregroundStyle(isEnabled ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tertiary))
        .background(hovering && isEnabled ? DS.Color.neutral.opacity(0.12) : .clear,
                    in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .contentShape(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .onHover { hovering = $0 }
        .fixedSize()
    }
}

/// Items of the new-tab menu: the shells, then the detached shells or the retry row.
struct NewTabMenuItems: View {
    let workspace: TerminalWorkspace
    let shells: [TerminalShell]
    let preferred: TerminalShell

    var body: some View {
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
        if workspace.jobsError != nil {
            Section(String.ui("terminal.detached.title", "분리된 셸")) {
                Button(String.ui("terminal.detached.failed", "분리된 셸을 불러오지 못했습니다 · 다시 시도")) {
                    workspace.refreshJobs()
                }
            }
        } else if !workspace.detachedJobs.isEmpty {
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
            TerminalSettingToggles(preferences: preferences)
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

/// The terminal settings that menus toggle: the paste confirmation, Option as Meta and remote
/// clipboard writes.
struct TerminalSettingToggles: View {
    @Bindable var preferences: TerminalPreferences

    var body: some View {
        Toggle(String.ui("terminal.paste.confirmSetting", "여러 줄을 붙여 넣기 전에 묻기"), isOn: $preferences.confirmsMultilinePaste)
        Toggle(String.ui("terminal.optionAsMeta", "Option을 Meta 키로 쓰기"), isOn: $preferences.optionAsMeta)
        Toggle(String.ui("terminal.clipboardWrite", "원격 프로그램의 클립보드 쓰기 허용하기"), isOn: $preferences.allowsClipboardWrite)
    }
}

// MARK: States around the terminal

/// What the selected shell shows over its output while it opens, attaches for the first time
/// or has failed.
struct TerminalOverlay: View {
    let session: TerminalSession
    let workspace: TerminalWorkspace
    let host: Host

    private var hostConnected: Bool { host.connection == .ready || host.connection == .degraded }

    var body: some View {
        switch session.phase {
        case .idle, .opening:
            TerminalProgressCard(title: String.ui("terminal.opening", "\(session.shell.title) 여는 중…"),
                                 detail: host.name)
        case .attaching:
            if session.hasOutput {
                Color.clear
                    .allowsHitTesting(false)
            } else {
                TerminalProgressCard(title: String.ui("terminal.attaching", "셸에 연결하는 중…"),
                                     detail: session.jobID == nil
                                         ? host.name
                                         : String.ui("terminal.attaching.replay", "\(host.name) · 놓친 출력을 함께 가져옵니다"))
            }
        case .live, .reconnecting, .exited:
            Color.clear
                .allowsHitTesting(false)
        case .failed(let error):
            ZStack {
                if session.hasOutput {
                    DS.Color.terminalBackground.opacity(0.7)
                }
                FailureCard(session: session, error: error, hostConnected: hostConnected,
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

/// The bar over the terminal while the shell's stream is attaching again and the computer is
/// connected: what happened, when the next attempt starts and 다시 연결 to try now.
struct StreamReconnectBar: View {
    let session: TerminalSession
    let error: OperationError?
    let hostConnected: Bool

    var body: some View {
        HStack(spacing: DS.Space.s) {
            ProgressView()
                .controlSize(.small)
            Text(String.ui("terminal.reconnect.title", "셸 연결이 끊겼습니다"))
                .font(DS.Font.body.weight(.semibold))
                .layoutPriority(1)
            Text(error?.message ?? String.ui("terminal.reconnect.message", "셸은 원격 PC에서 계속 실행 중이며, 다시 붙으면 놓친 출력이 이어집니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: DS.Space.s)
            if let next = session.nextAttempt {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    let seconds = max(0, Int(next.timeIntervalSince(context.date).rounded(.up)))
                    Text(seconds > 0
                         ? String.ui("terminal.reconnect.next", "\(seconds)초 후 다시 시도")
                         : String.ui("terminal.reconnect.trying", "다시 시도하는 중…"))
                        .font(DS.Font.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
            Button(String.ui("terminal.reconnect.now", "다시 연결")) { session.reconnect() }
                .controlSize(.small)
                .disabled(!hostConnected)
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: 32)
        .background(DS.Color.warning.opacity(0.12))
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .onAppear {
            AccessibilityNotification.Announcement(String.ui("terminal.reconnect.title", "셸 연결이 끊겼습니다")).post()
        }
    }
}

/// The bar under the output of a shell that ended: the shell and its exit code, 탭 닫기 and 새로 시작.
struct ExitedBar: View {
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
                Text(String.ui("terminal.exited.title", "셸이 종료되었습니다"))
                    .font(DS.Font.body.weight(.semibold))
                Text(String.ui("terminal.exited.code", "\(shell.title) · 코드 \(code)"))
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
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s + 2)
        .background(DS.Color.surface)
        .overlay(alignment: .top) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
        .onAppear {
            AccessibilityNotification.Announcement(String.ui("terminal.exited.title", "셸이 종료되었습니다")).post()
        }
    }
}

/// Why the shell could not be opened or attached again, with the next steps: attach again or
/// open a new shell, PowerShell instead of a shell the computer cannot start, or close the tab.
/// Retrying waits while the computer is not connected.
private struct FailureCard: View {
    let session: TerminalSession
    let error: OperationError
    let hostConnected: Bool
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
                         : String.ui("terminal.failed.open", "\(session.shell.title) 셸을 열지 못했습니다"))
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
                        .disabled(!hostConnected)
                    if error.code != "NOT_FOUND" {
                        Button(String.ui("action.retry", "다시 시도"), action: retry)
                            .disabled(!hostConnected)
                    }
                } else if shellUnavailable {
                    Button(String.ui("terminal.failed.openPowerShell", "PowerShell 열기"), action: openPowerShell)
                        .buttonStyle(.borderedProminent)
                        .disabled(!hostConnected)
                } else {
                    Button(String.ui("action.retry", "다시 시도"), action: retry)
                        .buttonStyle(.borderedProminent)
                        .disabled(!hostConnected)
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

