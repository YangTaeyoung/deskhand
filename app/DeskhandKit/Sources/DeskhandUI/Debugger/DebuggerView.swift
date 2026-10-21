import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The debugger tab: a toolbar with resume and step controls, the script navigator and the
/// exception setting; sessions and their targets over the breakpoints on the left; the paused
/// location over the console in the middle; the call stack over the variables on the right.
/// Below `narrowWidth` the left column folds away behind the toolbar's sidebar button. Without a
/// session it offers to launch an Electron app or attach to a running one.
public struct DebuggerView: View {
    static let narrowWidth: CGFloat = 1000

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
                DebuggerContent(store: store, model: model, host: host, availability: availability, chrome: chrome)
            } else {
                HostFeatureUnavailableView(host: host, availability: availability, feature: .debugger)
                    .background(DS.Color.surface)
            }
        }
        .onAppear { model.start() }
        .onChange(of: store.layout.refreshRequests) { _, _ in Task { await model.reload() } }
    }
}

private struct DebuggerContent: View {
    let store: AppStore
    @Bindable var model: DebuggerModel
    let host: Host
    let availability: HostFeatureAvailability
    let chrome: WorkspaceChrome
    @State private var keys = DebuggerKeyMonitor()
    /// The tab's width once laid out.
    @State private var width: CGFloat?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var colorScheme

    private var narrow: Bool { (width ?? DebuggerView.narrowWidth) < DebuggerView.narrowWidth }
    private var showsNavigator: Bool { model.navigatorShown ?? !narrow }
    private var navigatorOverlay: Bool { narrow && showsNavigator }

    private func setNavigator(_ shown: Bool) {
        withAnimation(reduceMotion ? nil : .snappy(duration: 0.2)) { model.navigatorShown = shown }
    }

    var body: some View {
        VStack(spacing: 0) {
            if !model.sessions.isEmpty || model.starting != nil {
                DebuggerToolbar(model: model, session: model.selected, enabled: availability == .ready, chrome: chrome,
                                navigatorShown: showsNavigator) {
                    setNavigator(!showsNavigator)
                }
            }
            if availability != .ready {
                OfflineStrip(host: host, availability: availability)
            }
            main
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(DS.Color.surface)
        .background(WindowReader { keys.window = $0 })
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .onChange(of: navigatorOverlay, initial: true) { _, overlay in model.navigatorOverlay = overlay }
        .overlayPreferenceValue(ScriptNavigatorAnchor.self) { anchor in
            if chrome == .standIn, model.showsScriptNavigator, let anchor, let session = model.selected {
                GeometryReader { proxy in
                    let rect = proxy[anchor]
                    ScriptNavigatorStandIn(session: session)
                        .offset(x: min(max(DS.Space.s, rect.midX - ScriptNavigator.width / 2), proxy.size.width - ScriptNavigator.width - DS.Space.s),
                                y: rect.maxY + DS.Space.xs)
                }
            }
        }
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
            case .condition(let breakpointID):
                if let session = model.selected, let breakpoint = session.info.breakpoints.first(where: { $0.breakpointID == breakpointID }) {
                    BreakpointSheet(session: session, editing: breakpoint) { model.sheet = nil }
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
                Button(String.ui("debugger.close.detach", "연결 해제")) {
                    Task { await session.close(keepProcess: true) }
                }
            }
            Button(String.ui("action.cancel", "취소"), role: .cancel) {}
        } message: { session in
            Text(closeMessage(session))
        }
        .onAppear {
            let model = model
            let store = store
            let host = host
            let keys = keys
            keys.install { event in
                let current = store.host(host.hostID) ?? host
                return DebuggerKeyHandler.handle(event, model: model, enabled: HostFeatureAvailability(host: current, capability: "tunnel") == .ready,
                                                 window: keys.window)
            }
        }
        .onDisappear { keys.remove() }
    }

