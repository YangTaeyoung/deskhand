import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// 연결: the paired computers in a list beside the selected one's address, SSH alias, agent,
/// status and reconnection, with connecting, disconnecting, re-pairing and removal behind
/// confirmations. PC 추가… brings the main window (`main`) forward with the add-computer sheet.
struct ConnectionSettings: View {
    let store: AppStore
    @Bindable var model: SettingsModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        content
            .frame(width: SettingsPane.width, height: SettingsPane.connection.height(for: store))
            .confirmationDialog(disconnectTitle, isPresented: isPresented($model.pendingDisconnect),
                                titleVisibility: .visible, presenting: model.pendingDisconnect) { _ in
                Button(String.ui("action.disconnect", "연결 끊기")) { model.confirmDisconnect() }
                Button(String.ui("action.cancel", "취소"), role: .cancel) {}
            } message: { _ in
                Text(String.ui("settings.connection.disconnect.message", "‘연결’을 누를 때까지 다시 연결하지 않습니다. 진행 중인 전송은 다시 연결하면 이어지고, 실행 중인 명령은 중단될 수 있습니다."))
            }
            .confirmationDialog(repairTitle, isPresented: isPresented($model.pendingRepair),
                                titleVisibility: .visible, presenting: model.pendingRepair) { _ in
                Button(String.ui("settings.connection.repair.confirm", "다시 페어링")) { model.confirmRepair() }
                Button(String.ui("action.cancel", "취소"), role: .cancel) {}
            } message: { host in
                Text(String.ui("settings.connection.repair.message", "SSH 별칭: \(host.sshAlias). 에이전트를 다시 설치하고 이 Mac과 다시 페어링합니다. 그동안 이 PC의 원격 작업이 끊깁니다."))
            }
            .confirmationDialog(removalTitle, isPresented: isPresented($model.pendingRemoval),
                                titleVisibility: .visible, presenting: model.pendingRemoval) { _ in
                Button(String.ui("remove.keepAgent", "목록에서 제거"), role: .destructive) { model.confirmRemoval(uninstall: false) }
                Button(String.ui("remove.uninstall", "에이전트까지 제거"), role: .destructive) { model.confirmRemoval(uninstall: true) }
                Button(String.ui("action.cancel", "취소"), role: .cancel) {}
            } message: { _ in
                Text(String.ui("remove.message", "이 Mac에서 페어링 정보를 지웁니다. ‘에이전트까지 제거’는 SSH로 원격 PC의 에이전트와 예약 작업, 방화벽 규칙도 지웁니다."))
            }
    }

    @ViewBuilder
    private var content: some View {
        if case .daemonUnavailable = store.phase {
            SettingsEmptyState(symbol: "bolt.horizontal.circle",
                               title: String.ui("settings.connection.noDaemon.title", "데몬에 연결되지 않았습니다"),
                               message: String.ui("settings.connection.noDaemon.message", "데몬이 실행되면 페어링한 PC가 여기에 나타납니다."),
                               actionTitle: String.ui("settings.connection.noDaemon.action", "일반 설정 보기")) {
                model.pane = .general
            }
        } else if let error = store.hostsError, !store.hostsLoaded {
            SettingsEmptyState(symbol: "exclamationmark.triangle",
                               title: String.ui("hosts.failed", "PC 목록을 불러오지 못했습니다"),
                               message: [error.message.asSentence, error.fix].compactMap { $0 }.joined(separator: " "),
                               actionTitle: String.ui("action.retry", "다시 시도")) {
                Task { await store.reloadHosts() }
            }
        } else if !store.hostsLoaded {
            LoadingView(title: String.ui("hosts.loading", "PC 목록을 불러오는 중"))
        } else if store.hosts.isEmpty {
            SettingsEmptyState(symbol: "pc",
                               title: String.ui("settings.connection.empty.title", "페어링한 PC가 없습니다"),
                               message: String.ui("settings.connection.empty.message", "PC를 추가하면 주소, 포트, SSH 별칭과 페어링을 여기에서 관리합니다."),
                               actionTitle: String.ui("settings.connection.empty.action", "PC 추가…")) {
                openWindow(id: "main")
                store.presentAddHost()
            }
        } else if let host = model.host(model.connectionHostID) {
            HStack(spacing: 0) {
                List(selection: Binding(get: { Optional(host.hostID) }, set: { model.connectionHostID = $0 })) {
                    ForEach(store.hosts, id: \.hostID) { item in
                        HostSettingsRow(host: item)
                            .tag(item.hostID)
                    }
                }
                .listStyle(.inset)
                .frame(width: 216)
                .accessibilityLabel(String.ui("settings.connection.list", "PC 목록"))
                Divider()
                HostConnectionDetail(host: host, model: model)
            }
        }
    }

    private func isPresented(_ binding: Binding<Host?>) -> Binding<Bool> {
        Binding(get: { binding.wrappedValue != nil }, set: { if !$0 { binding.wrappedValue = nil } })
    }

    private var disconnectTitle: String {
        guard let name = model.pendingDisconnect?.name else { return "" }
        return .ui("settings.connection.disconnect.title", "‘\(name)’ 연결 끊기")
    }

    private var repairTitle: String {
        guard let name = model.pendingRepair?.name else { return "" }
        return .ui("settings.connection.repair.title", "‘\(name)’ 다시 페어링")
    }

    private var removalTitle: String {
        guard let name = model.pendingRemoval?.name else { return "" }
        return .ui("remove.title", "\(name) 제거")
    }
}

