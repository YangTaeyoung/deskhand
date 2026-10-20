import AppKit
import DeskhandClient
import SwiftUI

/// The session's console: messages and uncaught exceptions of every target with level, target and
/// text filters; evaluations with their results; and the expression field, which evaluates in the
/// selected frame while paused and globally otherwise.
struct DebugConsoleView: View {
    let session: DebugSessionModel?
    @State private var inputHeight: CGFloat = ConsoleInput.height(lines: 1)
    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            header
            if let session {
                ConsoleList(session: session)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(DS.Code.background)
                inputBar(session)
            } else {
                DebugPanePlaceholder(symbol: "text.alignleft", title: String.ui("debugger.console.none", "세션이 없습니다"))
            }
        }
        .accessibilityElement(children: .contain)
    }

    /// The title, level toggles, target menu, search and clear; narrower widths drop the toggle
    /// titles, then the title and target menu, then the search.
    private var header: some View {
        Group {
            if let session {
                ViewThatFits(in: .horizontal) {
                    headerRow(session, compact: false, title: true, scope: true, search: 160)
                    headerRow(session, compact: true, title: true, scope: true, search: 120)
                    headerRow(session, compact: true, title: false, scope: false, search: 100)
                    headerRow(session, compact: true, title: false, scope: false, search: nil)
                }
            } else {
                HStack {
                    title
                    Spacer()
                }
            }
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: DebugPaneHeader<EmptyView>.height)
        .background(DS.Color.surfaceRaised.opacity(0.55))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    private var title: some View {
        Text(String.ui("debugger.console", "콘솔"))
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .accessibilityAddTraits(.isHeader)
            .fixedSize()
    }

    private func headerRow(_ session: DebugSessionModel, compact: Bool, title showsTitle: Bool, scope: Bool, search: CGFloat?) -> some View {
        HStack(spacing: DS.Space.s) {
            if showsTitle { title }
            LevelFilter(session: session, compact: compact)
            Spacer(minLength: DS.Space.s)
            if scope, session.info.targets.count > 1 { TargetScopeMenu(session: session) }
            if let search {
                SearchField(text: Binding(get: { session.consoleFilter.query }, set: { session.consoleFilter.query = $0 }),
                            prompt: String.ui("debugger.console.search", "콘솔 검색"), focused: $searchFocused)
                    .frame(width: search)
            }
            Button {
                session.clearConsole()
            } label: {
                Image(systemName: "trash")
                    .font(.system(size: 11))
                    .frame(width: 18, height: 18)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .help(String.ui("debugger.console.clear.help", "지금까지의 콘솔 내용을 지웁니다 (⌘L)"))
            .accessibilityLabel(String.ui("debugger.console.clear", "콘솔 지우기"))
        }
    }

    private func inputBar(_ session: DebugSessionModel) -> some View {
        let enabled = !session.isClosed && session.selectedTarget?.attached == true
        return HStack(alignment: .top, spacing: DS.Space.s) {
            Image(systemName: "chevron.right")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(enabled ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.tertiary))
                .frame(height: ConsoleInput.lineHeight)
                .padding(.top, 2)
                .accessibilityHidden(true)
            ZStack(alignment: .topLeading) {
                if session.draft.isEmpty {
                    Text(placeholder(session))
                        .font(DebugFormat.mono)
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                        .padding(.top, 2)
                        .allowsHitTesting(false)
                }
                ConsoleInput(text: Binding(get: { session.draft }, set: { session.draft = $0 }), height: $inputHeight,
                             submit: { session.evaluate($0) },
                             previous: { session.history.previous(current: $0) },
                             next: { session.history.next() },
                             enabled: enabled)
                    .frame(height: inputHeight)
            }
            Text(String.ui("debugger.console.keys", "↩ 평가 · ⇧↩ 줄바꿈 · ↑↓ 기록"))
                .font(DS.Font.caption)
                .foregroundStyle(.tertiary)
                .padding(.top, 2)
                .fixedSize()
                .accessibilityHidden(true)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Code.background)
        .overlay(alignment: .top) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .disabled(!enabled)
    }

    private func placeholder(_ session: DebugSessionModel) -> String {
        if session.isClosed { return .ui("debugger.console.placeholder.closed", "세션이 닫혀 평가할 수 없습니다") }
        if let frame = session.selectedFrame, session.pauseState != nil {
            let name = DebugFormat.frameName(frame.function)
            return .ui("debugger.console.placeholder.frame", "\(name) 프레임에서 식 평가")
        }
        let target = session.selectedTarget.map(DebugFormat.role) ?? ""
        return .ui("debugger.console.placeholder.global", "\(target) 전역에서 식 평가")
    }
}

