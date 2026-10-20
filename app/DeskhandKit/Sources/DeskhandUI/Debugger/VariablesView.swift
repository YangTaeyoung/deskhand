import AppKit
import DeskhandClient
import SwiftUI

/// The scopes of the selected frame as a tree, with a thrown exception first: scopes open like
/// sections, objects expand on demand and their properties load then; failed loads retry and
/// long objects load in pages. While the tree has the keyboard ↑ and ↓ move the selection and
/// → and ← expand and collapse it.
struct VariablesView: View {
    let session: DebugSessionModel?
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            DebugPaneHeader(title: String.ui("debugger.variables", "변수"), detail: detail)
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        }
        .accessibilityElement(children: .contain)
    }

    private var detail: String? {
        guard let frame = session?.selectedFrame else { return nil }
        return DebugFormat.frameName(frame.function)
    }

    @ViewBuilder
    private var content: some View {
        if let session, let state = session.pauseState {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if let exception = state.exception, exception.objectID == nil, session.selectedFrameIndex == 0 {
                        ExceptionValueRow(value: exception)
                    }
                    VariableRows(rows: session.variables.rows, selected: session.selectedVariable,
                                 select: { row in
                                     focused = true
                                     session.selectedVariable = row.id
                                 },
                                 toggle: { session.toggleVariable($0) })
                }
                .padding(.vertical, DS.Space.xs)
            }
            .focusable()
            .focused($focused)
            .onKeyPress(.upArrow) {
                session.moveVariableSelection(by: -1)
                return .handled
            }
            .onKeyPress(.downArrow) {
                session.moveVariableSelection(by: 1)
                return .handled
            }
            .onKeyPress(.rightArrow) {
                session.setSelectedVariableExpanded(true)
                return .handled
            }
            .onKeyPress(.leftArrow) {
                session.setSelectedVariableExpanded(false)
                return .handled
            }
        } else if let session, session.isClosed {
            DebugPaneHint(text: String.ui("debugger.callStack.closed", "세션이 닫혔습니다."))
        } else if let session, session.stateFailure != nil {
            DebugPaneHint(text: String.ui("debugger.variables.unread", "멈춘 위치를 읽지 못해 변수를 볼 수 없습니다."))
        } else if let session, session.awaitingState {
            DebugPaneHint(text: String.ui("debugger.variables.loading", "불러오는 중…"))
        } else {
            DebugPaneHint(text: String.ui("debugger.variables.hint", "멈추면 선택한 프레임의 변수가 여기에 나타납니다."))
        }
    }
}

/// A thrown value without properties, above the scopes while the target is paused on it.
private struct ExceptionValueRow: View {
    let value: RemoteValue

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: "exclamationmark.octagon.fill")
                .font(.system(size: 11))
                .foregroundStyle(DS.Color.danger)
                .frame(width: 12)
                .accessibilityHidden(true)
            Text(String.ui("debugger.variables.exception", "예외"))
                .font(DebugFormat.mono.weight(.semibold))
                .foregroundStyle(DS.Code.errorText)
            Text(value.valuePreview)
                .font(DebugFormat.mono)
                .foregroundStyle(.primary)
                .lineLimit(2)
                .textSelection(.enabled)
        }
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Code.errorRow)
        .accessibilityElement(children: .combine)
    }
}

/// Rows of a variable tree; `toggle` expands, retries or loads more, and `select` picks a row.
struct VariableRows: View {
    let rows: [VariableTree.Row]
    var baseIndent: CGFloat = 0
    var selected: VariableTree.Row.ID?
    var select: ((VariableTree.Row) -> Void)?
    let toggle: (VariableTree.Row) -> Void

    var body: some View {
        ForEach(rows) { row in
            VariableRowView(row: row, baseIndent: baseIndent, selected: row.id == selected, select: select, toggle: toggle)
        }
    }
}

private struct VariableRowView: View {
    static let indent: CGFloat = 16
    static let height: CGFloat = 24

    let row: VariableTree.Row
    let baseIndent: CGFloat
    let selected: Bool
    let select: ((VariableTree.Row) -> Void)?
    let toggle: (VariableTree.Row) -> Void
    @State private var hovering = false

    private var leading: CGFloat { DS.Space.s + baseIndent + CGFloat(row.depth) * Self.indent }

    /// Where loading, failure and more rows start: past the chevron of their parent's children.
    private var childLeading: CGFloat { leading + 12 + DS.Space.xs }