/// A computer in the connection pane's list: its tile, name and connection.
private struct HostSettingsRow: View {
    let host: Host
    @Environment(\.backgroundProminence) private var prominence

    var body: some View {
        HStack(spacing: DS.Space.s) {
            HostTile(host: host, size: 28)
            VStack(alignment: .leading, spacing: 0) {
                Text(host.name)
                    .lineLimit(1)
                HStack(spacing: DS.Space.xs) {
                    StatusDot(state: host.connection)
                    Text(host.connection.title)
                        .font(DS.Font.caption)
                        .foregroundStyle(prominence == .increased ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
                        .lineLimit(1)
                }
            }
        }
        .padding(.vertical, DS.Space.xs)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("settings.connection.row.a11y", "\(host.name), \(host.connection.title)"))
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
                    SettingsValue(String.ui("settings.connection.address.value", "\(host.address) · UDP \(String(host.port))"), monospaced: true)
                }
                LabeledContent(String.ui("settings.connection.ssh", "SSH 별칭")) {
                    SettingsValue(host.sshAlias.isEmpty ? "—" : host.sshAlias, monospaced: true)
                }
                LabeledContent(String.ui("settings.connection.agent", "에이전트")) {
                    SettingsValue(host.agentVersion ?? String.ui("settings.connection.agent.unknown", "알 수 없음"), monospaced: true)
                }
            } header: {
                Text(String.ui("settings.connection.computer", "PC"))
            } footer: {
                SettingsFootnote(String.ui("settings.connection.computer.footer", "이름, 주소와 포트는 PC를 추가할 때 정해집니다. 바꾸려면 제거한 뒤 다시 추가하세요."))
            }
            Section {
                connectionRow
                LabeledContent {
                    SettingsValue(String.ui("settings.connection.reconnect.auto", "자동"))
                } label: {
                    SettingsLabel(String.ui("settings.connection.reconnect", "다시 연결"),
                                  detail: String.ui("settings.connection.reconnect.detail", "끊기면 데몬이 간격을 늘려 가며 다시 연결합니다. ‘연결 끊기’로 끊은 연결은 다시 잇지 않습니다."))
                }
            } header: {
                Text(String.ui("settings.connection.state.header", "상태"))
            }
            Section {
                actionRow(title: String.ui("settings.connection.repair", "다시 페어링"),
                          detail: String.ui("settings.connection.repair.detail", "인증서가 맞지 않거나 에이전트를 다시 설치해야 할 때 SSH로 다시 설치하고 페어링합니다."),
                          button: String.ui("settings.connection.repair.button", "다시 페어링…")) {
                    model.pendingRepair = host
                }
                actionRow(title: String.ui("settings.connection.remove", "PC 제거"),
                          detail: String.ui("settings.connection.remove.detail", "이 Mac에서 페어링 정보를 지웁니다. 원하면 PC의 에이전트도 함께 지웁니다."),
                          button: String.ui("settings.connection.remove.button", "제거…")) {
                    model.pendingRemoval = host
                }
            } header: {
                Text(String.ui("settings.connection.pairing", "페어링"))
            }
        }
        .formStyle(.grouped)
    }

    private var header: some View {
        HStack(spacing: DS.Space.m) {
            HostTile(host: host, size: 32)
            VStack(alignment: .leading, spacing: 0) {
                Text(host.name)
                    .font(DS.Font.headline)
                Text(String.ui("settings.connection.header.detail", "\(host.platformTitle) · \(host.statusLine())"))
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }

    private var connectionRow: some View {
        LabeledContent(String.ui("settings.connection.state", "현재")) {
            HStack(spacing: DS.Space.s) {
                StatusDot(state: host.connection)
                SettingsValue(connectionSummary)
                switch host.connection {
                case .offline:
                    Button(String.ui("action.connect", "연결")) { model.connect(host.hostID) }
                        .disabled(running != nil)
                case .blocked:
                    Button(String.ui("settings.connection.repair.button", "다시 페어링…")) { model.pendingRepair = host }
                        .disabled(running != nil)
                case .ready, .degraded, .connecting, .reconnecting:
                    Button(String.ui("action.disconnect.ask", "연결 끊기…")) { model.pendingDisconnect = host }
                        .disabled(running != nil)
                }
            }
        }
    }

    private var connectionSummary: String {
        switch host.connection {
        case .offline:
            guard let ago = host.lastSeenText else { return host.connection.title }
            return .ui("settings.connection.lastSeen", "마지막 연결 \(ago)")
        case .blocked:
            return .ui("settings.connection.blocked", "인증서 불일치")
        default:
            return host.connection.title
        }
    }

    private func actionRow(title: String, detail: String, button: String, action: @escaping () -> Void) -> some View {
        LabeledContent {
            Button(button, action: action)
                .disabled(running != nil)
        } label: {
            SettingsLabel(title, detail: detail)
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
}