/// Toggles for 오류, 경고, 정보, 로그 and 디버그 with their counts.
private struct LevelFilter: View {
    let session: DebugSessionModel
    var compact = false

    var body: some View {
        let counts = session.console.counts(session.consoleFilter)
        HStack(spacing: DS.Space.xs) {
            ForEach([ConsoleLevel.error, .warning, .info, .log, .debug], id: \.self) { level in
                LevelToggle(level: level, count: counts[level] ?? 0, isOn: session.consoleFilter.levels.contains(level), compact: compact) {
                    if session.consoleFilter.levels.contains(level) {
                        session.consoleFilter.levels.remove(level)
                    } else {
                        session.consoleFilter.levels.insert(level)
                    }
                }
            }
        }
        .fixedSize()
    }
}

private struct LevelToggle: View {
    let level: ConsoleLevel
    let count: Int
    let isOn: Bool
    var compact = false
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 3) {
                if let symbol = DebugFormat.levelSymbol(level) ?? (compact ? "text.alignleft" : nil) {
                    Image(systemName: symbol)
                        .imageScale(.small)
                        .foregroundStyle(isOn ? AnyShapeStyle(DebugFormat.levelTint(level)) : AnyShapeStyle(.tertiary))
                }
                if !compact {
                    Text(DebugFormat.levelTitle(level))
                        .foregroundStyle(isOn ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
                }
                if count > 0, level == .error || level == .warning {
                    Text(String(count))
                        .monospacedDigit()
                        .foregroundStyle(isOn ? AnyShapeStyle(DebugFormat.levelTint(level)) : AnyShapeStyle(.secondary))
                }
            }
            .font(DS.Font.caption.weight(.medium))
            .padding(.horizontal, 7)
            .frame(height: 20)
            .background(background, in: Capsule())
            .overlay(Capsule().strokeBorder(isOn ? DS.Color.separator.opacity(1) : DS.Color.separator.opacity(0.6), lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(String.ui("debugger.console.level.help", "\(DebugFormat.levelTitle(level)) 메시지 보기"))
        .accessibilityLabel(DebugFormat.levelTitle(level))
        .accessibilityValue(String(count))
        .accessibilityAddTraits(isOn ? [.isSelected] : [])
    }

    private var background: AnyShapeStyle {
        if isOn { return AnyShapeStyle(DS.Color.neutral.opacity(0.14)) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.08)) : AnyShapeStyle(.clear)
    }
}

/// Every target, or the selected target only.
private struct TargetScopeMenu: View {
    let session: DebugSessionModel

    var body: some View {
        Menu {
            Button(String.ui("debugger.console.allTargets", "모든 대상")) { session.consoleFilter.targetID = nil }
            Divider()
            ForEach(session.info.targets) { target in
                Button(DebugFormat.role(target) + " · " + DebugFormat.targetTitle(target)) { session.consoleFilter.targetID = target.id }
            }
        } label: {
            Text(title)
                .font(DS.Font.caption)
                .lineLimit(1)
        }
        .menuStyle(.button)
        .buttonStyle(.borderless)
        .fixedSize()
        .help(String.ui("debugger.console.scope.help", "어느 대상의 메시지를 볼지 고릅니다"))
    }

    private var title: String {
        guard let id = session.consoleFilter.targetID, let target = session.info.targets.first(where: { $0.id == id }) else {
            return .ui("debugger.console.allTargets", "모든 대상")
        }
        return DebugFormat.role(target)
    }
}

/// Console rows kept scrolled to the newest, with the earlier-entries row on top. Up to
/// `eagerLimit` rows lay out at once and longer consoles lazily; a short console starts at the top.
private struct ConsoleList: View {
    static let eagerLimit = 400

