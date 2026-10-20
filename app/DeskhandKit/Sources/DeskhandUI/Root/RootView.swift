import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Window content: daemon start-up, the daemon-unavailable error, or the computers split view.
public struct RootView: View {
    @Bindable var store: AppStore

    public init(store: AppStore) {
        self.store = store
    }

    public var body: some View {
        Group {
            switch store.phase {
            case .idle, .starting:
                DaemonStartingView()
            case .daemonUnavailable(let failure):
                DaemonUnavailableView(failure: failure) {
                    Task { await store.start() }
                }
            case .ready, .reconnecting:
                ComputersSplitView(store: store)
            }
        }
        .frame(minWidth: 960, minHeight: 600)
        .task { await store.start() }
    }
}

/// Sidebar of computers and the selected computer's detail, with the add, diagnose and remove flows.
struct ComputersSplitView: View {
    @Bindable var store: AppStore

    var body: some View {
        @Bindable var controlActions = store.controlActions
        NavigationSplitView {
            SidebarView(store: store)
                .navigationSplitViewColumnWidth(min: 220, ideal: 260, max: 360)
        } detail: {
            RootDetailView(store: store)
                .navigationTitle(store.selectedHost?.name ?? "Deskhand")
                .navigationSubtitle(store.selectedHost?.sessionSubtitle ?? "")
        }
        .sheet(item: $controlActions.pendingTakeover) { pending in
            TakeoverConfirmView(store: store, pending: pending)
        }
        .sheet(item: $store.addHost) { flow in
            AddHostSheet(flow: flow)
        }
        .sheet(item: $store.doctor) { model in
            DoctorView(model: model) {
                model.cancel()
                store.doctor = nil
            }
        }
        .confirmationDialog(
            removalTitle,
            isPresented: Binding(get: { store.pendingRemoval != nil }, set: { if !$0 { store.pendingRemoval = nil } }),
            titleVisibility: .visible,
            presenting: store.pendingRemoval
        ) { host in
            Button(String.ui("remove.keepAgent", "목록에서 제거"), role: .destructive) {
                store.remove(host.hostID, uninstall: false)
            }
            Button(String.ui("remove.uninstall", "에이전트까지 제거")) {
                store.remove(host.hostID, uninstall: true)
            }
            Button(String.ui("action.cancel", "취소"), role: .cancel) {}
        } message: { _ in
            Text(String.ui("remove.message", "이 Mac에서 페어링 정보를 지웁니다. ‘에이전트까지 제거’는 SSH로 원격 컴퓨터의 에이전트와 예약 작업, 방화벽 규칙도 지웁니다."))
        }
        .alert(
            String.ui("actionError.title", "작업을 완료하지 못했습니다"),
            isPresented: Binding(get: { store.actionError != nil }, set: { if !$0 { store.actionError = nil } }),
            presenting: store.actionError
        ) { _ in
            Button(String.ui("action.ok", "확인"), role: .cancel) {}
        } message: { error in
            Text([error.message, error.fix].compactMap { $0 }.joined(separator: "\n"))
        }
    }

    private var removalTitle: String {
        guard let host = store.pendingRemoval else { return "" }
        return .ui("remove.title", "\(host.name) 제거")
    }
}

/// Detail column: loading, list error, no computers, the selected computer's workspace, or a
/// prompt to select one.
public struct RootDetailView: View {
    let store: AppStore
    let chrome: WorkspaceChrome

    public init(store: AppStore, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.chrome = chrome
    }

    /// The content under the daemon-reconnecting notice, reporting a zero minimum size to the split view.
    public var body: some View {
        content
            .safeAreaInset(edge: .top, spacing: 0) {
                if store.phase == .reconnecting {
                    BannerBand(style: .tinted(DS.Color.warning), symbol: nil,
                               title: String.ui("daemon.reconnecting.title", "데몬과 다시 연결하는 중…"),
                               message: String.ui("daemon.reconnecting.message", "연결되면 목록과 상태를 다시 불러옵니다. 그동안 보이는 상태는 최신이 아닐 수 있습니다.")) {
                        EmptyView()
                    }
                }
            }
            .frame(minWidth: 0, minHeight: 0)
    }

