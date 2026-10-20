import AppKit
import DeskhandClient
import SwiftUI

/// The scopes of the selected frame as a tree: scopes open like sections, objects expand on
/// demand and their properties load then; failed loads retry and long objects load in pages.
struct VariablesView: View {
    let session: DebugSessionModel?

    var body: some View {
        VStack(spacing: 0) {
            DebugPaneHeader(title: String.ui("debugger.variables", "변수"), detail: detail)
            content
        }
        .accessibilityElement(children: .contain)
    }

    private var detail: String? {
        guard let frame = session?.selectedFrame else { return nil }
        return DebugFormat.frameName(frame.function)
    }

    @ViewBuilder
    private var content: some View {
        if let session, session.pauseState != nil {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if let exception = session.pauseState?.exception {
                        ExceptionValueRow(value: exception)
                    }
                    VariableRows(rows: session.variables.rows) { session.toggleVariable($0) }
                }
                .padding(.vertical, DS.Space.xs)
            }
        } else if let session, session.isClosed {
            DebugPanePlaceholder(symbol: "stop.circle", title: String.ui("debugger.callStack.closed", "세션이 닫혔습니다"))
        } else {
            DebugPanePlaceholder(symbol: "curlybraces",
                                 title: String.ui("debugger.variables.running", "실행 중"),
                                 message: String.ui("debugger.variables.hint", "멈추면 선택한 프레임의 변수가 여기에 나타납니다."))
        }
    }
}

/// The value the target threw, above the scopes while it is paused on an exception.
private struct ExceptionValueRow: View {
    let value: RemoteValue

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: "exclamationmark.octagon.fill")
                .font(.system(size: 11))
                .foregroundStyle(DS.Color.danger)
                .frame(width: 14)
                .accessibilityHidden(true)
            Text(String.ui("debugger.variables.exception", "예외"))
                .font(DebugFormat.mono.weight(.semibold))
                .foregroundStyle(DS.Color.danger)
            Text(value.valuePreview)
                .font(DebugFormat.mono)
                .foregroundStyle(.primary)
                .lineLimit(2)
                .textSelection(.enabled)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, 5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.danger.opacity(0.07))
        .accessibilityElement(children: .combine)
    }
}

/// Rows of a variable tree; `toggle` expands, retries or loads more.
struct VariableRows: View {
    let rows: [VariableTree.Row]
    var baseIndent: CGFloat = 0
    let toggle: (VariableTree.Row) -> Void

    var body: some View {
        ForEach(rows) { row in
            VariableRowView(row: row, baseIndent: baseIndent, toggle: toggle)
        }
    }
}

private struct VariableRowView: View {
    static let indent: CGFloat = 14

    let row: VariableTree.Row
    let baseIndent: CGFloat
    let toggle: (VariableTree.Row) -> Void
    @State private var hovering = false

    private var leading: CGFloat { DS.Space.s + baseIndent + CGFloat(row.depth) * Self.indent }

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
            .padding(.leading, leading + 16)
            .frame(height: 22)
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
            .padding(.leading, leading + 16)
            .frame(height: 22)
        case .more:
            let count = String(row.remaining)
            Button(String.ui("debugger.variables.more", "\(count)개 더 보기")) { toggle(row) }
                .buttonStyle(.link)
                .font(DS.Font.caption)
                .tint(DS.Color.accent)
                .padding(.leading, leading + 16)
                .frame(height: 22)
        }
    }

    private var failureText: String {
        if row.error?.code == "STALE_TARGET" { return .ui("debugger.variables.stale", "대상이 다시 실행되어 읽을 수 없습니다") }
        return .ui("debugger.variables.failed", "속성을 읽지 못했습니다")
    }

    private var scope: some View {
        Button {
            toggle(row)
        } label: {
            HStack(spacing: DS.Space.xs) {
                Chevron(expanded: row.isExpanded)
                Text(row.title)
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if let value = row.value {
                    ValuePreviewText(value: value)
                }
                Spacer(minLength: 0)
            }
            .padding(.leading, leading)
            .padding(.trailing, DS.Space.s)
            .frame(height: 24)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(row.title)
        .accessibilityValue(row.isExpanded ? String.ui("a11y.expanded", "펼침") : String.ui("a11y.collapsed", "접힘"))
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
            Text(row.title)
                .font(DebugFormat.mono)
                .foregroundStyle(row.propertyKind == .internal ? AnyShapeStyle(.secondary) : AnyShapeStyle(.primary))
                .lineLimit(1)
                .fixedSize()
            Text(":")
                .font(DebugFormat.mono)
                .foregroundStyle(.tertiary)
            if let value = row.value {
                ValuePreviewText(value: value)
            }
            Spacer(minLength: 0)
        }
        .padding(.leading, leading)
        .padding(.trailing, DS.Space.s)
        .frame(height: 22)
        .background(hovering ? DS.Color.neutral.opacity(0.08) : .clear)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { if row.isExpandable { toggle(row) } }
        .onHover { hovering = $0 }
        .help(row.value?.valuePreview ?? "")
        .contextMenu {
            Button(String.ui("debugger.variables.copyValue", "값 복사")) { copy(row.value?.valuePreview ?? "") }
            Button(String.ui("debugger.variables.copyName", "이름 복사")) { copy(row.title) }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(String.ui("debugger.variable.a11y", "\(row.title): \(row.value?.valuePreview ?? "")"))
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
