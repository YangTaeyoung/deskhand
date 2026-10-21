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
    /// The height of the one-line expression bar under the console.
    static let inputBarHeight: CGFloat = ConsoleInput.height(lines: 1) + DS.Space.xs * 2 + 1

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
                    .focusesOnFind($searchFocused)
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
                        .font(.system(size: DebugFormat.monoSize))
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