    @ViewBuilder
    private var content: some View {
        if let error = store.hostsError, !store.hostsLoaded {
            VStack {
                ErrorBanner(message: String.ui("hosts.failed", "컴퓨터 목록을 불러오지 못했습니다"),
                            fix: [error.message, error.fix].compactMap { $0 }.joined(separator: " "),
                            code: error.code,
                            retry: { Task { await store.reloadHosts() } })
                    .frame(maxWidth: 560)
            }
            .padding(DS.Space.xl)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if !store.hostsLoaded {
            LoadingView(title: String.ui("hosts.loading", "컴퓨터 목록을 불러오는 중"))
        } else if store.hosts.isEmpty {
            NoHostsView { store.presentAddHost() }
        } else if let host = store.selectedHost {
            WorkspaceView(store: store, host: host, chrome: chrome)
        } else {
            EmptyStateView(symbol: "sidebar.left",
                           title: String.ui("detail.select.title", "컴퓨터를 선택하세요"),
                           message: String.ui("detail.select.message", "사이드바에서 작업할 컴퓨터를 고르세요."))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

/// First-run state: no computers yet, with the add action and what the remote PC needs.
public struct NoHostsView: View {
    let add: () -> Void

    public init(add: @escaping () -> Void) {
        self.add = add
    }

    public var body: some View {
        VStack(spacing: DS.Space.xl) {
            Spacer(minLength: 0)
            EmptyStateView(
                symbol: "pc",
                title: String.ui("empty.hosts.title", "아직 연결한 컴퓨터가 없습니다"),
                message: String.ui("empty.hosts.message", "SSH로 접속되는 Windows PC를 추가하면 에이전트를 설치하고 이 Mac과 안전하게 페어링합니다."),
                actionTitle: String.ui("action.addHost", "컴퓨터 추가"),
                action: add)
            prerequisites
                .frame(maxWidth: 520)
            Spacer(minLength: 0)
        }
        .padding(DS.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var prerequisites: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            Text(String.ui("empty.prereq.title", "추가하기 전에 확인하세요"))
                .font(DS.Font.headline)
                .accessibilityAddTraits(.isHeader)
            PrerequisiteRow(index: 1,
                            title: String.ui("empty.prereq.ssh.title", "Windows에서 OpenSSH 서버를 켭니다"),
                            detail: String.ui("empty.prereq.ssh.detail", "설정 › 시스템 › 선택적 기능에서 OpenSSH 서버를 설치하고 서비스를 시작합니다."))
            PrerequisiteRow(index: 2,
                            title: String.ui("empty.prereq.key.title", "비밀번호 없이 접속되는지 확인합니다"),
                            detail: String.ui("empty.prereq.key.detail", "이 Mac의 터미널에서 ssh 별칭으로 접속해 봅니다. 키 인증만 사용합니다."),
                            command: String.ui("empty.prereq.key.command", "ssh <별칭> hostname"))
            PrerequisiteRow(index: 3,
                            title: String.ui("empty.prereq.admin.title", "관리자 계정을 씁니다"),
                            detail: String.ui("empty.prereq.admin.detail", "에이전트 설치와 방화벽 규칙 등록에 관리자 권한이 필요합니다."))
        }
        .dsCard()
    }
}

struct PrerequisiteRow: View {
    let index: Int
    let title: String
    let detail: String
    var command: String?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Text(verbatim: "\(index)")
                .font(DS.Font.caption.weight(.bold))
                .foregroundStyle(DS.Color.accent)
                .frame(width: 20, height: 20)
                .background(DS.Color.accent.opacity(0.14), in: Circle())
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.body.weight(.medium))
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let command {
                    Text(command)
                        .font(DS.Font.mono)
                        .textSelection(.enabled)
                        .padding(.horizontal, DS.Space.s)
                        .padding(.vertical, DS.Space.xs)
                        .background(DS.Color.surface, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous).strokeBorder(DS.Color.separator))
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Centered spinner with a title, for views waiting on the daemon.
public struct LoadingView: View {
    let title: String
    var detail: String?

    public init(title: String, detail: String? = nil) {
        self.title = title
        self.detail = detail
    }

    public var body: some View {
        VStack(spacing: DS.Space.m) {
            ProgressView()
                .controlSize(.regular)
            Text(title)
                .font(DS.Font.headline)
            if let detail {
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .combine)
    }
}

/// Shown while the daemon is being started; `DaemonLauncher` bounds this to five seconds.
public struct DaemonStartingView: View {
    public init() {}

    public var body: some View {
        LoadingView(title: String.ui("daemon.starting", "데몬에 연결하는 중…"),
                    detail: String.ui("daemon.starting.detail", "deskhand daemon이 실행 중이 아니면 시작합니다."))
    }
}

/// Full-window error when the daemon cannot be started: the cause, retry, and the daemon log.
public struct DaemonUnavailableView: View {
    let failure: AppStore.DaemonFailure
    let retry: () -> Void
    @State private var copied = false

    public static let startCommand = "deskhand daemon start"

    public init(failure: AppStore.DaemonFailure, retry: @escaping () -> Void) {
        self.failure = failure
        self.retry = retry
    }

    public var body: some View {
        VStack(spacing: DS.Space.xl) {
            RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous)
                .fill(DS.Color.danger.opacity(0.12))
                .frame(width: 64, height: 64)
                .overlay {
                    Image(systemName: "desktopcomputer.trianglebadge.exclamationmark")
                        .font(.system(size: 30, weight: .medium))
                        .foregroundStyle(DS.Color.danger)
                }
                .accessibilityHidden(true)
            VStack(spacing: DS.Space.s) {
                Text(String.ui("daemon.unavailable.title", "데몬을 시작할 수 없습니다"))
                    .font(DS.Font.display)
                    .accessibilityAddTraits(.isHeader)
                Text(String.ui("daemon.unavailable.message", "Deskhand는 이 Mac에서 실행되는 데몬을 통해 원격 컴퓨터에 연결합니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            ErrorBanner(message: failure.message,
                        fix: String.ui("daemon.unavailable.fix", "로그에서 원인을 확인하거나 터미널에서 데몬을 직접 시작한 뒤 다시 시도하세요."),
                        retry: retry)
            VStack(alignment: .leading, spacing: DS.Space.m) {
                KeyValueGrid([
                    KeyValueRow(String.ui("daemon.field.log", "로그"), failure.logPath.abbreviatingHome, monospaced: true),
                    KeyValueRow(String.ui("daemon.field.command", "직접 시작"), Self.startCommand, monospaced: true),
                ])
                HStack(spacing: DS.Space.s) {
                    Button {
                        FinderReveal.reveal(path: failure.logPath)
                    } label: {
                        Label(String.ui("daemon.openLog", "로그 열기"), systemImage: "doc.text.magnifyingglass")
                    }
                    .help(String.ui("help.openLog", "Finder에서 데몬 로그를 엽니다"))
                    Button {
                        Pasteboard.copy(Self.startCommand)
                        copied = true
                        Task {
                            try? await Task.sleep(for: .seconds(1.5))
                            copied = false
                        }
                    } label: {
                        Label(copied ? String.ui("action.copied", "복사됨") : String.ui("daemon.copyCommand", "명령 복사"),
                              systemImage: copied ? "checkmark" : "doc.on.doc")
                    }
                    .help(String.ui("help.copyCommand", "데몬을 시작하는 터미널 명령을 복사합니다"))
                }
                .buttonStyle(.bordered)
                .controlSize(.regular)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .dsCard()
        }
        .frame(maxWidth: 560)
        .padding(DS.Space.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
