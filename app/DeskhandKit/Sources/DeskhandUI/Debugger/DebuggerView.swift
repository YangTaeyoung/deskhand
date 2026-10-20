import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The debugger tab: a toolbar with resume and step controls and the exception setting; sessions
/// and their targets over the breakpoints on the left; the paused location over the console in
/// the middle; the call stack over the variables on the right. Without a session it offers to
/// launch an Electron app or attach to a running one.
public struct DebuggerView: View {
    let store: AppStore
    let host: Host
    let chrome: WorkspaceChrome

    public init(store: AppStore, host: Host, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.host = host
        self.chrome = chrome
    }

    private var availability: HostFeatureAvailability { HostFeatureAvailability(host: host, capability: "tunnel") }

    public var body: some View {
        let model = store.debugger(for: host)
        Group {
            if availability == .ready || !model.sessions.isEmpty {
                DebuggerContent(store: store, model: model, host: host, availability: availability)
            } else {
                HostFeatureUnavailableView(host: host, availability: availability, feature: .debugger)
                    .background(DS.Color.surface)
            }
        }
        .onAppear { model.start() }
    }
}

private struct DebuggerContent: View {
    let store: AppStore
    @Bindable var model: DebuggerModel
    let host: Host
    let availability: HostFeatureAvailability
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(spacing: 0) {
            if !model.sessions.isEmpty || model.starting != nil {
                DebuggerToolbar(model: model, session: model.selected, enabled: availability == .ready)
            }
            if availability != .ready {
                OfflineStrip(host: host, availability: availability)
            }
            main
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(DS.Color.surface)
        .sheet(item: $model.sheet) { sheet in
            switch sheet {
            case .launch:
                LaunchSheet(model: model, host: host, files: store.backend) { model.sheet = nil }
            case .attach:
                AttachSheet(model: model, host: host, processes: store.backend) { model.sheet = nil }
            case .breakpoint:
                if let session = model.selected {
                    BreakpointSheet(session: session) { model.sheet = nil }
                }
            }
        }
        .confirmationDialog(closeTitle, isPresented: Binding(get: { model.pendingClose != nil }, set: { if !$0 { model.pendingClose = nil } }),
                            titleVisibility: .visible, presenting: model.pendingClose.flatMap(model.session)) { session in
            if session.info.sessionKind == .launch {
                Button(String.ui("debugger.close.kill", "세션 닫고 앱 종료"), role: .destructive) {
                    Task { await session.close(keepProcess: false) }
                }
                Button(String.ui("debugger.close.keep", "앱은 두고 세션만 닫기")) {
                    Task { await session.close(keepProcess: true) }
                }
            } else {
                Button(String.ui("debugger.close.detach", "연결 끊기")) {
                    Task { await session.close(keepProcess: true) }
                }
            }
            Button(String.ui("action.cancel", "취소"), role: .cancel) {}
        } message: { session in
            Text(closeMessage(session))
        }
    }

    private var closeTitle: String {
        guard let session = model.pendingClose.flatMap(model.session) else { return "" }
        let name = session.info.name
        return .ui("debugger.close.title", "‘\(name)’ 세션을 닫을까요?")
    }

    private func closeMessage(_ session: DebugSessionModel) -> String {
        let name = session.info.name
        if session.info.sessionKind == .launch {
            if let pid = session.info.pid {
                let number = String(pid)
                return .ui("debugger.close.launchMessage", "Deskhand가 실행한 \(name)(PID \(number))도 함께 종료합니다. 앱을 두려면 ‘앱은 두고 세션만 닫기’를 고르세요.")
            }
            return .ui("debugger.close.launchMessageNoPID", "Deskhand가 실행한 \(name)도 함께 종료합니다. 앱을 두려면 ‘앱은 두고 세션만 닫기’를 고르세요.")
        }
        return .ui("debugger.close.attachMessage", "\(name) 앱은 계속 실행되고 Deskhand가 연 연결과 터널만 닫힙니다.")
    }

