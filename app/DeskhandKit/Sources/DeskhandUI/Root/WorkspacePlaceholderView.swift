import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Detail column for the selected computer: the workspace header (name, connection, control and
/// actions), a notice when it is not connected, its connection and session facts, capabilities
/// and recent activity.
public struct WorkspacePlaceholderView: View {
    let store: AppStore
    let host: Host

    public init(store: AppStore, host: Host) {
        self.store = store
        self.host = host
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DS.Space.xl) {
                header
                notice
                HStack(alignment: .top, spacing: DS.Space.l) {
                    connectionCard
                    sessionCard
                }
                .fixedSize(horizontal: false, vertical: true)
                CapabilitySummary(host: host)
                    .dsCard()
                ActivityCard(entries: store.timeline(for: host.hostID))
            }
            .padding(DS.Space.xl)
            .frame(maxWidth: 1040, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: host.hostID) {
            await store.loadControl(for: host.hostID)
            await store.loadTimeline(for: host.hostID)
        }
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .center, spacing: DS.Space.l) {
            HostTile(host: host, size: 48)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(host.name)
                    .font(.title.weight(.semibold))
                    .lineLimit(1)
                    .accessibilityAddTraits(.isHeader)
                HStack(spacing: DS.Space.s) {
                    StatusDot(state: host.connection, showsLabel: true)
                    Text(verbatim: "·").foregroundStyle(.tertiary)
                    Text(verbatim: "\(host.platformTitle) · \(host.address)")
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: DS.Space.l)
            if host.connection == .ready || host.connection == .degraded {
                let control = store.control(for: host.hostID)
                ControlPill(owner: control.flatMap { ControlOwner(rawValue: $0.owner) } ?? .none,
                            clientName: control?.ownerClient)
            }
            HStack(spacing: DS.Space.s) {
                Button {
                    store.presentDoctor(for: host.hostID)
                } label: {
                    Label(String.ui("action.doctor", "진단 실행"), systemImage: "stethoscope")
                }
                primaryAction
            }
            .controlSize(.large)
        }
    }

    @ViewBuilder
    private var primaryAction: some View {
        switch host.connection {
        case .offline:
            Button {
                store.connect(host.hostID)
            } label: {
                Label(String.ui("action.connect", "연결"), systemImage: "bolt.horizontal")
            }
            .buttonStyle(.borderedProminent)
        case .blocked:
            Button {
                store.repair(host.hostID)
            } label: {
                Label(String.ui("action.repair", "다시 페어링"), systemImage: "arrow.triangle.2.circlepath")
            }
            .buttonStyle(.borderedProminent)
        case .ready, .degraded, .connecting, .reconnecting:
            Menu {
                HostActionsMenu(store: store, host: host)
            } label: {
                Label(String.ui("action.more", "더 보기"), systemImage: "ellipsis")
                    .labelStyle(.iconOnly)
            }
            .menuIndicator(.hidden)
            .fixedSize()
            .help(String.ui("action.more", "더 보기"))
        }
    }

    // MARK: Notice

    @ViewBuilder
    private var notice: some View {
        switch host.connection {
        case .ready:
            EmptyView()
        case .offline:
            StatusNotice(symbol: "moon.zzz.fill", tint: DS.Color.neutral,
                         title: String.ui("notice.offline.title", "오프라인입니다"),
                         message: host.lastSeenText.map {
                             String.ui("notice.offline.lastSeen", "마지막 연결 \($0). 컴퓨터가 켜져 있으면 연결을 눌러 다시 시도하세요.")
                         } ?? String.ui("notice.offline.message", "컴퓨터가 켜져 있으면 연결을 눌러 다시 시도하세요."))
        case .connecting, .reconnecting:
            StatusNotice(symbol: nil, tint: DS.Color.warning,
                         title: host.connection == .connecting ? String.ui("notice.connecting.title", "연결하는 중…")
                             : String.ui("notice.reconnecting.title", "다시 연결하는 중…"),
                         message: String.ui("notice.reconnecting.message", "연결이 돌아오면 화면과 명령을 바로 다시 쓸 수 있습니다. 결과를 모르는 작업은 다시 실행하지 않습니다."))
        case .degraded:
            StatusNotice(symbol: "wifi.exclamationmark", tint: DS.Color.warning,
                         title: String.ui("notice.degraded.title", "연결이 불안정합니다"),
                         message: String.ui("notice.degraded.message", "화면이 늦게 바뀌거나 끊길 수 있습니다. 네트워크 상태를 확인하세요."))
        case .blocked:
            ErrorBanner(message: String.ui("notice.blocked.title", "인증서가 페어링 때와 다릅니다"),
                        fix: String.ui("notice.blocked.fix", "에이전트를 다시 설치했다면 다시 페어링하세요. 다시 설치한 적이 없다면 다른 컴퓨터가 이 주소를 쓰고 있을 수 있으니 진단을 먼저 실행하세요."))
        }
    }

    // MARK: Facts

    private var connectionCard: some View {
        var rows = [
            KeyValueRow(String.ui("host.field.address", "주소"), "\(host.address):\(host.port)", monospaced: true),
            KeyValueRow(String.ui("host.field.alias", "SSH 별칭"), host.sshAlias, monospaced: true),
            KeyValueRow(String.ui("host.field.agent", "에이전트"), host.agentVersion ?? "—"),
            KeyValueRow(String.ui("host.field.generation", "연결 세대"), "\(host.connectionGeneration)"),
        ]
        if host.connection != .ready, let ago = host.lastSeenText {
            rows.append(KeyValueRow(String.ui("host.field.lastSeen", "마지막 연결"), ago))
        }
        return FactCard(title: String.ui("host.section.connection", "연결"), rows: rows)
    }

    private var sessionCard: some View {
        var rows: [KeyValueRow] = []
        if let session = host.session {
            rows.append(KeyValueRow(String.ui("host.field.user", "사용자"), session.user))
            rows.append(KeyValueRow(String.ui("host.field.session", "세션"), HostOS.sessionTitle(session)))
        } else {
            rows.append(KeyValueRow(String.ui("host.field.session", "세션"), String.ui("host.session.none", "정보 없음 · 연결되면 표시됩니다")))
        }
        for display in host.displays {
            rows.append(KeyValueRow(
                display.primary ? String.ui("host.field.primaryDisplay", "주 디스플레이") : String.ui("host.field.display", "디스플레이"),
                "\(display.width) × \(display.height) · \(Int((display.scale * 100).rounded()))% · \(display.refreshHz) Hz"))
        }
        return FactCard(title: String.ui("host.section.session", "세션과 디스플레이"), rows: rows)
    }
}

