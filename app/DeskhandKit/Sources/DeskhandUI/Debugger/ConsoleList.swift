import AppKit
import DeskhandClient
import SwiftUI

/// Console rows kept scrolled to the newest, with the earlier-entries row on top. Up to
/// `eagerLimit` rows lay out at once and longer consoles lazily; a short console starts at the top.
struct ConsoleList: View {
    static let eagerLimit = 400

    let session: DebugSessionModel
    let narrow: Bool
    @FocusState private var focused: Bool

    var body: some View {
        let items = session.console.items(session.consoleFilter)
        let showsTargets = session.info.targets.count > 1 && session.consoleFilter.targetID == nil
        GeometryReader { proxy in
            ScrollView {
                if items.count <= Self.eagerLimit {
                    VStack(alignment: .leading, spacing: 0) { rows(items, showsTargets: showsTargets) }
                        .frame(minHeight: proxy.size.height, alignment: .top)
                } else {
                    LazyVStack(alignment: .leading, spacing: 0) { rows(items, showsTargets: showsTargets) }
                }
            }
            .defaultScrollAnchor(.bottom)
            .defaultScrollAnchor(.bottom, for: .sizeChanges)
        }
        .focusable()
        .focused($focused)
        .onCopyCommand {
            let text = session.selectedConsoleText
            guard !text.isEmpty else { return [] }
            return [NSItemProvider(object: text as NSString)]
        }
        .overlay {
            if items.isEmpty {
                switch session.consolePhase {
                case .loading, .idle:
                    DebugLoadingLine(text: String.ui("debugger.console.loading", "콘솔 기록을 불러오는 중…"))
                case .failed(let error):
                    DebugPanePlaceholder(symbol: "exclamationmark.triangle", title: String.ui("debugger.console.failed", "콘솔 기록을 불러오지 못했습니다"),
                                         message: error.message)
                case .loaded:
                    Text(session.consoleFilter.query.isEmpty ? String.ui("debugger.console.empty", "표시할 메시지가 없습니다")
                                                             : String.ui("debugger.console.searchNone", "일치하는 메시지가 없습니다"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    @ViewBuilder
    private func rows(_ items: [ConsoleItem], showsTargets: Bool) -> some View {
        if session.console.hasEarlier {
            HStack(spacing: DS.Space.xs) {
                if session.isLoadingEarlier { ProgressView().controlSize(.mini) }
                Button(String.ui("debugger.console.earlier", "이전 기록 불러오기")) {
                    Task { await session.loadEarlierConsole() }
                }
                .buttonStyle(.link)
                .font(DS.Font.caption)
                .tint(DS.Color.accent)
                .disabled(session.isLoadingEarlier)
            }
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.xs)
        }
        if session.console.dropped > 0 {
            let count = DS.Format.count(session.console.dropped)
            Text(String.ui("debugger.console.dropped", "오래된 메시지 \(count)개는 데몬 보관 한도를 넘어 지워졌습니다"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .padding(.horizontal, DS.Space.m)
                .padding(.vertical, DS.Space.xs)
        }
        ForEach(items) { item in
            let selected = session.consoleSelection.contains(item.id)
            Group {
                switch item {
                case .entry(let entry):
                    ConsoleEntryRow(session: session, entry: entry, showsTarget: showsTargets, narrow: narrow)
                case .evaluation(let record):
                    EvaluationRows(session: session, record: record, showsTarget: showsTargets)
                case .gap:
                    GapRow()
                }
            }
            .background(selected ? DS.Code.selection : .clear)
            .contentShape(Rectangle())
            .onTapGesture {
                focused = true
                let flags = NSEvent.modifierFlags
                session.selectConsoleItem(item.id, extending: flags.contains(.shift), toggling: flags.contains(.command))
            }
            .contextMenu {
                Button(String.ui("debugger.console.copy", "복사")) {
                    if !session.consoleSelection.contains(item.id) { session.consoleSelection = [item.id] }
                    session.copyConsoleSelection()
                }
            }
            .accessibilityAddTraits(selected ? [.isSelected] : [])
        }
        Color.clear.frame(height: DS.Space.xs)
    }
}

/// Entries the daemon discarded before they could be read.
struct GapRow: View {
    var body: some View {
        HStack(spacing: DS.Space.s) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
            Text(String.ui("debugger.console.gap", "— 받지 못한 메시지가 있습니다 —"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize()
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: ConsoleRowLayout.rowHeight + DS.Space.xs)
        .help(String.ui("debugger.console.gap.help", "연결이 끊긴 사이 데몬 보관 한도를 넘은 메시지는 받을 수 없습니다"))
    }
}

/// A console entry: disclosure for a stack, level symbol, target badge, text, the call site and
/// time in fixed columns; exceptions disclose their stack.
struct ConsoleEntryRow: View {
    let session: DebugSessionModel
    let entry: ConsoleEntry
    let showsTarget: Bool
    let narrow: Bool

    private var expanded: Bool { session.expandedEntries.contains(entry.seq) }

    private var level: ConsoleLevel { entry.consoleLevel }
    private var hasStack: Bool { !(entry.stack?.isEmpty ?? true) && level == .error }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            HStack(alignment: .firstTextBaseline, spacing: 0) {
                disclosure
                    .frame(width: ConsoleRowLayout.disclosureColumn)
                    .padding(.trailing, DS.Space.xs)
                levelIcon
                    .frame(width: ConsoleRowLayout.iconColumn)
                    .padding(.trailing, DS.Space.s)
                if showsTarget, let target = session.info.targets.first(where: { $0.id == entry.targetID }) {
                    RoleBadge(target: target, fixedWidth: true)
                        .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }
                        .padding(.trailing, DS.Space.s)
                }
                if narrow {
                    VStack(alignment: .leading, spacing: DS.Space.xs) {
                        message
                        HStack(spacing: DS.Space.s) {
                            locationLink
                            time
                        }
                    }
                } else {
                    message
                    locationLink
                        .padding(.leading, DS.Space.s)
                    time
                        .padding(.leading, DS.Space.s)
                }
            }
            if expanded, let stack = entry.stack {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(stack.enumerated()), id: \.offset) { _, frame in
                        StackFrameLine(session: session, targetID: entry.targetID, frame: frame)
                    }
                }
                .padding(.leading, ConsoleRowLayout.disclosureColumn + DS.Space.xs + ConsoleRowLayout.iconColumn + DS.Space.s
                         + (showsTarget ? RoleBadge.width + DS.Space.s : 0))
            }
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.xs)
        .background(rowTint)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator.opacity(0.5)).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(DebugFormat.levelTitle(level) + ", " + entry.text)
    }

    @ViewBuilder
    private var disclosure: some View {
        if hasStack {
            Button {
                if expanded { session.expandedEntries.remove(entry.seq) } else { session.expandedEntries.insert(entry.seq) }
            } label: {
                Chevron(expanded: expanded)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(expanded ? String.ui("debugger.console.hideStack", "스택 접기") : String.ui("debugger.console.showStack", "스택 보기"))
        } else {
            Color.clear.frame(height: 1)
        }
    }

    @ViewBuilder
    private var levelIcon: some View {
        if let symbol = DebugFormat.levelSymbol(level) {
            Image(systemName: symbol)
                .font(.system(size: 10))
                .foregroundStyle(DebugFormat.levelTint(level))
                .accessibilityHidden(true)
        } else {
            Color.clear.frame(height: 1)
        }
    }

    private var message: some View {
        Text(entry.text)
            .font(DebugFormat.mono)
            .foregroundStyle(textStyle)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var time: some View {
        Text(DebugFormat.clock(entry.time.date))
            .font(.system(size: 10, design: .monospaced))
            .foregroundStyle(.secondary)
            .fixedSize()
    }

    private var textStyle: AnyShapeStyle {
        switch level {
        case .error: AnyShapeStyle(DS.Code.errorText)
        case .debug: AnyShapeStyle(.secondary)
        default: AnyShapeStyle(.primary)
        }
    }

    private var rowTint: Color {
        switch level {
        case .error: DS.Code.errorRow
        case .warning: DS.Code.warningRow
        default: .clear
        }
    }

    @ViewBuilder
    private var locationLink: some View {
        if let url = entry.url, let line = entry.line {
            Button {
                if let scriptID = entry.scriptID {
                    session.open(SourceFocus(targetID: entry.targetID, scriptID: scriptID, url: url, line: line, column: entry.column))
                }
            } label: {
                Text(DebugFormat.location(url: url, line: line))
                    .font(.system(size: 11, design: .monospaced))
                    .underline(entry.scriptID != nil, color: DS.Code.link.opacity(0.4))
                    .foregroundStyle(entry.scriptID != nil ? AnyShapeStyle(DS.Code.link) : AnyShapeStyle(.secondary))
                    .lineLimit(1)
            }
            .buttonStyle(.plain)
            .disabled(entry.scriptID == nil)
            .fixedSize()
            .help(url + ":" + String(line))
        }
    }
}

/// `at applyDiscount (api.js:34)`, the location a link to the source.
struct StackFrameLine: View {
    let session: DebugSessionModel
    let targetID: String
    let frame: StackFrameInfo

    var body: some View {
        HStack(spacing: 0) {
            Text("at " + (frame.function.isEmpty ? "<anonymous>" : frame.function) + " (")
                .foregroundStyle(.secondary)
            Button {
                if let scriptID = frame.scriptID {
                    session.open(SourceFocus(targetID: targetID, scriptID: scriptID, url: frame.url, line: frame.line, column: frame.column))
                }
            } label: {
                Text(DebugFormat.location(url: frame.url, line: frame.line))
                    .underline(frame.scriptID != nil, color: DS.Code.link.opacity(0.4))
                    .foregroundStyle(frame.scriptID != nil ? AnyShapeStyle(DS.Code.link) : AnyShapeStyle(.secondary))
            }
            .buttonStyle(.plain)
            .disabled(frame.scriptID == nil)
            .help(frame.url + ":" + String(frame.line) + ":" + String(frame.column))
            Text(")")
                .foregroundStyle(.secondary)
        }
        .font(.system(size: 11, design: .monospaced))
        .lineLimit(1)
        .frame(height: 16)
    }
}

/// An evaluation: the expression echoed with the target it ran in, then its value, object or
/// thrown error.
struct EvaluationRows: View {
    let session: DebugSessionModel
    let record: EvaluationRecord
    let showsTarget: Bool

    private var target: DebugTarget? {
        showsTarget ? session.info.targets.first { $0.id == record.targetID } : nil
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            HStack(alignment: .firstTextBaseline, spacing: 0) {
                Color.clear.frame(width: ConsoleRowLayout.disclosureColumn, height: 1)
                    .padding(.trailing, DS.Space.xs)
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(DS.Color.accent)
                    .frame(width: ConsoleRowLayout.iconColumn)
                    .padding(.trailing, DS.Space.s)
                    .accessibilityHidden(true)
                if let target {
                    RoleBadge(target: target, fixedWidth: true)
                        .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }
                        .padding(.trailing, DS.Space.s)
                }
                Text(record.expression)
                    .font(DebugFormat.mono)
                    .foregroundStyle(.primary)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let frame = record.frame {
                    Text(String.ui("debugger.console.inFrame", "\(frame)에서"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .fixedSize()
                        .padding(.leading, DS.Space.s)
                }
            }
            result
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.xs)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator.opacity(0.5)).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }

    /// A result line: the disclosure column, then `icon` in the symbol column, then `content`.
    private func resultLine<Disclosure: View, Icon: View, Content: View>(@ViewBuilder disclosure: () -> Disclosure, @ViewBuilder icon: () -> Icon,
                                                                         @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 0) {
            disclosure()
                .frame(width: ConsoleRowLayout.disclosureColumn)
                .padding(.trailing, DS.Space.xs)
            icon()
                .frame(width: ConsoleRowLayout.iconColumn)
                .padding(.trailing, DS.Space.s)
                .padding(.trailing, inset)
            content()
        }
    }

    /// The width of the target badge column, when the rows show targets.
    private var inset: CGFloat { target == nil ? 0 : RoleBadge.width + DS.Space.s }

    @ViewBuilder
    private var result: some View {
        switch record.result {
        case .pending:
            resultLine {
                Color.clear.frame(height: 1)
            } icon: {
                ProgressView().controlSize(.mini)
            } content: {
                Text(String.ui("debugger.console.evaluating", "평가하는 중…"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        case .failure(let error):
            resultLine {
                Color.clear.frame(height: 1)
            } icon: {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
            } content: {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                    Text(error.message.asSentence)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    if let code = error.code {
                        Text(code)
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                }
            }
        case .value(let evaluation):
            if let exception = evaluation.exception {
                resultLine {
                    Color.clear.frame(height: 1)
                } icon: {
                    Image(systemName: "xmark.octagon.fill")
                        .font(.system(size: 10))
                        .foregroundStyle(DS.Color.danger)
                        .accessibilityHidden(true)
                } content: {
                    Text(exception.text)
                        .font(DebugFormat.mono)
                        .foregroundStyle(DS.Code.errorText)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } else {
                ResultValue(session: session, record: record, value: evaluation.value, inset: inset)
            }
        }
    }
}

/// The value of an evaluation; objects expand like the variables tree, read from the target the
/// evaluation ran in.
struct ResultValue: View {
    let session: DebugSessionModel
    let record: EvaluationRecord
    let value: RemoteValue
    /// Space before the value, past the target badge column.
    let inset: CGFloat

    var body: some View {
        let path: VariableTree.Path = ["x\(record.id)"]
        let expandable = value.objectID != nil && value.hasChildren
        let expanded = session.consoleObjects.isExpanded(path)
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: 0) {
                Group {
                    if expandable {
                        Button {
                            let root = VariableTree.Row(kind: .scope, path: path, depth: 0, title: record.expression, value: value,
                                                        isExpandable: true, isExpanded: expanded, objectID: value.objectID,
                                                        targetID: record.targetID)
                            session.toggleConsoleObject(root)
                        } label: {
                            Chevron(expanded: expanded)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(expanded ? String.ui("debugger.variables.collapse", "접기") : String.ui("debugger.variables.expand", "펼치기"))
                    } else {
                        Color.clear.frame(height: 1)
                    }
                }
                .frame(width: ConsoleRowLayout.disclosureColumn)
                .padding(.trailing, DS.Space.xs)
                Image(systemName: "arrow.turn.down.right")
                    .font(.system(size: 9))
                    .foregroundStyle(.secondary)
                    .frame(width: ConsoleRowLayout.iconColumn)
                    .padding(.trailing, DS.Space.s + inset)
                    .accessibilityHidden(true)
                Text(value.valuePreview)
                    .font(DebugFormat.mono)
                    .italic(value.type == "function")
                    .foregroundStyle(DebugFormat.valueColor(value))
                    .fixedSize(horizontal: false, vertical: true)
            }
            if expanded {
                VariableRows(rows: session.consoleObjects.rows(under: path), baseIndent: ConsoleRowLayout.disclosureColumn + inset) {
                    session.toggleConsoleObject($0)
                }
            }
        }
    }
}
