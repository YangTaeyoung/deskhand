import DeskhandClient
import SwiftUI

/// The sessions of the computer, each with its targets (main and renderers) and their state; the
/// selected target is the one execution commands act on. A launch or attach in flight shows as a
/// pending row.
struct TargetsList: View {
    let model: DebuggerModel
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
                        .frame(width: 18, height: 18)
                        .contentShape(Rectangle())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .disabled(model.starting != nil)
                .help(String.ui("debugger.new.help", "Electron 앱을 실행하거나 실행 중인 앱에 연결합니다"))
                .accessibilityLabel(String.ui("debugger.new", "새 디버그 세션"))
            }
            ScrollView {
                VStack(spacing: DS.Space.xs) {
                    ForEach(model.sessions) { session in
                        ViewThatFits(in: .horizontal) {
                            SessionRow(model: model, session: session, selected: session.id == model.selected?.id)
                            SessionRow(model: model, session: session, selected: session.id == model.selected?.id, compactBadge: true)
                        }
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

/// A session: the app, how it was started, its state, and its targets.
private struct SessionRow: View {
    let model: DebuggerModel
    let session: DebugSessionModel
    let selected: Bool
    var compactBadge = false
    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Button {
                model.selectedSessionID = session.id
            } label: {
                header
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("debugger.session.a11y", "\(session.info.name), \(stateTitle)"))
            if !session.isClosed {
                ForEach(session.info.targets) { target in
                    TargetRow(session: session, target: target,
                              selected: selected && session.selectedTargetID == target.id) {
                        model.selectedSessionID = session.id
                        session.select(target: target.id)
                    }
                }
            } else {
                Text(DebugFormat.closed(session.info.closeKind, exitCode: session.info.exitCode))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .padding(.leading, 36 + DS.Space.s)
                    .padding(.bottom, DS.Space.xs)
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

    private var header: some View {
        HStack(alignment: .center, spacing: DS.Space.s) {
            Image(systemName: session.info.sessionKind == .attach ? "cable.connector" : "macwindow")
                .font(.system(size: 14))
                .foregroundStyle(session.isClosed ? AnyShapeStyle(.tertiary) : AnyShapeStyle(.secondary))
                .frame(width: 28, height: 28)
                .background(DS.Color.neutral.opacity(0.12), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 1) {
                Text(session.info.name)
                    .font(DS.Font.body.weight(.semibold))
                    .foregroundStyle(session.isClosed ? .secondary : .primary)
                    .lineLimit(1)
                Text(subtitle)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: DS.Space.xs)
            if session.isClosed, hovering {
                Button {
                    model.dismiss(session.id)
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(.secondary)
                        .frame(width: 18, height: 18)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(String.ui("debugger.session.dismiss", "목록에서 지우기"))
                .accessibilityLabel(String.ui("debugger.session.dismiss", "목록에서 지우기"))
            } else {
                SessionStateBadge(session: session, compact: compactBadge)
            }
        }
        .padding(.horizontal, DS.Space.xs)
        .contentShape(Rectangle())
    }

    private var stateTitle: String {
        switch session.phase {
        case .paused: .ui("debugger.state.paused", "일시정지됨")
        case .closed: .ui("debugger.state.closed", "종료됨")
        default: session.pausedTargets.isEmpty ? .ui("debugger.state.running", "실행 중") : .ui("debugger.state.paused", "일시정지됨")
        }
    }

    /// `PID 10244 · 실행함` or `포트 9229에 연결함`.
    private var subtitle: String {
        var parts: [String] = []
        if let pid = session.info.pid {
            let number = String(pid)
            parts.append(.ui("debugger.session.pid", "PID \(number)"))
        }
        if session.info.sessionKind == .attach {
            if let port = session.info.ports.inspect ?? session.info.ports.remoteDebugging {
                let number = String(port)
                parts.append(.ui("debugger.session.attachedPort", "포트 \(number) 연결"))
            } else {
                parts.append(.ui("debugger.session.attached", "연결함"))
            }
        } else {
            parts.append(.ui("debugger.session.launched", "실행함"))
        }
        return parts.joined(separator: " · ")
    }
}

/// 일시정지 (orange), 실행 중 (green dot) or 종료됨 for a session.
struct SessionStateBadge: View {
    let session: DebugSessionModel
    /// The symbol only, with the state as its help tag.
    var compact = false

    var body: some View {
        if session.isClosed {
            badge(String.ui("debugger.state.closed", "종료됨"), tint: DS.Color.neutral, symbol: nil)
        } else if !session.pausedTargets.isEmpty {
            badge(String.ui("debugger.state.paused", "일시정지됨"), tint: DS.Color.warning, symbol: "pause.fill")
        } else {
            badge(String.ui("debugger.state.running", "실행 중"), tint: DS.Color.success, symbol: "circle.fill")
        }
    }

    private func badge(_ title: String, tint: Color, symbol: String?) -> some View {
        HStack(spacing: 3) {
            if let symbol {
                Image(systemName: symbol)
                    .font(.system(size: symbol == "circle.fill" ? 6 : 8, weight: .bold))
            }
            if !compact || symbol == nil {
                Text(title)
                    .font(.system(size: NSFont.smallSystemFontSize - 1, weight: .semibold))
            }
        }
        .help(title)
        .accessibilityLabel(title)
        .foregroundStyle(tint == DS.Color.neutral ? AnyShapeStyle(.secondary) : AnyShapeStyle(tint))
        .padding(.horizontal, 6)
        .frame(height: 16)
        .background(tint.opacity(0.14), in: Capsule())
        .fixedSize()
    }
}

/// A target of a session: its role, title and whether it runs, is paused or is not attached.
private struct TargetRow: View {
    let session: DebugSessionModel
    let target: DebugTarget
    let selected: Bool
    let select: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: select) {
            HStack(spacing: DS.Space.s) {
                RoleBadge(target: target, inverted: selected)
                Text(DebugFormat.targetTitle(target))
                    .font(DS.Font.body)
                    .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.primary))
                    .lineLimit(1)
                    .truncationMode(.tail)
                Spacer(minLength: DS.Space.xs)
                state
            }
            .padding(.leading, 36 + DS.Space.xs)
            .padding(.trailing, DS.Space.s)
            .frame(height: 26)
            .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .contextMenu {
            if !target.attached, !session.isClosed {
                Button(String.ui("debugger.target.attach", "이 대상에 붙기")) { session.attachTarget(target.id) }
            }
        }
        .help(String.ui("debugger.target.help", "\(DebugFormat.targetTitle(target)) · \(target.url)"))
        .accessibilityLabel(String.ui("debugger.target.a11y", "\(DebugFormat.role(target)) \(DebugFormat.targetTitle(target)), \(stateText)"))
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.accent) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.12)) : AnyShapeStyle(.clear)
    }

    private var stateText: String {
        if !target.attached { return .ui("debugger.target.detached", "연결 안 됨") }
        return session.isPaused(target.id) ? .ui("debugger.state.paused", "일시정지됨") : .ui("debugger.state.running", "실행 중")
    }

    @ViewBuilder
    private var state: some View {
        if !target.attached {
            Button(String.ui("debugger.target.attachShort", "붙기")) { session.attachTarget(target.id) }
                .buttonStyle(.link)
                .font(DS.Font.caption)
                .tint(selected ? DS.Color.onFill : DS.Color.accent)
                .disabled(session.isClosed)
                .help(String.ui("debugger.target.detached.help", "이 대상에는 아직 붙지 않았습니다. 누르면 붙어 중단점과 콘솔을 받습니다."))
        } else if session.isPaused(target.id) {
            Image(systemName: "pause.fill")
                .font(.system(size: 9, weight: .bold))
                .foregroundStyle(selected ? DS.Color.onFill : DS.Color.warning)
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
                .frame(width: 28, height: 28)
            VStack(alignment: .leading, spacing: 1) {
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