    @ViewBuilder
    private var main: some View {
        if model.sessions.isEmpty {
            switch model.listPhase {
            case .idle, .loading:
                DebugLoadingLine(text: String.ui("debugger.loading", "디버그 세션을 불러오는 중…"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed(let error):
                VStack {
                    ErrorBanner(message: String.ui("debugger.listFailed", "디버그 세션 목록을 불러오지 못했습니다"),
                                fix: error.detail, code: error.code) {
                        Task { await model.reload() }
                    }
                    .frame(maxWidth: 520)
                }
                .padding(DS.Space.xl)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .loaded:
                DebuggerIdleView(model: model, host: host, enabled: availability == .ready)
            }
        } else {
            workspace
        }
    }

    private var workspace: some View {
        GeometryReader { proxy in
            let width = proxy.size.width
            let narrow = width < 1000
            let navigator = narrow ? 208 : min(max(width * 0.22, 224), 280)
            let inspector = narrow ? 244 : min(max(width * 0.25, 248), 320)
            HStack(spacing: 0) {
                DebugSplit(axis: .vertical, fraction: $model.navigatorSplit, minimumFirst: 140, minimumSecond: 120) {
                    TargetsList(model: model, launch: { model.sheet = .launch }, attach: { model.sheet = .attach })
                } second: {
                    BreakpointsList(session: model.selected) { model.sheet = .breakpoint }
                }
                .frame(width: navigator)
                .background(DS.Color.surfaceRaised.opacity(0.35))
                .clipped()
                Divider()
                DebugSplit(axis: .vertical, fraction: $model.centerSplit, minimumFirst: 160, minimumSecond: 150) {
                    SourcePanel(session: model.selected)
                } second: {
                    DebugConsoleView(session: model.selected)
                }
                .frame(minWidth: 0, maxWidth: .infinity)
                .clipped()
                Divider()
                DebugSplit(axis: .vertical, fraction: $model.inspectorSplit, minimumFirst: 120, minimumSecond: 140) {
                    CallStackView(session: model.selected)
                } second: {
                    VariablesView(session: model.selected)
                }
                .frame(width: inspector)
                .clipped()
            }
            .overlay(alignment: .bottom) {
                if let session = model.selected, let notice = session.notice {
                    DiagnosticsNoticeToast(notice: notice) { session.dismissNotice() }
                        .padding(.horizontal, DS.Space.l)
                        .padding(.bottom, DS.Space.m)
                        .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
                }
            }
            .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: model.selected?.notice)
        }
    }
}

/// The computer went offline or is reconnecting while sessions are listed.
private struct OfflineStrip: View {
    let host: Host
    let availability: HostFeatureAvailability

    var body: some View {
        HStack(spacing: DS.Space.s) {
            if availability == .connecting || availability == .reconnecting {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: "bolt.horizontal.circle")
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
            }
            Text(message)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.warning.opacity(0.08))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
    }

    private var message: String {
        let name = host.name
        switch availability {
        case .connecting, .reconnecting:
            return .ui("debugger.offline.reconnecting", "\(name)에 다시 연결하는 중입니다. 연결이 돌아오면 세션 상태를 다시 읽습니다.")
        default:
            return .ui("debugger.offline.strip", "\(name)에 연결되어 있지 않아 실행 제어와 평가를 쓸 수 없습니다. 이전 세션 기록만 볼 수 있습니다.")
        }
    }
}

// MARK: Toolbar

/// 일시정지·재개 (F8), 건너뛰기 (F10), 들어가기 (F11), 나오기 (⇧F11), the session's state, the
/// exception setting and closing the session.
struct DebuggerToolbar: View {
    let model: DebuggerModel
    let session: DebugSessionModel?
    let enabled: Bool

