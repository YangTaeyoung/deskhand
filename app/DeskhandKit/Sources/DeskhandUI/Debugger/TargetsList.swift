import DeskhandClient
import SwiftUI

/// The sessions of the computer, each with its targets (main and renderers) and their state; the
/// selected target is the one execution commands act on. A launch or attach in flight shows as a
/// pending row. New sessions start only while the computer is connected (`enabled`).
struct TargetsList: View {
    let model: DebuggerModel
    var enabled = true
    /// Why new sessions cannot start, while not `enabled`.
    var disabledReason: String?
    let launch: () -> Void
    let attach: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            DebugPaneHeader(title: String.ui("debugger.sessions", "세션"), detail: countText) {
                Menu {
                    Button(String.ui("debugger.launch.menu", "앱 실행…"), systemImage: "play.rectangle", action: launch)
                    Button(String.ui("debugger.attach.menu", "실행 중인 앱에 연결…"), systemImage: "cable.connector", action: attach)
                } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 12, weight: .semibold))
                        .frame(width: 20, height: 20)
                        .contentShape(Rectangle())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .disabled(model.starting != nil || !enabled)
                .help(enabled ? String.ui("debugger.new.help", "Electron 앱을 실행하거나 실행 중인 앱에 연결합니다")
                              : (disabledReason ?? String.ui("debugger.new.offline", "PC에 연결되어 있지 않아 새 세션을 시작할 수 없습니다")))
                .accessibilityLabel(String.ui("debugger.new", "새 디버그 세션"))
            }
            ScrollView {
                VStack(spacing: DS.Space.xs) {
                    ForEach(model.sessions) { session in
                        SessionRow(model: model, session: session, selected: session.id == model.selected?.id)
                    }
                    if let starting = model.starting {
                        StartingRow(starting: starting)
                    }
                }
                .padding(DS.Space.s)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("debugger.sessions.a11y", "디버그 세션과 대상"))
    }

    private var countText: String? {
        let open = model.openSessions.count
        guard open > 0 else { return nil }
        let number = String(open)
        return .ui("debugger.sessions.count", "\(number)개 열림")
    }
}

/// A session: the app, how and by whom it was started, its state, and its targets.
private struct SessionRow: View {
    static let iconSize: CGFloat = 28

    let model: DebuggerModel
    let session: DebugSessionModel
    let selected: Bool
    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            Button {
                model.selectedSessionID = session.id
            } label: {
                header
            }
            .buttonStyle(.plain)
            .overlay(alignment: .topTrailing) { dismissButton }
            .accessibilityLabel(String.ui("debugger.session.a11y", "\(session.info.name), \(SessionStateBadge.title(session))"))
            if !session.isClosed {
                VStack(spacing: 0) {
                    ForEach(session.info.targets) { target in
                        TargetRow(session: session, target: target, selected: selected && session.selectedTargetID == target.id) {
                            model.selectedSessionID = session.id
                            session.select(target: target.id)
                        }
                    }
                }
                if let reason = session.info.mainUnavailable {
                    UnavailableLine(text: DebugFormat.mainUnavailableShort(reason), detail: DebugFormat.mainUnavailable(reason))
                }
                if let reason = session.info.rendererUnavailable {
                    UnavailableLine(text: DebugFormat.rendererUnavailableShort(reason), detail: DebugFormat.rendererUnavailable(reason))
                }
            } else {
                Text(DebugFormat.closedShort(session.info.closeKind, exitCode: session.info.exitCode, kind: session.info.sessionKind))
                    .help(DebugFormat.closed(session.info.closeKind, exitCode: session.info.exitCode, kind: session.info.sessionKind))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .padding(.leading, Self.iconSize + DS.Space.s + DS.Space.xs)
                    .padding(.trailing, DS.Space.s)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, DS.Space.xs)
        .background(selected ? DS.Color.neutral.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .onHover { hovering = $0 }
        .contextMenu {
            if session.isClosed {
                Button(String.ui("debugger.session.dismiss", "목록에서 지우기")) { model.dismiss(session.id) }
            }
        }
    }