    let session: DebugSessionModel

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
                        .foregroundStyle(.tertiary)
                }
            }
        }
    }

    @ViewBuilder
    private func rows(_ items: [ConsoleItem], showsTargets: Bool) -> some View {
        if session.console.hasEarlier {
            Button {
                Task { await session.loadEarlierConsole() }
            } label: {
                HStack(spacing: DS.Space.xs) {
                    if session.isLoadingEarlier { ProgressView().controlSize(.mini) }
                    Text(String.ui("debugger.console.earlier", "이전 기록 불러오기"))
                }
                .font(DS.Font.caption)
            }
            .buttonStyle(.link)
            .tint(DS.Color.accent)
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.xs)
        }
        if session.console.dropped > 0 {
            let count = FileFormat.count(session.console.dropped)
            Text(String.ui("debugger.console.dropped", "오래된 메시지 \(count)개는 데몬 보관 한도를 넘어 지워졌습니다"))
                .font(DS.Font.caption)
                .foregroundStyle(.tertiary)
                .padding(.horizontal, DS.Space.m)
                .padding(.vertical, DS.Space.xs)
        }
        ForEach(items) { item in
            switch item {
            case .entry(let entry):
                ConsoleEntryRow(session: session, entry: entry, showsTarget: showsTargets)
            case .evaluation(let record):
                EvaluationRows(session: session, record: record)
            }
        }
        Color.clear.frame(height: DS.Space.xs)
    }
}

/// A console entry: level symbol, text, the call site and time; exceptions disclose their stack.
private struct ConsoleEntryRow: View {
    let session: DebugSessionModel
    let entry: ConsoleEntry
    let showsTarget: Bool

    private var expanded: Bool { session.expandedEntries.contains(entry.seq) }

    private var level: ConsoleLevel { entry.consoleLevel }
    private var hasStack: Bool { !(entry.stack?.isEmpty ?? true) && level == .error }

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                Group {
                    if hasStack {
                        Button {
                            if expanded { session.expandedEntries.remove(entry.seq) } else { session.expandedEntries.insert(entry.seq) }
                        } label: {
                            Chevron(expanded: expanded)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(expanded ? String.ui("debugger.console.hideStack", "스택 접기") : String.ui("debugger.console.showStack", "스택 보기"))
                    } else if let symbol = DebugFormat.levelSymbol(level) {
                        Image(systemName: symbol)
                            .font(.system(size: 10))
                            .foregroundStyle(DebugFormat.levelTint(level))
                    } else {
                        Color.clear.frame(width: 12, height: 10)
                    }
                }
                .frame(width: 12)
                if hasStack, let symbol = DebugFormat.levelSymbol(level) {
                    Image(systemName: symbol)
                        .font(.system(size: 10))
                        .foregroundStyle(DebugFormat.levelTint(level))
                        .accessibilityHidden(true)
                }
                if showsTarget, let target = session.info.targets.first(where: { $0.id == entry.targetID }) {
                    RoleBadge(target: target)
                        .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }
                }
                Text(entry.text)
                    .font(DebugFormat.mono)
                    .foregroundStyle(textStyle)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                locationLink
                Text(DebugFormat.clock(entry.time.date))
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundStyle(.tertiary)
                    .fixedSize()
            }
            if expanded, let stack = entry.stack {
                ForEach(Array(stack.enumerated()), id: \.offset) { _, frame in
                    StackFrameLine(session: session, targetID: entry.targetID, frame: frame)
                }
            }
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, 4)
        .background(rowTint)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator.opacity(0.5)).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(DebugFormat.levelTitle(level) + ", " + entry.text)
    }

    private var textStyle: AnyShapeStyle {
        switch level {
        case .error: AnyShapeStyle(DS.Color.danger)
        case .debug: AnyShapeStyle(.secondary)
        default: AnyShapeStyle(.primary)
        }
    }

    private var rowTint: Color {
        switch level {
        case .error: DS.Color.danger.opacity(0.06)
        case .warning: DS.Color.warning.opacity(0.07)
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
                    .underline(entry.scriptID != nil, color: DS.Color.accent.opacity(0.4))
                    .foregroundStyle(entry.scriptID != nil ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                    .lineLimit(1)
            }
            .buttonStyle(.plain)
            .disabled(entry.scriptID == nil)
            .fixedSize()
            .help(url + ":" + String(line))
        }
    }
}