    var body: some View {
        let canControl = enabled && (session?.canControl ?? false) && session?.command == nil
        let paused = session?.pauseState != nil
        HStack(spacing: DS.Space.s) {
            HStack(spacing: 2) {
                ControlButton(symbol: paused ? "play.fill" : "pause.fill",
                              title: paused ? String.ui("debugger.resume", "재개") : String.ui("debugger.pause", "일시정지"),
                              shortcut: "F8", tint: paused ? DS.Color.success : nil, enabled: canControl) {
                    session?.togglePause()
                }
                ControlButton(symbol: "arrow.uturn.forward", title: String.ui("debugger.stepOver", "건너뛰기"), shortcut: "F10",
                              enabled: canControl && paused) { session?.step(.over) }
                ControlButton(symbol: "arrow.down.to.line", title: String.ui("debugger.stepInto", "들어가기"), shortcut: "F11",
                              enabled: canControl && paused) { session?.step(.into) }
                ControlButton(symbol: "arrow.up.to.line", title: String.ui("debugger.stepOut", "나오기"), shortcut: "⇧F11",
                              enabled: canControl && paused) { session?.step(.out) }
            }
            .padding(2)
            .background(DS.Color.neutral.opacity(0.1), in: RoundedRectangle(cornerRadius: DS.Radius.s + 2, style: .continuous))
            StatusText(model: model, session: session)
                .padding(.leading, DS.Space.xs)
            Spacer(minLength: DS.Space.s)
            if let session, session.isClosed, session.info.sessionKind == .launch, let path = session.info.path {
                Button {
                    Task { _ = await model.launch(DebugLaunchRequest(hostID: model.hostID, path: path, args: session.info.args)) }
                } label: {
                    Label(String.ui("debugger.relaunch", "다시 실행"), systemImage: "arrow.clockwise")
                }
                .disabled(!enabled || model.starting != nil)
                .help(String.ui("debugger.relaunch.help", "같은 실행 파일과 인수로 새 디버그 세션을 시작합니다"))
            }
            if let session, !session.isClosed {
                ExceptionMenu(session: session, enabled: enabled)
                Button {
                    model.pendingClose = session.id
                } label: {
                    Label(session.info.sessionKind == .attach ? String.ui("debugger.detach", "연결 끊기") : String.ui("debugger.closeSession", "세션 닫기"),
                          systemImage: "xmark.circle")
                }
                .disabled(session.command == .close)
                .help(session.info.sessionKind == .attach
                      ? String.ui("debugger.detach.help", "앱은 두고 디버거 연결만 끊습니다")
                      : String.ui("debugger.closeSession.help", "세션을 닫습니다. Deskhand가 실행한 앱도 종료할지 묻습니다"))
            }
        }
        .controlSize(.regular)
        .padding(.horizontal, DS.Space.m)
        .frame(height: 44)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("debugger.toolbar", "디버거 제어"))
    }
}

/// An icon button of the execution controls with its name and shortcut in the help tag.
private struct ControlButton: View {
    let symbol: String
    let title: String
    let shortcut: String
    var tint: Color?
    let enabled: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(foreground)
                .frame(width: 30, height: 24)
                .background(hovering && enabled ? DS.Color.neutral.opacity(0.16) : .clear,
                            in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .onHover { hovering = $0 }
        .help(title + " (" + shortcut + ")")
        .accessibilityLabel(title)
        .accessibilityHint(shortcut)
    }

    private var foreground: AnyShapeStyle {
        guard enabled else { return AnyShapeStyle(.tertiary) }
        if let tint { return AnyShapeStyle(tint) }
        return AnyShapeStyle(.primary)
    }
}

/// The selected session's state in a line.
private struct StatusText: View {
    let model: DebuggerModel
    let session: DebugSessionModel?

