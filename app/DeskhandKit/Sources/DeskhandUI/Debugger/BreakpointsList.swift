import DeskhandClient
import SwiftUI

/// The session's breakpoints: each with its switch, where it bound or that it waits for its script,
/// and its condition; a row opens its location. The header adds one and turns them all on or off.
struct BreakpointsList: View {
    let session: DebugSessionModel?
    let add: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            DebugPaneHeader(title: String.ui("debugger.breakpoints", "중단점"), detail: countText) {
                if let session {
                    Button {
                        session.setBreakpointsActive(!session.info.breakpointsActive)
                    } label: {
                        Image(systemName: session.info.breakpointsActive ? "arrowshape.forward.fill" : "arrowshape.forward")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundStyle(session.info.breakpointsActive ? AnyShapeStyle(DS.Code.breakpoint) : AnyShapeStyle(.secondary))
                            .frame(width: 18, height: 18)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .disabled(session.isClosed || session.info.breakpoints.isEmpty)
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
                        .frame(width: 18, height: 18)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(session == nil || session?.isClosed == true)
                .help(String.ui("debugger.breakpoint.add.help", "스크립트와 줄로 중단점을 추가합니다"))
                .accessibilityLabel(String.ui("debugger.breakpoint.add", "중단점 추가"))
            }
            content
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
                VStack(spacing: 1) {
                    if !session.info.breakpointsActive {
                        Label(String.ui("debugger.breakpoints.inactive", "중단점이 모두 꺼져 있어 멈추지 않습니다"), systemImage: "pause.circle")
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, DS.Space.s)
                            .padding(.vertical, DS.Space.xs)
                    }
                    ForEach(session.info.breakpoints) { breakpoint in
                        BreakpointRow(session: session, breakpoint: breakpoint)
                    }
                }
                .padding(DS.Space.xs)
            }
        } else {
            DebugPanePlaceholder(symbol: "arrowshape.forward",
                                 title: String.ui("debugger.breakpoints.empty", "중단점이 없습니다"),
                                 message: session == nil ? nil
                                     : String.ui("debugger.breakpoints.empty.hint", "소스의 줄 번호를 누르거나 +로 스크립트와 줄을 지정하세요."))
        }
    }
}

private struct BreakpointRow: View {
    let session: DebugSessionModel
    let breakpoint: DebugBreakpoint
    @State private var hovering = false

    var body: some View {
        let busy = session.busyBreakpoints.contains(breakpoint.breakpointID)
        HStack(alignment: .top, spacing: DS.Space.s) {
            Toggle(isOn: Binding(get: { breakpoint.enabled }, set: { session.setBreakpointEnabled(breakpoint.breakpointID, $0) })) {
                EmptyView()
            }
            .toggleStyle(.checkbox)
            .labelsHidden()
            .disabled(busy || session.isClosed)
            .accessibilityLabel(String.ui("debugger.breakpoint.enable.a11y", "\(BreakpointPattern.title(of: breakpoint)) 켜기"))
            Button {
                if let location = breakpoint.locations.first {
                    session.open(SourceFocus(targetID: location.targetID, scriptID: location.scriptID, url: location.url,
                                             line: location.line, column: location.column))
                }
            } label: {
                HStack(alignment: .top, spacing: DS.Space.s) {
                    BreakpointMarker(enabled: breakpoint.enabled, resolved: breakpoint.resolved, active: session.info.breakpointsActive,
                                     width: 18, height: 12)
                        .padding(.top, 3)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(BreakpointPattern.title(of: breakpoint))
                            .font(DebugFormat.mono.weight(.medium))
                            .foregroundStyle(breakpoint.enabled ? .primary : .secondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        detail
                    }
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(breakpoint.locations.isEmpty)
            .help(breakpoint.locations.first.map { $0.url } ?? (breakpoint.url ?? breakpoint.urlRegex ?? ""))
            if busy {
                ProgressView().controlSize(.mini)
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
        .padding(.vertical, 5)
        .background(hovering ? DS.Color.neutral.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .onHover { hovering = $0 }
        .contextMenu {
            Button(breakpoint.enabled ? String.ui("debugger.breakpoint.disable", "중단점 끄기") : String.ui("debugger.breakpoint.enable", "중단점 켜기")) {
                session.setBreakpointEnabled(breakpoint.breakpointID, !breakpoint.enabled)
            }
            Button(String.ui("debugger.breakpoint.remove", "중단점 지우기"), role: .destructive) {
                session.removeBreakpoint(breakpoint.breakpointID)
            }
        }
    }

    @ViewBuilder
    private var detail: some View {
        if let condition = breakpoint.condition, !condition.isEmpty {
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
            Text(String.ui("debugger.breakpoint.pending", "일치하는 스크립트가 아직 없습니다. 로드되면 연결됩니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        } else if breakpoint.locations.count > 1 {
            let count = String(breakpoint.locations.count)
            Text(String.ui("debugger.breakpoint.locations", "위치 \(count)곳"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        } else if breakpoint.urlRegex != nil, let location = breakpoint.locations.first {
            Text(ScriptURL.display(location.url))
                .font(DS.Font.caption)
                .foregroundStyle(.tertiary)
                .lineLimit(1)
                .truncationMode(.head)
        }
    }
}