    private var closeTitle: String {
        guard let session = model.pendingClose.flatMap(model.session) else { return "" }
        let name = session.info.name
        if session.info.sessionKind == .attach { return .ui("debugger.close.detachTitle", "‘\(name)’ 앱에서 연결을 해제할까요?") }
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
        return .ui("debugger.close.attachMessage", "\(name) 앱은 계속 실행됩니다. Deskhand가 연 터널을 닫고, Deskhand가 켠 검사기는 끌 수 있으면 끕니다.")
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
                                fix: DebuggerModel.listFix(error), code: error.code) {
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

    private var navigator: some View {
        DebugSplit(axis: .vertical, fraction: $model.navigatorSplit, minimumFirst: 140, minimumSecond: 120) {
            TargetsList(model: model, enabled: availability == .ready, disabledReason: OfflineStrip.reason(host: host, availability: availability),
                        launch: { model.sheet = .launch }, attach: { model.sheet = .attach })
        } second: {
            BreakpointsList(model: model, session: model.selected, enabled: availability == .ready) { model.sheet = .breakpoint }
        }
    }

    private var workspace: some View {
        GeometryReader { proxy in
            let width = proxy.size.width
            let navigatorWidth = min(max(width * 0.22, 240), 320)
            let inspectorWidth = narrow ? 248 : min(max(width * 0.24, 248), 320)
            let inspectorHeight = proxy.size.height
            HStack(spacing: 0) {
                if showsNavigator, !narrow {
                    navigator
                        .frame(width: navigatorWidth)
                        .background(DS.Color.surfaceRaised.opacity(0.35))
                        .clipped()
                    Divider()
                }
                DebugSplit(axis: .vertical, fraction: $model.centerSplit, minimumFirst: 160, minimumSecond: 152) {
                    SourcePanel(model: model, session: model.selected)
                } second: {
                    DebugConsoleView(model: model, session: model.selected, compact: narrow)
                }
                .frame(minWidth: 0, maxWidth: .infinity)
                .clipped()
                Divider()
                VStack(spacing: 0) {
                    let frames = model.selected?.pauseState?.callFrames.count ?? 0
                    CallStackView(session: model.selected)
                        .frame(height: min(CallStackView.idealHeight(frames: frames), inspectorHeight * 0.45))
                    Divider()
                    VariablesView(session: model.selected)
                        .frame(maxHeight: .infinity)
                }
                .frame(width: inspectorWidth)
                .clipped()
            }
            .overlay(alignment: .leading) {
                if navigatorOverlay {
                    Color.black.opacity(colorScheme == .dark ? 0.24 : 0.08)
                        .contentShape(Rectangle())
                        .onTapGesture { setNavigator(false) }
                        .transition(.opacity)
                        .accessibilityHidden(true)
                }
            }
            .overlay(alignment: .leading) {
                if navigatorOverlay {
                    navigator
                        .frame(width: 248)
                        .background(DS.Color.surfaceRaised.opacity(0.35))
                        .background(DS.Color.surface)
                        .overlay(alignment: .trailing) { Rectangle().fill(DS.Color.separator).frame(width: 1) }
                        .shadow(color: .black.opacity(colorScheme == .dark ? 0.5 : 0.18), radius: 12, x: 4)
                        .transition(reduceMotion ? .opacity : .move(edge: .leading))
                }
            }
            .overlay(alignment: .bottom) {
                if let session = model.selected, let notice = session.notice {
                    DiagnosticsNoticeToast(notice: notice) { session.dismissNotice() }
                        .padding(.horizontal, DS.Space.l)
                        .padding(.bottom, DebugConsoleView.inputBarHeight + DS.Space.s)
                        .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
                }
            }
            .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: model.selected?.notice)
        }
    }
}

/// The PC went offline or is reconnecting while sessions are listed.
struct OfflineStrip: View {
    let host: Host
    let availability: HostFeatureAvailability

    var body: some View {
        HostOfflineStrip(reconnecting: availability == .connecting || availability == .reconnecting, detail: message)
    }

