import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// How the selected sidebar row is highlighted.
public enum SidebarSelectionStyle: Sendable {
    /// The system list selection.
    case native
    /// An accent-filled row background, used by offscreen snapshots.
    case drawn
}

/// Computer list with selection, per-row actions and the "컴퓨터 추가" button at the bottom.
public struct SidebarView: View {
    @Bindable var store: AppStore
    let selectionStyle: SidebarSelectionStyle

    public init(store: AppStore, selectionStyle: SidebarSelectionStyle = .native) {
        self.store = store
        self.selectionStyle = selectionStyle
    }

    private var selection: Binding<String?> {
        selectionStyle == .native ? $store.selectedHostID : .constant(nil)
    }

    public var body: some View {
        List(selection: selection) {
            if !store.hosts.isEmpty {
                Section {
                    ForEach(store.hosts, id: \.hostID) { host in
                        row(host)
                    }
                } header: {
                    Text(String.ui("sidebar.section", "컴퓨터"))
                }
            }
        }
        .listStyle(.sidebar)
        .overlay {
            if store.hosts.isEmpty {
                placeholder
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            AddHostButton { store.presentAddHost() }
        }
        .accessibilityLabel(String.ui("a11y.sidebar", "컴퓨터 목록"))
    }

    @ViewBuilder
    private func row(_ host: Host) -> some View {
        let base = HostRow(host: host)
            .tag(host.hostID as String?)
            .contextMenu { HostActionsMenu(store: store, host: host) }
        if selectionStyle == .drawn && host.hostID == store.selectedHostID {
            base
                .environment(\.backgroundProminence, .increased)
                .foregroundStyle(.white)
                .listRowBackground(
                    RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                        .fill(DS.Color.accent)
                        .padding(.horizontal, DS.Space.s)
                )
        } else {
            base
        }
    }

    @ViewBuilder
    private var placeholder: some View {
        if !store.hostsLoaded && store.hostsError == nil {
            ProgressView()
                .controlSize(.small)
                .accessibilityLabel(String.ui("hosts.loading", "컴퓨터 목록을 불러오는 중"))
        } else if store.hostsLoaded {
            Text(String.ui("sidebar.empty", "추가한 컴퓨터가\n여기에 표시됩니다"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(DS.Space.l)
        }
    }
}

/// Bottom-of-sidebar button that opens the add-computer sheet.
public struct AddHostButton: View {
    let action: () -> Void

    public init(action: @escaping () -> Void) {
        self.action = action
    }

    public var body: some View {
        Button(action: action) {
            Label(String.ui("action.addHost", "컴퓨터 추가"), systemImage: "plus.circle")
                .font(DS.Font.body)
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .foregroundStyle(.secondary)
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .help(String.ui("help.addHost", "SSH 별칭으로 새 컴퓨터를 추가합니다 (⌘N)"))
    }
}

/// Connect, diagnose, repair and remove actions for one computer, shared by the row context
/// menu, the detail header and the menu bar.
struct HostActionsMenu: View {
    let store: AppStore
    let host: Host

    var body: some View {
        switch host.connection {
        case .offline:
            Button(String.ui("action.connect", "연결"), systemImage: "bolt.horizontal") { store.connect(host.hostID) }
        case .blocked:
            Button(String.ui("action.repair", "다시 페어링"), systemImage: "arrow.triangle.2.circlepath") { store.repair(host.hostID) }
        case .ready, .degraded, .connecting, .reconnecting:
            Button(String.ui("action.disconnect", "연결 끊기"), systemImage: "bolt.horizontal.circle") { store.disconnect(host.hostID) }
        }
        Button(String.ui("action.doctor", "진단 실행"), systemImage: "stethoscope") { store.presentDoctor(for: host.hostID) }
        Divider()
        Button(String.ui("action.copyAlias", "SSH 별칭 복사"), systemImage: "doc.on.doc") { Pasteboard.copy(host.sshAlias) }
        Divider()
        Button(String.ui("action.remove", "제거…"), systemImage: "trash", role: .destructive) { store.pendingRemoval = host }
    }
}