    /// The app icon, the name with the state badge, and how and by whom it was started.
    private var header: some View {
        HStack(alignment: .top, spacing: DS.Space.s) {
            Image(systemName: session.info.sessionKind == .attach ? "cable.connector" : "macwindow")
                .font(.system(size: 14))
                .foregroundStyle(.secondary)
                .frame(width: Self.iconSize, height: Self.iconSize)
                .background(DS.Color.neutral.opacity(0.12), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: DS.Space.xs) {
                    Text(session.info.name)
                        .font(DS.Font.body.weight(.semibold))
                        .foregroundStyle(session.isClosed ? .secondary : .primary)
                        .lineLimit(1)
                    Spacer(minLength: DS.Space.xs)
                    SessionStateBadge(session: session)
                        .opacity(session.isClosed && hovering ? 0 : 1)
                }
                Text(subtitle)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .help(creatorHelp)
            }
        }
        .padding(.horizontal, DS.Space.xs)
        .contentShape(Rectangle())
    }

    /// Removes a closed session, over its badge while the pointer is on the row.
    @ViewBuilder
    private var dismissButton: some View {
        if session.isClosed, hovering {
            Button {
                model.dismiss(session.id)
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.trailing, DS.Space.xs)
            .help(String.ui("debugger.session.dismiss", "목록에서 지우기"))
            .accessibilityLabel(String.ui("debugger.session.dismiss", "목록에서 지우기"))
        }
    }

    private var startedByAI: Bool { session.info.createdBy.actor == "ai" }

    /// `PID 10244 · 실행함`, `포트 9229 연결함` or `PID 10244 · AI가 실행함`.
    private var subtitle: String {
        var parts: [String] = []
        if let pid = session.info.pid {
            let number = String(pid)
            parts.append(.ui("debugger.session.pid", "PID \(number)"))
        }
        if session.info.sessionKind == .attach {
            if session.info.pid == nil, let port = session.info.ports.inspect ?? session.info.ports.remoteDebugging {
                let number = String(port)
                parts.append(.ui("debugger.session.attachedPort", "포트 \(number)"))
            }
            parts.append(startedByAI ? .ui("debugger.session.attachedByAI", "AI가 연결함") : .ui("debugger.session.attached", "연결함"))
        } else {
            parts.append(startedByAI ? .ui("debugger.session.launchedByAI", "AI가 실행함") : .ui("debugger.session.launched", "실행함"))
        }
        return parts.joined(separator: " · ")
    }

    private var creatorHelp: String {
        guard let client = session.info.createdBy.clientName, !client.isEmpty else { return subtitle }
        return .ui("debugger.session.createdBy", "\(client)에서 시작한 세션")
    }
}

/// Why a session lacks main or a renderer, in one line under its targets with the full reason as
/// its help tag.
private struct UnavailableLine: View {
    let text: String
    let detail: String

    var body: some View {
        Label(text, systemImage: "info.circle")
            .font(DS.Font.caption)
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .truncationMode(.tail)
            .padding(.leading, SessionRow.iconSize + DS.Space.s + DS.Space.xs)
            .padding(.trailing, DS.Space.s)
            .help(detail)
            .accessibilityLabel(detail)
    }
}

/// 일시정지됨 (orange), 실행 중 (green dot), 연결 해제됨, 연결 끊김 or 종료됨 for a session, in a
/// column of one width. An attach session ends without ending its app: detached, or cut off with
/// the PC.
struct SessionStateBadge: View {
    static let width: CGFloat = 64

    let session: DebugSessionModel

    static func title(_ session: DebugSessionModel) -> String {
        if session.isClosed {
            if session.info.sessionKind == .attach {
                switch session.info.closeKind {
                case .requested: return .ui("debugger.state.detached", "연결 해제됨")
                case .connectionLost: return .ui("debugger.state.connectionLost", "연결 끊김")
                default: break
                }
            }
            return .ui("debugger.state.closed", "종료됨")
        }
        return session.pausedTargets.isEmpty ? .ui("debugger.state.running", "실행 중") : .ui("debugger.state.paused", "일시정지됨")
    }

    var body: some View {
        let title = Self.title(session)
        if session.isClosed {
            badge(title, tint: DS.Color.neutral, symbol: nil)
        } else if !session.pausedTargets.isEmpty {
            badge(title, tint: DS.Color.warning, symbol: "pause.fill")
        } else {
            badge(title, tint: DS.Color.success, symbol: "circle.fill")
        }
    }