    private var message: String {
        switch availability {
        case .connecting, .reconnecting:
            return .ui("debugger.offline.reconnecting", "연결이 돌아오면 세션 상태와 놓친 콘솔 기록을 다시 읽습니다.")
        default:
            return .ui("debugger.offline.strip", "실행 제어와 평가는 쓸 수 없고, 이전 세션 기록만 볼 수 있습니다.")
        }
    }
    /// Why sessions cannot start on `host` now, or nil while it can serve the debugger.
    static func reason(host: Host, availability: HostFeatureAvailability) -> String? {
        let name = host.name
        switch availability {
        case .ready: return nil
        case .connecting, .reconnecting: return .ui("debugger.new.reconnecting", "\(name)에 다시 연결하는 중이라 새 세션을 시작할 수 없습니다")
        default: return .ui("debugger.new.offlineNamed", "\(name)에 연결되어 있지 않아 새 세션을 시작할 수 없습니다")
        }
    }
}

// MARK: Toolbar

/// The bounds of the toolbar's script button, where the script navigator opens.
struct ScriptNavigatorAnchor: PreferenceKey {
    static let defaultValue: Anchor<CGRect>? = nil

    static func reduce(value: inout Anchor<CGRect>?, nextValue: () -> Anchor<CGRect>?) {
        value = value ?? nextValue()
    }
}

/// The script navigator drawn in place of its popover, for offscreen snapshots.
private struct ScriptNavigatorStandIn: View {
    let session: DebugSessionModel

    var body: some View {
        ScriptNavigator(session: session) {}
            .background(DS.Color.surface)
            .clipShape(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
            .shadow(color: .black.opacity(0.2), radius: 16, y: 8)
    }
}

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
                        Text(String.ui("debugger.idle.message", "\(host.name)에서 Electron 앱을 디버그 포트를 연 채로 실행하거나 실행 중인 앱에 연결합니다. 메인과 렌더러에 중단점을 걸고, 멈추면 호출 스택과 변수를 보고 식을 평가합니다."))
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

/// Apps launched for debugging before, each launched again with one click with the same
/// arguments, folder, start pause and breakpoints.
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

    var body: some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: "macwindow")
                .font(.system(.title3))
                .foregroundStyle(.secondary)
                .frame(width: 24)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 0) {
                Text(FilePath.lastComponent(launch.path, style: .windows))
                    .font(DS.Font.body.weight(.medium))
                    .lineLimit(1)
                Text(([launch.path] + (launch.args.isEmpty ? [] : [ArgumentSplitter.join(launch.args)])).joined(separator: "  "))
                    .font(.system(.subheadline, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let extras = extras {
                    Text(extras)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: DS.Space.s)
            Button(String.ui("debugger.recent.run", "다시 실행")) {
                Task { _ = await model.launch(launch.request(hostID: model.hostID)) }
            }
            .controlSize(.small)
            .disabled(!enabled || model.starting != nil)
            .help(String.ui("debugger.recent.run.help", "같은 인수, 작업 폴더, 중단점으로 디버그 실행합니다"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
    }

    /// `중단점 3개 · 첫 줄에서 멈춤` when the launch carries them.
    private var extras: String? {
        var parts: [String] = []
        if !launch.breakpoints.isEmpty {
            let count = String(launch.breakpoints.count)
            parts.append(.ui("debugger.recent.breakpoints", "중단점 \(count)개"))
        }
        if launch.pauseOnStart { parts.append(.ui("debugger.recent.pauseOnStart", "첫 줄에서 멈춤")) }
        if let cwd = launch.cwd, !cwd.isEmpty { parts.append(.ui("debugger.recent.cwd", "작업 폴더 \(cwd)")) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

/// Three facts about launching: the main inspector, the renderer DevTools port and cleanup.
private struct HowItWorks: View {
    var body: some View {
        HStack(alignment: .top, spacing: DS.Space.m) {
            fact(symbol: "cpu", title: String.ui("debugger.how.main", "메인 프로세스"),
                 text: String.ui("debugger.how.main.text", "--inspect로 Node 검사기를 열어 main.js와 IPC 처리기에서 멈출 수 있습니다."))
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