    var body: some View {
        switch row.kind {
        case .scope:
            scope
        case .property:
            property
        case .loading:
            HStack(spacing: DS.Space.xs) {
                ProgressView().controlSize(.mini)
                Text(String.ui("debugger.variables.loading", "불러오는 중…"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.leading, childLeading)
            .frame(height: Self.height)
            .accessibilityElement(children: .combine)
        case .failure:
            HStack(spacing: DS.Space.xs) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
                Text(failureText)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .help(row.error?.detail ?? "")
                Button(String.ui("action.retry", "다시 시도")) { toggle(row) }
                    .buttonStyle(.link)
                    .font(DS.Font.caption)
                    .tint(DS.Color.accent)
            }
            .padding(.leading, childLeading)
            .frame(height: Self.height)
        case .more:
            HStack(spacing: DS.Space.xs) {
                if row.busy { ProgressView().controlSize(.mini) }
                let count = String(row.remaining)
                Button(String.ui("debugger.variables.more", "\(count)개 더 보기")) { toggle(row) }
                    .buttonStyle(.link)
                    .font(DS.Font.caption)
                    .tint(DS.Color.accent)
                    .disabled(row.busy)
            }
            .padding(.leading, childLeading)
            .frame(height: Self.height)
        }
    }

    private var failureText: String {
        if row.error?.code == "STALE_TARGET" { return .ui("debugger.variables.stale", "대상이 다시 실행되어 읽을 수 없습니다") }
        return .ui("debugger.variables.failed", "속성을 읽지 못했습니다")
    }

    private var isException: Bool { row.path == ["exception"] }

    private var scope: some View {
        Button {
            select?(row)
            toggle(row)
        } label: {
            HStack(spacing: DS.Space.xs) {
                Chevron(expanded: row.isExpanded)
                if isException {
                    Image(systemName: "exclamationmark.octagon.fill")
                        .font(.system(size: 11))
                        .foregroundStyle(DS.Color.danger)
                        .accessibilityHidden(true)
                }
                Text(row.title)
                    .font(isException ? DebugFormat.mono.weight(.semibold) : DS.Font.caption.weight(.semibold))
                    .foregroundStyle(isException ? AnyShapeStyle(DS.Code.errorText) : AnyShapeStyle(.secondary))
                    .lineLimit(1)
                    .fixedSize()
                if let value = row.value {
                    ValuePreviewText(value: value)
                }
                Spacer(minLength: 0)
            }
            .padding(.leading, leading)
            .padding(.trailing, DS.Space.s)
            .frame(height: Self.height)
            .background(rowBackground)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(row.title)
        .accessibilityValue(row.isExpanded ? String.ui("a11y.expanded", "펼침") : String.ui("a11y.collapsed", "접힘"))
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private var rowBackground: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Code.selection) }
        if isException { return AnyShapeStyle(DS.Code.errorRow) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.08)) : AnyShapeStyle(.clear)
    }

    private var property: some View {
        HStack(spacing: DS.Space.xs) {
            if row.isExpandable {
                Button {
                    toggle(row)
                } label: {
                    Chevron(expanded: row.isExpanded)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(row.isExpanded ? String.ui("debugger.variables.collapse", "접기") : String.ui("debugger.variables.expand", "펼치기"))
            } else {
                Color.clear.frame(width: 12, height: 12)
            }
            NameValueLayout {
                HStack(spacing: 0) {
                    Text(row.title)
                        .font(DebugFormat.mono)
                        .foregroundStyle(row.propertyKind == .internal ? AnyShapeStyle(.secondary) : AnyShapeStyle(.primary))
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .help(row.title)
                    Text(":")
                        .font(DebugFormat.mono)
                        .foregroundStyle(.secondary)
                        .fixedSize()
                }
                Group {
                    if let value = row.value {
                        ValuePreviewText(value: value)
                    } else {
                        Color.clear.frame(height: 1)
                    }
                }
            }
        }
        .padding(.leading, leading)
        .padding(.trailing, DS.Space.s)
        .frame(height: Self.height)
        .background(rowBackground)
        .contentShape(Rectangle())
        .onTapGesture { select?(row) }
        .onHover { hovering = $0 }
        .contextMenu {
            Button(String.ui("debugger.variables.copyValue", "값 복사")) { copy(row.value?.valuePreview ?? "") }
            Button(String.ui("debugger.variables.copyName", "이름 복사")) { copy(row.title) }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(String.ui("debugger.variable.a11y", "\(row.title): \(row.value?.valuePreview ?? "")"))
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private func copy(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}

/// A disclosure chevron that turns down when expanded.
struct Chevron: View {
    let expanded: Bool

    var body: some View {
        Image(systemName: "chevron.right")
            .font(.system(size: 9, weight: .bold))
            .foregroundStyle(.secondary)
            .rotationEffect(.degrees(expanded ? 90 : 0))
            .frame(width: 12, height: 12)
            .accessibilityHidden(true)
    }
}
