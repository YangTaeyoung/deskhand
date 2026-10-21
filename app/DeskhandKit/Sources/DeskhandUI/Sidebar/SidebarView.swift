import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// How the selected sidebar row is highlighted.
public enum SidebarSelectionStyle: Sendable {
    /// The system list selection.
    case native
    /// A row background in `DS.Status.selectionFill`, used by offscreen snapshots.
    case drawn
}

/// Computer list with selection, per-row actions and the "PC 추가" button at the bottom.
public struct SidebarView: View {
    @Bindable var store: AppStore
    let selectionStyle: SidebarSelectionStyle
    @FocusState private var listFocused: Bool

    public init(store: AppStore, selectionStyle: SidebarSelectionStyle = .native) {
        self.store = store
        self.selectionStyle = selectionStyle
    }

    private var selection: Binding<String?> {
        selectionStyle == .native ? $store.selectedHostID : .constant(nil)
    }

    public var body: some View {
        List(selection: selection) {
            Section {
                if store.hosts.isEmpty {
                    placeholder
                        .selectionDisabled()
                } else {
                    ForEach(store.hosts, id: \.hostID) { host in
                        row(host)
                    }
                }
            } header: {
                Text(String.ui("sidebar.section", "PC"))
                    .foregroundStyle(DS.Status.neutralText)
            }
        }
        .listStyle(.sidebar)
        .focused($listFocused)
        .onChange(of: listFocused) { _, focused in store.layout.sidebarFocused = focused }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            AddHostButton { store.presentAddHost() }
        }
        .accessibilityLabel(String.ui("a11y.sidebar", "PC 목록"))
    }

    @ViewBuilder
    private func row(_ host: Host) -> some View {
        let base = HostRow(host: host, debuggerPaused: host.hostID != store.selectedHostID && store.debuggerPaused(hostID: host.hostID))
            .tag(host.hostID as String?)
            .contextMenu { HostActionsMenu(store: store, host: host) }
        if selectionStyle == .drawn && host.hostID == store.selectedHostID {
            base
                .environment(\.backgroundProminence, .increased)
                .foregroundStyle(.white)
                .listRowBackground(
                    RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                        .fill(DS.Status.selectionFill)
                        .padding(.horizontal, DS.Space.s)
                )
        } else {
            base
        }
    }

    /// Row shown in the empty section: loading, or a note that no computer was added yet.
    @ViewBuilder
    private var placeholder: some View {
        if !store.hostsLoaded && store.hostsError == nil {
            HStack(spacing: DS.Space.s) {
                ProgressView()
                    .controlSize(.small)
                Text(String.ui("hosts.loading", "PC 목록을 불러오는 중"))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
        } else if store.hostsLoaded {
            Text(String.ui("sidebar.empty", "아직 추가한 PC가 없습니다"))
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
                .padding(.vertical, DS.Space.xs)
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
            Label(String.ui("action.addHost", "PC 추가"), systemImage: "plus.circle")
                .font(DS.Font.body)
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .foregroundStyle(DS.Status.neutralText)
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .help(String.ui("help.addHost", "SSH 별칭으로 새 PC를 추가합니다 (⌘N)"))
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
            Button(String.ui("action.repair.ask", "다시 페어링…"), systemImage: "arrow.triangle.2.circlepath") { store.requestRepair(host) }
                .disabled(store.isRepairing(host.hostID))
        case .ready, .degraded, .connecting, .reconnecting:
            Button(String.ui("action.disconnect.ask", "연결 끊기…"), systemImage: "bolt.horizontal.circle") { store.requestDisconnect(host) }
        }
        Button(String.ui("action.doctor", "진단 실행"), systemImage: "stethoscope") { store.presentDoctor(for: host.hostID) }
        Divider()
        Button(String.ui("action.copyAlias", "SSH 별칭 복사"), systemImage: "doc.on.doc") { Pasteboard.copy(host.sshAlias) }
        Divider()
        Button(String.ui("action.remove", "제거…"), systemImage: "trash", role: .destructive) { store.pendingRemoval = host }
    }
}
