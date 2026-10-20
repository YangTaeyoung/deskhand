import AppKit
import DeskhandClient
import SwiftUI

/// The session's console: messages and uncaught exceptions of every target with level, target and
/// text filters; evaluations with their results; and the expression field, which evaluates in the
/// selected frame while paused and globally otherwise. Rows select with a click, ⌘-click and
/// ⇧-click, and ⌘C copies them. In a `compact` tab or below `narrowWidth` the call site and time
/// go under the text and the key hint hides.
struct DebugConsoleView: View {
    static let narrowWidth: CGFloat = 560

    let model: DebuggerModel
    let session: DebugSessionModel?
    var compact = false
    @State private var inputHeight: CGFloat = ConsoleInput.height(lines: 1)
    @State private var width: CGFloat = 800
    @FocusState private var searchFocused: Bool

    private var narrow: Bool { compact || width < Self.narrowWidth }

    var body: some View {
        VStack(spacing: 0) {
            header
            if let session {
                ConsoleList(session: session, narrow: narrow)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(DS.Code.background)
                inputBar(session)
            } else {
                DebugPaneHint(text: String.ui("debugger.console.none", "세션이 없습니다."))
            }
        }
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
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
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .help(String.ui("debugger.console.clear.help", "모든 클라이언트에서 지금까지의 콘솔 내용을 지웁니다 (⌘L)"))
            .accessibilityLabel(String.ui("debugger.console.clear", "콘솔 지우기"))
        }
    }

    private func inputBar(_ session: DebugSessionModel) -> some View {
        let enabled = !session.isClosed && session.selectedTarget?.attached == true
        return HStack(alignment: .top, spacing: DS.Space.s) {
            Image(systemName: "chevron.right")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(enabled ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                .frame(width: ConsoleRowLayout.iconColumn, height: ConsoleInput.lineHeight)
                .padding(.top, ConsoleInput.inset)
                .accessibilityHidden(true)
            ZStack(alignment: .topLeading) {
                if session.draft.isEmpty {
                    Text(placeholder(session))
                        .font(DebugFormat.mono)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .padding(.top, ConsoleInput.inset)
                        .allowsHitTesting(false)
                }
                ConsoleInput(text: Binding(get: { session.draft }, set: { session.draft = $0 }), height: $inputHeight,
                             submit: { session.evaluate($0) },
                             previous: { session.history.previous(current: $0) },
                             next: { session.history.next() },
                             focusChanged: { model.consoleFocused = $0 },
                             enabled: enabled)
                    .frame(height: inputHeight)
            }
            if !narrow {
                Text(String.ui("debugger.console.keys", "↩ 평가 · ⇧↩ 줄바꿈 · ↑↓ 기록"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .padding(.top, ConsoleInput.inset)
                    .fixedSize()
                    .accessibilityHidden(true)
            }
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.xs)
        .background(DS.Code.background)
        .overlay(alignment: .top) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .disabled(!enabled)
    }

    private func placeholder(_ session: DebugSessionModel) -> String {
        if session.isClosed { return .ui("debugger.console.placeholder.closed", "세션이 닫혀 평가할 수 없습니다") }
        if session.selectedTarget?.attached == false { return .ui("debugger.console.placeholder.detached", "연결하지 않은 대상에서는 평가할 수 없습니다") }
        if let frame = session.selectedFrame, session.pauseState != nil {
            let name = DebugFormat.frameName(frame.function)
            return .ui("debugger.console.placeholder.frame", "\(name) 프레임에서 식 평가")
        }
        let target = session.selectedTarget.map(DebugFormat.role) ?? ""
        return .ui("debugger.console.placeholder.global", "\(target) 전역에서 식 평가")
    }
}

/// Column widths shared by console rows, so text starts at one x in every row.
enum ConsoleRowLayout {
    /// The disclosure chevron of entries with a stack.
    static let disclosureColumn: CGFloat = 12
    /// The level symbol, or the arrows of an evaluation.
    static let iconColumn: CGFloat = 12
    static let rowHeight: CGFloat = 20
    /// Where the text of a row starts.
    static var textLeading: CGFloat { DS.Space.m + disclosureColumn + DS.Space.xs + iconColumn + DS.Space.s }
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
            HStack(spacing: DS.Space.xs) {
                if let symbol = DebugFormat.levelSymbol(level) ?? (compact ? "text.alignleft" : nil) {
                    Image(systemName: symbol)
                        .imageScale(.small)
                        .foregroundStyle(isOn ? AnyShapeStyle(DebugFormat.levelTint(level)) : AnyShapeStyle(.secondary))
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
            .padding(.horizontal, DS.Space.s)
            .frame(height: 20)
            .background(background, in: Capsule())
            .overlay(Capsule().strokeBorder(DS.Color.separator.opacity(isOn ? 1 : 0.6), lineWidth: 1))
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
        .focusEffectDisabled()
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
            let count = FileFormat.count(session.console.dropped)
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
            .background(selected ? DS.Color.accent.opacity(0.16) : .clear)
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
private struct GapRow: View {
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
private struct ConsoleEntryRow: View {
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
        case .error: DS.Color.danger.opacity(DS.Code.errorRowOpacity)
        case .warning: DS.Color.warning.opacity(0.08)
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
private struct StackFrameLine: View {
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
private struct EvaluationRows: View {
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
private struct ResultValue: View {
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
