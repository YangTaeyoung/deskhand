import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Inspector pane with the computer's platform and connection state, its diagnose and
/// connection actions, and its connection, session, display and capability details.
struct HostInfoPane: View {
    let store: AppStore
    let host: Host

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DS.Space.l) {
                summary
                actions
                Divider()
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.s) {
                    section(String.ui("host.section.connection", "연결"), rows: connectionRows)
                    GridRow {
                        Divider()
                            .gridCellColumns(2)
                            .padding(.vertical, DS.Space.s)
                    }
                    section(String.ui("host.section.session", "세션과 디스플레이"), rows: sessionRows)
                }
                Divider()
                CapabilitySummary(host: host)
            }
            .padding(DS.Space.l)
        }
    }

    /// A header row spanning both columns, then one row per key and value, in the grid shared by
    /// every section.
    @ViewBuilder
    private func section(_ title: String, rows: [KeyValueRow]) -> some View {
        GridRow {
            Text(title)
                .font(DS.Font.headline)
                .accessibilityAddTraits(.isHeader)
                .gridCellColumns(2)
                .padding(.bottom, DS.Space.xs)
        }
        ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
            GridRow {
                Text(row.key)
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Text(row.value)
                    .font(row.monospaced ? DS.Font.mono : DS.Font.body)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .accessibilityElement(children: .combine)
        }
    }

    private var summary: some View {
        HStack(spacing: DS.Space.m) {
            HostTile(host: host, size: 40)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(host.platformTitle)
                    .font(DS.Font.headline)
                StatusDot(state: host.connection, showsLabel: true)
            }
        }
        .accessibilityElement(children: .combine)
    }

    /// Diagnose, and disconnect while connected. Connect and re-pair live in the workspace banner.
    private var actions: some View {
        HStack(spacing: DS.Space.s) {
            Button {
                store.presentDoctor(for: host.hostID)
            } label: {
                Label(String.ui("action.doctor", "진단 실행"), systemImage: "stethoscope")
            }
            if host.connection == .ready || host.connection == .degraded {
                Button {
                    store.disconnect(host.hostID)
                } label: {
                    Label(String.ui("action.disconnect", "연결 끊기"), systemImage: "bolt.horizontal.circle")
                }
            }
        }
        .buttonStyle(.bordered)
        .controlSize(.regular)
    }

    private var connectionRows: [KeyValueRow] {
        var rows = [
            KeyValueRow(String.ui("host.field.address", "주소"), "\(host.address):\(host.port)", monospaced: true),
            KeyValueRow(String.ui("host.field.alias", "SSH 별칭"), host.sshAlias, monospaced: true),
            KeyValueRow(String.ui("host.field.agent", "에이전트"), host.agentVersion ?? "—"),
            KeyValueRow(String.ui("host.field.generation", "연결 세대"), "\(host.connectionGeneration)"),
        ]
        if host.connection != .ready, let ago = host.lastSeenText {
            rows.append(KeyValueRow(String.ui("host.field.lastSeen", "마지막 연결"), ago))
        }
        return rows
    }

    private var sessionRows: [KeyValueRow] {
        var rows: [KeyValueRow] = []
        if let session = host.session {
            rows.append(KeyValueRow(String.ui("host.field.user", "사용자"), session.user))
            rows.append(KeyValueRow(String.ui("host.field.session", "세션"), HostOS.sessionTitle(session)))
            rows.append(KeyValueRow(String.ui("host.field.desktop", "입력 데스크톱"), HostOS.desktopTitle(session.inputDesktop)))
        } else {
            rows.append(KeyValueRow(String.ui("host.field.session", "세션"), String.ui("host.session.none", "정보 없음 · 연결되면 표시됩니다")))
        }
        for display in host.displays {
            rows.append(KeyValueRow(
                display.primary ? String.ui("host.field.primaryDisplay", "주 디스플레이") : String.ui("host.field.display", "디스플레이"),
                "\(display.width) × \(display.height) · \(Int((display.scale * 100).rounded()))% · \(display.refreshHz) Hz\n\(display.adapter)"))
        }
        return rows
    }
}