/// `at applyDiscount (api.js:34)` with a link to the location.
private struct StackFrameLine: View {
    let session: DebugSessionModel
    let targetID: String
    let frame: StackFrameInfo

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Text(String.ui("debugger.console.at", "위치"))
                .foregroundStyle(.tertiary)
            Text(DebugFormat.frameName(frame.function))
                .foregroundStyle(.secondary)
            Button {
                if let scriptID = frame.scriptID {
                    session.open(SourceFocus(targetID: targetID, scriptID: scriptID, url: frame.url, line: frame.line, column: frame.column))
                }
            } label: {
                Text(DebugFormat.location(url: frame.url, line: frame.line))
                    .foregroundStyle(frame.scriptID != nil ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
            }
            .buttonStyle(.plain)
            .disabled(frame.scriptID == nil)
        }
        .font(.system(size: 11, design: .monospaced))
        .padding(.leading, 12 + DS.Space.s + 20)
    }
}

/// An evaluation: the expression echoed, then its value, object or thrown error.
private struct EvaluationRows: View {
    let session: DebugSessionModel
    let record: EvaluationRecord

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(DS.Color.accent)
                    .frame(width: 12)
                    .accessibilityHidden(true)
                Text(record.expression)
                    .font(DebugFormat.mono)
                    .foregroundStyle(.primary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let frame = record.frame {
                    Text(String.ui("debugger.console.inFrame", "\(frame)에서"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                        .fixedSize()
                }
            }
            .padding(.horizontal, DS.Space.m)
            .padding(.top, 4)
            .padding(.bottom, 2)
            result
                .padding(.horizontal, DS.Space.m)
                .padding(.bottom, 4)
        }
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator.opacity(0.5)).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder
    private var result: some View {
        switch record.result {
        case .pending:
            HStack(spacing: DS.Space.s) {
                Color.clear.frame(width: 12)
                ProgressView().controlSize(.mini)
                Text(String.ui("debugger.console.evaluating", "평가하는 중…"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        case .failure(let error):
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(DS.Color.warning)
                    .frame(width: 12)
                    .accessibilityHidden(true)
                Text(error.message.asSentence)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let code = error.code {
                    Text(code)
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundStyle(.tertiary)
                }
            }
        case .value(let evaluation):
            if let exception = evaluation.exception {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                    Image(systemName: "xmark.octagon.fill")
                        .font(.system(size: 10))
                        .foregroundStyle(DS.Color.danger)
                        .frame(width: 12)
                        .accessibilityHidden(true)
                    Text(exception.text)
                        .font(DebugFormat.mono)
                        .foregroundStyle(DS.Color.danger)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } else {
                ResultValue(session: session, record: record, value: evaluation.value)
            }
        }
    }
}

/// The value of an evaluation; objects expand like the variables tree.
private struct ResultValue: View {
    let session: DebugSessionModel
    let record: EvaluationRecord
    let value: RemoteValue

    var body: some View {
        let path = "x\(record.id)"
        let expandable = value.objectID != nil && value.hasChildren
        let expanded = session.consoleObjects.isExpanded(path)
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                if expandable {
                    Button {
                        let root = VariableTree.Row(id: path, kind: .scope, path: path, depth: 0, title: record.expression, value: value,
                                                    isExpandable: true, isExpanded: expanded, objectID: value.objectID)
                        session.toggleConsoleObject(root)
                    } label: {
                        Chevron(expanded: expanded)
                    }
                    .buttonStyle(.plain)
                    .frame(width: 12)
                    .accessibilityLabel(expanded ? String.ui("debugger.variables.collapse", "접기") : String.ui("debugger.variables.expand", "펼치기"))
                } else {
                    Image(systemName: "arrow.turn.down.right")
                        .font(.system(size: 9))
                        .foregroundStyle(.tertiary)
                        .frame(width: 12)
                        .accessibilityHidden(true)
                }
                Text(value.valuePreview)
                    .font(DebugFormat.mono)
                    .italic(value.type == "function")
                    .foregroundStyle(DebugFormat.valueColor(value))
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if expanded {
                VariableRows(rows: session.consoleObjects.rows(under: path), baseIndent: 0) { session.toggleConsoleObject($0) }
                    .padding(.leading, -DS.Space.s)
            }
        }
    }
}