    private func badge(_ title: String, tint: Color, symbol: String?) -> some View {
        HStack(spacing: DS.Space.xs) {
            if let symbol {
                Image(systemName: symbol)
                    .font(.system(size: symbol == "circle.fill" ? 6 : 8, weight: .bold))
            }
            Text(title)
                .font(.system(size: NSFont.smallSystemFontSize - 1, weight: .semibold))
                .lineLimit(1)
        }
        .help(title)
        .accessibilityLabel(title)
        .foregroundStyle(tint == DS.Color.neutral ? AnyShapeStyle(.secondary) : AnyShapeStyle(tint))
        .padding(.horizontal, DS.Space.s)
        .frame(minWidth: Self.width)
        .frame(height: 16)
        .background(tint.opacity(0.14), in: Capsule())
        .fixedSize()
    }
}

/// A target of a session: its role, title and whether it runs or is paused; a target the daemon
/// left detached has a 연결 button beside it.
private struct TargetRow: View {
    let session: DebugSessionModel
    let target: DebugTarget
    let selected: Bool
    let select: () -> Void
    @State private var hovering = false

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Button(action: select) {
                HStack(spacing: DS.Space.s) {
                    RoleBadge(target: target, inverted: selected, fixedWidth: true)
                    Text(DebugFormat.targetTitle(target))
                        .font(DS.Font.body)
                        .foregroundStyle(titleStyle)
                        .lineLimit(1)
                        .truncationMode(.tail)
                    Spacer(minLength: DS.Space.xs)
                    stateSymbol
                        .frame(width: 12)
                }
                .padding(.leading, DS.Space.xs)
                .padding(.trailing, DS.Space.s)
                .frame(height: 28)
                .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(String.ui("debugger.target.help", "\(DebugFormat.targetTitle(target)) · \(target.url)"))
            .accessibilityLabel(String.ui("debugger.target.a11y", "\(DebugFormat.role(target)) \(DebugFormat.targetTitle(target)), \(stateText)"))
            .accessibilityAddTraits(selected ? [.isSelected] : [])
            if !target.attached {
                Button(String.ui("debugger.target.attach", "연결")) { session.attachTarget(target.id) }
                    .controlSize(.small)
                    .disabled(session.isClosed)
                    .help(String.ui("debugger.target.detached.help", "이 대상에는 아직 연결하지 않았습니다. 연결하면 중단점과 콘솔을 받습니다."))
                    .accessibilityLabel(String.ui("debugger.target.attach.a11y", "\(DebugFormat.targetTitle(target))에 연결"))
            }
        }
        .padding(.leading, SessionRow.iconSize + DS.Space.s)
        .padding(.trailing, DS.Space.xs)
        .onHover { hovering = $0 }
    }

    private var titleStyle: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.onFill) }
        return target.attached ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary)
    }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.accent) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.12)) : AnyShapeStyle(.clear)
    }

    private var stateText: String {
        if !target.attached { return .ui("debugger.target.detached", "연결 안 됨") }
        if session.isPaused(target.id) { return .ui("debugger.state.paused", "일시정지됨") }
        if target.pausePending { return .ui("debugger.target.pausePending", "일시정지 기다리는 중") }
        return .ui("debugger.state.running", "실행 중")
    }

    @ViewBuilder
    private var stateSymbol: some View {
        if !target.attached {
            Image(systemName: "circle.dashed")
                .font(.system(size: 9))
                .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
        } else if session.isPaused(target.id) {
            Image(systemName: "pause.fill")
                .font(.system(size: 9, weight: .bold))
                .foregroundStyle(selected ? DS.Color.onFill : DS.Color.warning)
        } else if target.pausePending {
            Image(systemName: "pause.circle")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(selected ? DS.Color.onFill : DS.Color.warning)
                .help(String.ui("debugger.target.pausePending.help", "일시정지를 요청했습니다. 다음 코드가 실행되면 멈춥니다."))
        } else {
            Image(systemName: "circle.fill")
                .font(.system(size: 6))
                .foregroundStyle(selected ? DS.Color.onFill : DS.Color.success)
        }
    }
}

/// A launch or attach in flight.
private struct StartingRow: View {
    let starting: DebuggerModel.Starting

    var body: some View {
        HStack(spacing: DS.Space.s) {
            ProgressView()
                .controlSize(.small)
                .frame(width: SessionRow.iconSize, height: SessionRow.iconSize)
            VStack(alignment: .leading, spacing: 0) {
                Text(starting.name)
                    .font(DS.Font.body.weight(.semibold))
                    .lineLimit(1)
                Text(starting.kind == .launch ? String.ui("debugger.starting.launch", "디버그 포트를 열고 실행하는 중…")
                                              : String.ui("debugger.starting.attach", "검사기에 연결하는 중…"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(DS.Space.xs)
        .accessibilityElement(children: .combine)
    }
}