/// Titled card holding a `KeyValueGrid`.
struct FactCard: View {
    let title: String
    let rows: [KeyValueRow]

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            Text(title)
                .font(DS.Font.headline)
                .accessibilityAddTraits(.isHeader)
            KeyValueGrid(rows)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .dsCard()
    }
}

/// Inline status message with a symbol (or a spinner when `symbol` is nil), tinted background.
struct StatusNotice: View {
    let symbol: String?
    let tint: Color
    let title: String
    let message: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Group {
                if let symbol {
                    Image(systemName: symbol)
                        .foregroundStyle(tint)
                } else {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .font(DS.Font.headline)
            .frame(width: DS.Space.l)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.headline)
                Text(message)
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(DS.Space.m)
        .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(tint.opacity(0.24), lineWidth: 1)
        )
        .accessibilityElement(children: .combine)
    }
}

/// The latest timeline entries of a computer.
struct ActivityCard: View {
    let entries: [TimelineEntry]
    static let limit = 5

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            Text(String.ui("host.section.activity", "최근 활동"))
                .font(DS.Font.headline)
                .accessibilityAddTraits(.isHeader)
            if entries.isEmpty {
                Text(String.ui("host.activity.empty", "아직 기록된 동작이 없습니다. 사람이나 AI가 이 컴퓨터를 조작하면 여기에 남습니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            } else {
                VStack(spacing: DS.Space.s) {
                    ForEach(entries.suffix(Self.limit).reversed(), id: \.seq) { entry in
                        ActivityRow(entry: entry)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .dsCard()
    }
}

struct ActivityRow: View {
    let entry: TimelineEntry

    private var owner: ControlOwner {
        switch entry.actor {
        case "human": .human
        case "ai": .ai
        default: .none
        }
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Image(systemName: entry.actor == "system" ? "gearshape.fill" : owner.symbol)
                .foregroundStyle(entry.actor == "system" ? AnyShapeStyle(.secondary) : AnyShapeStyle(owner.tint))
                .frame(width: DS.Space.l)
                .accessibilityHidden(true)
            Text(entry.summary)
                .font(entry.kind == "exec" || entry.kind == "file" ? DS.Font.mono : DS.Font.body)
                .lineLimit(1)
                .truncationMode(.middle)
            if entry.outcome == "failed" || entry.outcome == "denied" || entry.outcome == "unknown" {
                Text(outcomeTitle)
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(entry.outcome == "unknown" ? DS.Color.warning : DS.Color.danger)
                    .padding(.horizontal, DS.Space.s)
                    .background((entry.outcome == "unknown" ? DS.Color.warning : DS.Color.danger).opacity(0.12), in: Capsule())
            }
            Spacer(minLength: DS.Space.m)
            Text(entry.clientName ?? actorTitle)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(entry.ts.date.formatted(date: .omitted, time: .shortened))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .monospacedDigit()
        }
        .accessibilityElement(children: .combine)
    }

    private var actorTitle: String {
        switch entry.actor {
        case "human": .ui("actor.human", "사람")
        case "ai": .ui("actor.ai", "AI")
        default: .ui("actor.system", "시스템")
        }
    }

    private var outcomeTitle: String {
        switch entry.outcome {
        case "failed": .ui("outcome.failed", "실패")
        case "denied": .ui("outcome.denied", "거부됨")
        default: .ui("outcome.unknown", "결과 불명")
        }
    }
}
