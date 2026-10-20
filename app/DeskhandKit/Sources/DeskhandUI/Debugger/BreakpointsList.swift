import DeskhandClient
import SwiftUI

/// The session's breakpoints: each with its marker, which turns it on or off, the line it bound
/// to or that it waits for its script, and its condition; a row selects it and opens its
/// location, ⌫ removes the selected one, and the context menu turns it on or off, edits its
/// condition or removes it. The header adds one and turns them all on or off.
struct BreakpointsList: View {
    let model: DebuggerModel
    let session: DebugSessionModel?
    let add: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            DebugPaneHeader(title: String.ui("debugger.breakpoints", "중단점"), detail: countText) {
                if let session {
                    let usable = !session.isClosed && !session.info.breakpoints.isEmpty
                    Button {
                        session.setBreakpointsActive(!session.info.breakpointsActive)
                    } label: {
                        Image(systemName: session.info.breakpointsActive ? "arrowshape.forward.fill" : "arrowshape.forward")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundStyle(session.info.breakpointsActive && usable ? AnyShapeStyle(DS.Code.breakpoint) : AnyShapeStyle(.tertiary))
                            .frame(width: 20, height: 20)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!usable)
                    .help(session.info.breakpointsActive
                          ? String.ui("debugger.breakpoints.deactivate.help", "모든 중단점을 잠시 끕니다 (⌘Y)")
                          : String.ui("debugger.breakpoints.activate.help", "중단점을 다시 켭니다 (⌘Y)"))
                    .accessibilityLabel(session.info.breakpointsActive
                                        ? String.ui("debugger.breakpoints.deactivate", "모든 중단점 끄기")
                                        : String.ui("debugger.breakpoints.activate", "중단점 켜기"))
                }
                Button(action: add) {
                    Image(systemName: "plus")
                        .font(.system(size: 12, weight: .semibold))
                        .frame(width: 20, height: 20)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(session == nil || session?.isClosed == true)
                .help(String.ui("debugger.breakpoint.add.help", "스크립트와 줄로 중단점을 추가합니다"))
                .accessibilityLabel(String.ui("debugger.breakpoint.add", "중단점 추가"))
            }
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        }
        .accessibilityElement(children: .contain)
    }

    private var countText: String? {
        guard let session, !session.info.breakpoints.isEmpty else { return nil }
        let count = String(session.info.breakpoints.count)
        return session.info.breakpointsActive ? count : .ui("debugger.breakpoints.off", "\(count) · 모두 꺼짐")
    }

    @ViewBuilder
    private var content: some View {
        if let session, !session.info.breakpoints.isEmpty {
            ScrollView {
                VStack(spacing: 0) {
                    if !session.info.breakpointsActive {
                        Label(String.ui("debugger.breakpoints.inactive", "중단점이 모두 꺼져 있어 멈추지 않습니다"), systemImage: "pause.circle")
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, DS.Space.s)
                            .padding(.vertical, DS.Space.xs)
                    }
                    ForEach(session.info.breakpoints) { breakpoint in
                        BreakpointRow(model: model, session: session, breakpoint: breakpoint,
                                      selected: session.selectedBreakpointID == breakpoint.breakpointID) {
                            focused = true
                        }
                    }
                }
                .padding(DS.Space.xs)
            }
            .focusable()
            .focusEffectDisabled()
            .focused($focused)
            .onDeleteCommand { if !session.isClosed { session.removeSelectedBreakpoint() } }
            .onKeyPress(.upArrow) {
                moveSelection(session, by: -1)
                return .handled
            }
            .onKeyPress(.downArrow) {
                moveSelection(session, by: 1)
                return .handled
            }
        } else if session != nil {
            DebugPaneHint(text: String.ui("debugger.breakpoints.empty.hint", "중단점이 없습니다. 소스의 줄 번호를 누르거나 +로 스크립트와 줄을 지정하세요."))
        } else {
            DebugPaneHint(text: String.ui("debugger.breakpoints.empty", "중단점이 없습니다."))
        }
    }

    private func moveSelection(_ session: DebugSessionModel, by delta: Int) {
        let ids = session.info.breakpoints.map(\.breakpointID)
        guard !ids.isEmpty else { return }
        let index = ids.firstIndex { $0 == session.selectedBreakpointID } ?? (delta > 0 ? -1 : ids.count)
        session.selectedBreakpointID = ids[min(max(index + delta, 0), ids.count - 1)]
    }
}

private struct BreakpointRow: View {
    let model: DebuggerModel
    let session: DebugSessionModel
    let breakpoint: DebugBreakpoint
    let selected: Bool
    let focus: () -> Void
    @State private var hovering = false

