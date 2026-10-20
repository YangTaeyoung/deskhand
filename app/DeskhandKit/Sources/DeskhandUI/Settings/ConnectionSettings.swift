import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// 연결: the paired computers beside the selected one's address, port, SSH alias, agent and
/// reconnection, with connect or disconnect, re-pairing and removal behind a confirmation.
/// 컴퓨터 추가… brings the main window (`main`) forward with the add-computer sheet.
struct ConnectionSettings: View {
    let store: AppStore
    @Bindable var model: SettingsModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        content
            .frame(width: SettingsMetrics.width, height: SettingsPane.connection.height)
            .background(DS.Color.surface)
            .confirmationDialog(repairTitle, isPresented: isPresented($model.pendingRepair), titleVisibility: .visible,
                                presenting: model.pendingRepair) { _ in
                Button(String.ui("settings.connection.repair.confirm", "다시 페어링")) { model.confirmRepair() }
                Button(String.ui("action.cancel", "취소"), role: .cancel) {}
            } message: { host in
                Text(String.ui("settings.connection.repair.message", "SSH 별칭: \(host.sshAlias). 에이전트를 다시 설치하고 이 Mac과 다시 페어링합니다. 그동안 이 PC의 원격 작업이 끊깁니다."))
            }
            .confirmationDialog(removalTitle, isPresented: isPresented($model.pendingRemoval), titleVisibility: .visible,
                                presenting: model.pendingRemoval) { _ in
                Button(String.ui("remove.keepAgent", "목록에서 제거"), role: .destructive) { model.confirmRemoval(uninstall: false) }
                Button(String.ui("remove.uninstall", "에이전트까지 제거")) { model.confirmRemoval(uninstall: true) }
                Button(String.ui("action.cancel", "취소"), role: .cancel) {}
            } message: { _ in
                Text(String.ui("remove.message", "이 Mac에서 페어링 정보를 지웁니다. ‘에이전트까지 제거’는 SSH로 원격 컴퓨터의 에이전트와 예약 작업, 방화벽 규칙도 지웁니다."))
            }
    }

    @ViewBuilder
    private var content: some View {
        if case .daemonUnavailable = store.phase {
            EmptyStateView(symbol: "bolt.horizontal.circle",
                           title: String.ui("settings.connection.noDaemon.title", "데몬에 연결되지 않았습니다"),
                           message: String.ui("settings.connection.noDaemon.message", "데몬이 실행되면 페어링한 컴퓨터가 여기에 나타납니다."),
                           actionTitle: String.ui("settings.connection.noDaemon.action", "일반 설정 보기")) {
                model.pane = .general
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let error = store.hostsError, !store.hostsLoaded {
            VStack {
                ErrorBanner(message: String.ui("hosts.failed", "컴퓨터 목록을 불러오지 못했습니다"),
                            fix: [error.message.asSentence, error.fix].compactMap { $0 }.joined(separator: " "),
                            code: error.code,
                            retry: { Task { await store.reloadHosts() } })
            }
            .padding(DS.Space.xl)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if !store.hostsLoaded {
            LoadingView(title: String.ui("hosts.loading", "컴퓨터 목록을 불러오는 중"))
        } else if store.hosts.isEmpty {
            EmptyStateView(symbol: "pc",
                           title: String.ui("settings.connection.empty.title", "페어링한 컴퓨터가 없습니다"),
                           message: String.ui("settings.connection.empty.message", "컴퓨터를 추가하면 주소, 포트, SSH 별칭과 페어링을 여기에서 관리합니다."),
                           actionTitle: String.ui("settings.connection.empty.action", "컴퓨터 추가…")) {
                openWindow(id: "main")
                store.presentAddHost()
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let host = model.host(model.connectionHostID) {
            HStack(spacing: 0) {
                HostSettingsList(hosts: store.hosts, selection: host.hostID) { model.connectionHostID = $0 }
                    .frame(width: 208)
                Divider()
                HostConnectionDetail(host: host, model: model)
            }
        }
    }

    private func isPresented(_ binding: Binding<Host?>) -> Binding<Bool> {
        Binding(get: { binding.wrappedValue != nil }, set: { if !$0 { binding.wrappedValue = nil } })
    }

    private var repairTitle: String {
        guard let host = model.pendingRepair else { return "" }
        return .ui("settings.connection.repair.title", "‘\(host.name)’ 다시 페어링")
    }

    private var removalTitle: String {
        guard let host = model.pendingRemoval else { return "" }
        return .ui("remove.title", "\(host.name) 제거")
    }
}

/// The computers of the connection pane, one row each with its tile, name and status; ↑ and ↓
/// move the selection while the list has focus.
private struct HostSettingsList: View {
    let hosts: [Host]
    let selection: String
    let select: (String) -> Void
    @FocusState private var focused: Bool

    var body: some View {
        ScrollView {
            VStack(spacing: 2) {
                ForEach(hosts, id: \.hostID) { host in
                    row(host)
                }
            }
            .padding(DS.Space.s)
        }
        .background(DS.Color.surfaceRaised.opacity(0.5))
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onKeyPress(.downArrow) { move(1) }
        .onKeyPress(.upArrow) { move(-1) }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("settings.connection.list", "컴퓨터 목록"))
    }

    private func move(_ step: Int) -> KeyPress.Result {
        guard let index = hosts.firstIndex(where: { $0.hostID == selection }) else { return .ignored }
        let next = min(max(index + step, 0), hosts.count - 1)
        select(hosts[next].hostID)
        return .handled
    }

    private func row(_ host: Host) -> some View {
        let selected = host.hostID == selection
        return Button {
            select(host.hostID)
        } label: {
            HStack(spacing: DS.Space.s) {
                HostTile(host: host, size: 28)
                VStack(alignment: .leading, spacing: 0) {
                    Text(host.name)
                        .font(DS.Font.body.weight(.medium))
                        .lineLimit(1)
                    HStack(spacing: DS.Space.xs) {
                        StatusDot(state: host.connection)
                        Text(host.connection.title)
                            .font(DS.Font.caption)
                            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.primary))
            .padding(.horizontal, DS.Space.s)
            .padding(.vertical, 6)
            .background(selected ? DS.Status.selectionFill : .clear,
                        in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .environment(\.backgroundProminence, selected ? .increased : .standard)
        .accessibilityLabel(String.ui("settings.connection.row.a11y", "\(host.name), \(host.connection.title)"))
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }
}

/// The selected computer of the connection pane.
private struct HostConnectionDetail: View {
    let host: Host
    @Bindable var model: SettingsModel

    private var running: HostSettingsAction? { model.hostActions[host.hostID] }

    var body: some View {
        Form {
            if running != nil || model.hostErrors[host.hostID] != nil {
                Section {
                    if let running {
                        SettingsProgress(title: progressTitle(running))
                    }
                    if let error = model.hostErrors[host.hostID] {
                        SettingsNotice(symbol: "exclamationmark.octagon.fill", tint: DS.Color.danger,
                                       title: String.ui("actionError.title", "작업을 완료하지 못했습니다"),
                                       message: [error.message.asSentence, error.fix].compactMap { $0 }.joined(separator: " ")) {
                            HStack(spacing: DS.Space.s) {
                                if let code = error.code { ErrorCodeLine(code: code) }
                                Spacer(minLength: 0)
                                Button(String.ui("settings.connection.error.dismiss", "닫기")) { model.dismissError(host.hostID) }
                                    .controlSize(.small)
                            }
                        }
                    }
                }
            }
            Section {
                header
                LabeledContent(String.ui("settings.connection.address", "주소")) {
                    Text(String.ui("settings.connection.address.value", "\(host.address) · UDP \(String(host.port))"))
                        .font(DS.Font.mono)
                        .textSelection(.enabled)
                }
                LabeledContent(String.ui("settings.connection.ssh", "SSH 별칭")) { mono(host.sshAlias) }
                LabeledContent(String.ui("settings.connection.agent", "에이전트")) {
                    Text(agentText)
                        .monospacedDigit()
                }
            } header: {
                Text(String.ui("settings.connection.computer", "컴퓨터"))
            } footer: {
                SettingsFootnote(String.ui("settings.connection.computer.footer", "이름, 주소와 포트는 컴퓨터를 추가할 때 정해집니다. 바꾸려면 제거한 뒤 다시 추가하세요."))
            }
            Section {
                connectionRow
                LabeledContent {
                    Text(String.ui("settings.connection.reconnect.auto", "자동"))
                } label: {
                    SettingsLabel(String.ui("settings.connection.reconnect", "다시 연결"),
                                  detail: String.ui("settings.connection.reconnect.detail", "끊기면 데몬이 간격을 늘려 가며 다시 연결합니다. ‘연결 끊기’로 끊은 연결은 다시 잇지 않습니다."))
                }
            } header: {
                Text(String.ui("settings.connection.connection", "연결"))
            }
            Section {
                actionRow(title: String.ui("settings.connection.repair", "다시 페어링"),
                          detail: String.ui("settings.connection.repair.detail", "인증서가 맞지 않거나 에이전트를 다시 설치해야 할 때 SSH로 다시 설치하고 페어링합니다."),
                          button: String.ui("settings.connection.repair.button", "다시 페어링…"), destructive: false) {
                    model.pendingRepair = host
                }
                actionRow(title: String.ui("settings.connection.remove", "컴퓨터 제거"),
                          detail: String.ui("settings.connection.remove.detail", "이 Mac에서 페어링 정보를 지웁니다. 원하면 PC의 에이전트도 함께 지웁니다."),
                          button: String.ui("settings.connection.remove.button", "제거…"), destructive: true) {
                    model.pendingRemoval = host
                }
            } header: {
                Text(String.ui("settings.connection.pairing", "페어링"))
            }
        }
        .formStyle(.grouped)
        .scrollContentBackground(.hidden)
        .background(DS.Color.surface)
    }

    /// `0.1.0 · Windows · x64`.
    private var agentText: String {
        let version = host.agentVersion ?? String.ui("settings.connection.agent.unknown", "알 수 없음")
        let platform = host.platformTitle
        return .ui("settings.connection.agent.value", "\(version) · \(platform)")
    }

    private var header: some View {
        HStack(spacing: DS.Space.m) {
            HostTile(host: host, size: 32)
            VStack(alignment: .leading, spacing: 2) {
                Text(host.name)
                    .font(DS.Font.headline)
                HStack(spacing: DS.Space.xs) {
                    StatusDot(state: host.connection)
                    Text(host.statusLine())
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private var connectionRow: some View {
        HStack {
            Text(String.ui("settings.connection.state", "상태"))
            Spacer(minLength: DS.Space.s)
            Text(connectionSummary)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            switch host.connection {
            case .offline:
                Button(String.ui("action.connect", "연결")) { model.connect(host.hostID) }
                    .disabled(running != nil)
            case .blocked:
                EmptyView()
            case .ready, .degraded, .connecting, .reconnecting:
                Button(String.ui("action.disconnect", "연결 끊기")) { model.disconnect(host.hostID) }
                    .disabled(running != nil)
            }
        }
    }

    private var connectionSummary: String {
        switch host.connection {
        case .offline:
            guard let ago = host.lastSeenText else { return host.connection.title }
            return .ui("settings.connection.lastSeen", "마지막 연결 \(ago)")
        case .blocked:
            return .ui("settings.connection.blocked", "인증서가 맞지 않아 연결을 막았습니다")
        default:
            return host.connection.title
        }
    }

    private func actionRow(title: String, detail: String, button: String, destructive: Bool, action: @escaping () -> Void) -> some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            SettingsLabel(title, detail: detail)
            Spacer(minLength: DS.Space.s)
            Button(button, role: destructive ? .destructive : nil, action: action)
                .disabled(running != nil)
        }
    }

    private func progressTitle(_ action: HostSettingsAction) -> String {
        switch action {
        case .connect: .ui("settings.connection.progress.connect", "연결하는 중…")
        case .disconnect: .ui("settings.connection.progress.disconnect", "연결을 끊는 중…")
        case .repair: .ui("settings.connection.progress.repair", "다시 페어링하는 중…")
        case .remove: .ui("settings.connection.progress.remove", "제거하는 중…")
        }
    }

    private func mono(_ text: String) -> some View {
        Text(text.isEmpty ? "—" : text)
            .font(DS.Font.mono)
            .textSelection(.enabled)
    }
}
