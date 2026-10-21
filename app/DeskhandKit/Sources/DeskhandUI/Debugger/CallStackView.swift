import DeskhandClient
import SwiftUI

/// The paused target's call stack, innermost first; selecting a frame shows its source and
/// scopes, and ↑ and ↓ move the selection while the list has the keyboard.
struct CallStackView: View {
    static let rowHeight: CGFloat = 24
    /// Rows the pane is tall at least.
    static let minimumRows = 3

    let session: DebugSessionModel?
    @FocusState private var focused: Bool

    /// The pane's height for `frames` frames: the header and at least `minimumRows` rows.
    static func idealHeight(frames: Int) -> CGFloat {
        DebugPaneHeader<EmptyView>.height + CGFloat(max(minimumRows, frames)) * rowHeight + DS.Space.xs * 2
    }

    var body: some View {
        VStack(spacing: 0) {
            DebugPaneHeader(title: String.ui("debugger.callStack", "호출 스택"), detail: detail)
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        }
        .accessibilityElement(children: .contain)
    }

    static func isException(_ reason: DebugPauseReason?) -> Bool {
        reason == .exception || reason == .promiseRejection
    }

    private var detail: String? {
        guard let target = session?.selectedTarget else { return nil }
        return DebugFormat.targetTitle(target)
    }

    @ViewBuilder
    private var content: some View {
        if let session, let state = session.pauseState {
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(Array(state.callFrames.enumerated()), id: \.element.frameID) { index, frame in
                        FrameRow(index: index, frame: frame, selected: index == session.selectedFrameIndex,
                                 exception: index == 0 && Self.isException(state.pauseReason)) {
                            focused = true
                            session.selectFrame(index)
                        }
                    }
                }
                .padding(DS.Space.xs)
            }
            .focusable()
            .focused($focused)
            .onKeyPress(.upArrow) {
                session.moveFrameSelection(by: -1)
                return .handled
            }
            .onKeyPress(.downArrow) {
                session.moveFrameSelection(by: 1)
                return .handled
            }
        } else if let session, session.isClosed {
            DebugPaneHint(text: String.ui("debugger.callStack.closed", "세션이 닫혔습니다."))
        } else if let session, session.stateFailure != nil {
            DebugPaneHint(text: String.ui("debugger.callStack.failed", "호출 스택을 불러오지 못했습니다."))
        } else if let session, session.awaitingState {
            DebugPaneHint(text: String.ui("debugger.callStack.loading", "호출 스택을 불러오는 중…"))
        } else {
            DebugPaneHint(text: String.ui("debugger.callStack.hint", "멈추면 호출 스택이 여기에 나타납니다."))
        }
    }
}

private struct FrameRow: View {
    let index: Int
    let frame: DebugCallFrame
    let selected: Bool
    let exception: Bool
    let select: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: select) { label }
            .buttonStyle(.plain)
            .onHover { hovering = $0 }
            .help(tooltip)
            .accessibilityLabel(accessibilityText)
            .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private var tooltip: String {
        let line = String(frame.line)
        let column = String(frame.column)
        return frame.url + ":" + line + ":" + column
    }

    private var accessibilityText: String {
        let name = DebugFormat.frameName(frame.function)
        let location = DebugFormat.location(url: frame.url, line: frame.line)
        return .ui("debugger.frame.a11y", "\(name), \(location)")
    }

    private var label: some View {
        HStack(spacing: DS.Space.s) {
            marker
                .frame(width: 16)
            name
            Spacer(minLength: DS.Space.s)
            locationText
        }
        .padding(.horizontal, DS.Space.s)
        .frame(height: CallStackView.rowHeight)
        .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .contentShape(Rectangle())
    }

    private var name: some View {
        Text(DebugFormat.frameName(frame.function))
            .font(DS.Font.body.weight(index == 0 ? .semibold : .regular))
            .italic(frame.function.isEmpty)
            .foregroundStyle(nameStyle)
            .lineLimit(1)
            .truncationMode(.tail)
            .layoutPriority(1)
    }

    private var locationText: some View {
        let style: AnyShapeStyle = selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary)
        return Text(DebugFormat.location(url: frame.url, line: frame.line))
            .font(.system(size: 11, design: .monospaced))
            .foregroundStyle(style)
            .lineLimit(1)
            .truncationMode(.head)
    }

    private var nameStyle: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.onFill) }
        return frame.function.isEmpty ? AnyShapeStyle(.secondary) : AnyShapeStyle(.primary)
    }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Status.selectionFill) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.12)) : AnyShapeStyle(.clear)
    }

    @ViewBuilder
    private var marker: some View {
        if index == 0 {
            Image(systemName: exception ? "exclamationmark.octagon.fill" : "arrowtriangle.right.fill")
                .font(.system(size: exception ? 11 : 9))
                .foregroundStyle(selected ? DS.Color.onFill : (exception ? DS.Color.danger : DS.Color.success))
        } else {
            Text(String(index))
                .font(.system(size: 10, design: .monospaced))
                .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
        }
    }
}