    private var conditional: Bool { !(breakpoint.condition ?? "").isEmpty }

    var body: some View {
        let busy = session.busyBreakpoints.contains(breakpoint.breakpointID)
        HStack(alignment: .top, spacing: DS.Space.s) {
            Button {
                session.setBreakpointEnabled(breakpoint.breakpointID, !breakpoint.enabled)
            } label: {
                BreakpointMarker(enabled: breakpoint.enabled, resolved: breakpoint.resolved,
                                 active: session.info.breakpointsActive && !session.isClosed, conditional: conditional, width: 24, height: 16)
                    .frame(height: 16)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(busy || session.isClosed)
            .help(breakpoint.enabled ? String.ui("debugger.breakpoint.disable.help", "누르면 이 중단점을 끕니다")
                                     : String.ui("debugger.breakpoint.enable.help", "누르면 이 중단점을 켭니다"))
            .accessibilityLabel(breakpoint.enabled ? String.ui("debugger.breakpoint.disable", "중단점 끄기")
                                                   : String.ui("debugger.breakpoint.enable", "중단점 켜기"))
            VStack(alignment: .leading, spacing: 0) {
                Text(BreakpointPattern.title(of: breakpoint))
                    .font(DebugFormat.mono.weight(.medium))
                    .foregroundStyle(breakpoint.enabled && !session.isClosed ? .primary : .secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(height: 16)
                detail
            }
            Spacer(minLength: 0)
            if busy {
                ProgressView().controlSize(.mini)
                    .frame(width: 16, height: 16)
            } else if hovering, !session.isClosed {
                Button {
                    session.removeBreakpoint(breakpoint.breakpointID)
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundStyle(.secondary)
                        .frame(width: 16, height: 16)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(String.ui("debugger.breakpoint.remove", "중단점 지우기"))
                .accessibilityLabel(String.ui("debugger.breakpoint.remove", "중단점 지우기"))
            }
        }
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .contentShape(Rectangle())
        .onTapGesture {
            focus()
            session.selectedBreakpointID = breakpoint.breakpointID
            if let location = breakpoint.locations.first {
                session.open(SourceFocus(targetID: location.targetID, scriptID: location.scriptID, url: location.url,
                                         line: location.line, column: location.column))
            }
        }
        .onHover { hovering = $0 }
        .help(helpText)
        .contextMenu {
            if !session.isClosed {
                Button(breakpoint.enabled ? String.ui("debugger.breakpoint.disable", "중단점 끄기") : String.ui("debugger.breakpoint.enable", "중단점 켜기")) {
                    session.setBreakpointEnabled(breakpoint.breakpointID, !breakpoint.enabled)
                }
                Button(String.ui("debugger.breakpoint.editCondition", "조건 편집…")) { model.sheet = .condition(breakpoint.breakpointID) }
                Divider()
                Button(String.ui("debugger.breakpoint.remove", "중단점 지우기"), role: .destructive) {
                    session.removeBreakpoint(breakpoint.breakpointID)
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(BreakpointPattern.title(of: breakpoint))
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.accent.opacity(0.16)) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.08)) : AnyShapeStyle(.clear)
    }

    /// The script URL, and the line asked for when the breakpoint bound to another line.
    private var helpText: String {
        let script = breakpoint.locations.first?.url ?? breakpoint.pattern
        let bound = BreakpointPattern.line(of: breakpoint)
        guard bound != breakpoint.line else { return script }
        let requested = String(breakpoint.line)
        let resolved = String(bound)
        return script + "\n" + String.ui("debugger.breakpoint.moved", "\(requested)번 줄에 걸었고, 실행할 수 있는 \(resolved)번 줄에 연결되었습니다")
    }

    @ViewBuilder
    private var detail: some View {
        if conditional, let condition = breakpoint.condition {
            HStack(spacing: DS.Space.xs) {
                Text(String.ui("debugger.breakpoint.when", "조건"))
                    .foregroundStyle(.secondary)
                Text(condition)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            .font(DS.Font.caption)
        }
        if !breakpoint.resolved {
            Text(String.ui("debugger.breakpoint.pending", "스크립트가 로드되면 연결됩니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        } else if breakpoint.locations.count > 1 {
            let count = String(breakpoint.locations.count)
            Text(String.ui("debugger.breakpoint.locations", "위치 \(count)곳"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        } else if breakpoint.matchKind != .url, let location = breakpoint.locations.first {
            Text(ScriptURL.display(location.url))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.head)
        }
    }
}