    var body: some View {
        HStack(spacing: DS.Space.s) {
            indicator
            Text(text)
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private var indicator: some View {
        if model.starting != nil || (session?.command != nil && session?.command != .close) || session?.command == .close {
            ProgressView().controlSize(.small)
        } else if let session {
            switch session.phase {
            case .paused(let reason):
                Image(systemName: CallStackView.isException(reason) ? "exclamationmark.octagon.fill" : "pause.circle.fill")
                    .foregroundStyle(CallStackView.isException(reason) ? DS.Color.danger : DS.Color.warning)
            case .closed:
                Image(systemName: "stop.circle")
                    .foregroundStyle(.secondary)
            default:
                Image(systemName: "circle.fill")
                    .font(.system(size: 7))
                    .foregroundStyle(DS.Color.success)
            }
        }
    }

    private var text: String {
        if let starting = model.starting {
            let name = starting.name
            return starting.kind == .launch ? .ui("debugger.status.launching", "\(name) 실행하는 중…")
                                            : .ui("debugger.status.attaching", "\(name)에 연결하는 중…")
        }
        guard let session else { return .ui("debugger.status.none", "세션 없음") }
        switch session.command {
        case .pause: return .ui("debugger.status.pausing", "일시정지하는 중…")
        case .resume: return .ui("debugger.status.resuming", "재개하는 중…")
        case .step: return .ui("debugger.status.stepping", "한 단계 실행하는 중…")
        case .close: return .ui("debugger.status.closing", "세션을 닫는 중…")
        case nil: break
        }
        let target = session.selectedTarget.map { DebugFormat.role($0) + " · " + DebugFormat.targetTitle($0) } ?? ""
        switch session.phase {
        case .paused(let reason):
            let why = DebugFormat.reason(reason)
            if let frame = session.pauseState?.callFrames.first {
                let location = DebugFormat.location(url: frame.url, line: frame.line)
                return .ui("debugger.status.pausedAt", "일시정지됨 · \(why) · \(location)")
            }
            return .ui("debugger.status.paused", "일시정지됨 · \(why)")
        case .closed(let reason):
            return DebugFormat.closed(reason, exitCode: session.info.exitCode)
        default:
            if !session.pausedTargets.isEmpty {
                let other = session.pausedTargets.map(DebugFormat.role).joined(separator: ", ")
                return .ui("debugger.status.otherPaused", "\(target) 실행 중 · \(other) 일시정지됨")
            }
            return .ui("debugger.status.running", "실행 중 · \(target)")
        }
    }
}

/// 예외에서 멈춤: 멈추지 않음, 잡히지 않은 예외, 모든 예외.
private struct ExceptionMenu: View {
    let session: DebugSessionModel
    let enabled: Bool

    var body: some View {
        Menu {
            Picker(String.ui("debugger.exceptions", "예외에서 멈춤"), selection: Binding(
                get: { session.info.exceptionMode }, set: { session.setExceptionMode($0) })) {
                ForEach(ExceptionPauseMode.allCases, id: \.self) { mode in
                    Text(DebugFormat.exceptionMode(mode)).tag(mode)
                }
            }
            .pickerStyle(.inline)
        } label: {
            Label(String.ui("debugger.exceptions.label", "예외: \(DebugFormat.exceptionMode(session.info.exceptionMode))"),
                  systemImage: "exclamationmark.octagon")
        }
        .fixedSize()
        .disabled(!enabled)
        .help(String.ui("debugger.exceptions.help", "예외가 던져질 때 멈출지 고릅니다"))
    }
}

// MARK: Idle

/// No session yet: what the tab does, launch and attach, recent launches and how launching works.
private struct DebuggerIdleView: View {
    let model: DebuggerModel
    let host: Host
    let enabled: Bool

    var body: some View {
        ScrollView {
            VStack(spacing: DS.Space.xl) {
                VStack(spacing: DS.Space.l) {
                    Image(systemName: "ladybug")
                        .font(.system(size: 40, weight: .regular))
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    VStack(spacing: DS.Space.s) {
                        Text(String.ui("debugger.idle.title", "Electron 앱 디버그"))
                            .font(DS.Font.display)
                            .accessibilityAddTraits(.isHeader)
                        Text(String.ui("debugger.idle.message", "\(host.name)에서 Electron 앱을 디버그 포트를 연 채로 실행하거나 실행 중인 앱에 붙습니다. 메인과 렌더러에 중단점을 걸고, 멈추면 호출 스택과 변수를 보고 식을 평가합니다."))
                            .font(DS.Font.body)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .frame(maxWidth: 480)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    HStack(spacing: DS.Space.m) {
                        Button {
                            model.sheet = .launch
                        } label: {
                            Label(String.ui("debugger.launch.button", "앱 실행…"), systemImage: "play.rectangle")
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.large)
                        Button {
                            model.sheet = .attach
                        } label: {
                            Label(String.ui("debugger.attach.button", "실행 중인 앱에 연결…"), systemImage: "cable.connector")
                        }
                        .controlSize(.large)
                    }
                    .disabled(!enabled || model.starting != nil)
                }
                if !model.recentLaunches.isEmpty {
                    RecentLaunches(model: model, enabled: enabled)
                        .frame(maxWidth: 720)
                }
                HowItWorks()
                    .frame(maxWidth: 720)
            }
            .padding(.horizontal, DS.Space.xl)
            .padding(.vertical, DS.Space.xxl)
            .frame(maxWidth: .infinity)
        }
    }
}

/// Apps launched for debugging before, each launched again with one click.
private struct RecentLaunches: View {
    let model: DebuggerModel
    let enabled: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            Text(String.ui("debugger.recent", "최근 실행"))
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .padding(.leading, DS.Space.xs)
            VStack(spacing: 0) {
                ForEach(Array(model.recentLaunches.enumerated()), id: \.element.id) { index, launch in
                    if index > 0 { Divider().padding(.leading, 44) }
                    RecentLaunchRow(model: model, launch: launch, enabled: enabled)
                }
            }
            .background(DS.Color.surfaceRaised.opacity(0.6), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
        }
    }
}

private struct RecentLaunchRow: View {
    let model: DebuggerModel
    let launch: RecentLaunch
    let enabled: Bool
    @State private var running = false

    var body: some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: "macwindow")
                .font(.system(size: 14))
                .foregroundStyle(.secondary)
                .frame(width: 24)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 1) {
                Text(FilePath.lastComponent(launch.path, style: .windows))
                    .font(DS.Font.body.weight(.medium))
                    .lineLimit(1)
                Text(([launch.path] + (launch.args.isEmpty ? [] : [ArgumentSplitter.join(launch.args)])).joined(separator: "  "))
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: DS.Space.s)
            Button(String.ui("debugger.recent.run", "다시 실행")) {
                running = true
                Task {
                    _ = await model.launch(DebugLaunchRequest(hostID: model.hostID, path: launch.path, args: launch.args, cwd: launch.cwd))
                    running = false
                }
            }
            .controlSize(.small)
            .disabled(!enabled || model.starting != nil)
            .help(String.ui("debugger.recent.run.help", "같은 인수로 디버그 실행합니다"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
    }
}

/// Three facts about launching: the main inspector, the renderer DevTools port and cleanup.
private struct HowItWorks: View {
    var body: some View {
        HStack(alignment: .top, spacing: DS.Space.m) {
            fact(symbol: "cpu", title: String.ui("debugger.how.main", "메인 프로세스"),
                 text: String.ui("debugger.how.main.text", "--inspect로 Node 검사기를 열어 main.js와 IPC 처리기를 멈춥니다."))
            fact(symbol: "macwindow", title: String.ui("debugger.how.renderer", "렌더러"),
                 text: String.ui("debugger.how.renderer.text", "--remote-debugging-port로 창마다 DevTools 대상을 엽니다."))
            fact(symbol: "checkmark.shield", title: String.ui("debugger.how.cleanup", "정리"),
                 text: String.ui("debugger.how.cleanup.text", "세션을 닫으면 Deskhand가 연 터널·포트와 실행한 앱만 정리합니다."))
        }
    }

    private func fact(symbol: String, title: String, text: String) -> some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            Label(title, systemImage: symbol)
                .font(DS.Font.body.weight(.semibold))
            Text(text)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(DS.Color.surfaceRaised.opacity(0.6), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
        .accessibilityElement(children: .combine)
    }
}
