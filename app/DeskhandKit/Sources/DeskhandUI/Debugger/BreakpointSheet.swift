import DeskhandClient
import SwiftUI

/// Adds a breakpoint by script and line: the script as a file name, an exact URL or a regular
/// expression, with the loaded scripts offered; an optional condition. With `editing` it changes
/// that breakpoint's condition only.
public struct BreakpointSheet: View {
    public enum Match: String, CaseIterable, Identifiable, Sendable {
        case fileName
        case url
        case regex

        public var id: String { rawValue }
    }

    let session: DebugSessionModel
    let editing: DebugBreakpoint?
    let dismiss: () -> Void
    @State private var match: Match
    @State private var script: String
    @State private var line: String
    @State private var condition: String
    @State private var error: OperationError?
    @State private var saving = false

    public init(session: DebugSessionModel, match: Match = .fileName, script: String = "", line: String = "", condition: String = "",
                error: OperationError? = nil, dismiss: @escaping () -> Void) {
        self.session = session
        self.editing = nil
        self.dismiss = dismiss
        _match = State(initialValue: match)
        _script = State(initialValue: script)
        _line = State(initialValue: line)
        _condition = State(initialValue: condition)
        _error = State(initialValue: error)
    }

    /// The sheet that changes `breakpoint`'s condition.
    public init(session: DebugSessionModel, editing breakpoint: DebugBreakpoint, condition: String? = nil, error: OperationError? = nil,
                dismiss: @escaping () -> Void) {
        self.session = session
        self.editing = breakpoint
        self.dismiss = dismiss
        _match = State(initialValue: .fileName)
        _script = State(initialValue: breakpoint.pattern)
        _line = State(initialValue: String(breakpoint.line))
        _condition = State(initialValue: condition ?? breakpoint.condition ?? "")
        _error = State(initialValue: error)
    }

    private var pattern: BreakpointPattern {
        switch match {
        case .fileName: .fileName(script)
        case .url: .url(script)
        case .regex: .regex(script)
        }
    }

    private var lineNumber: Int? { Int(line.trimmingCharacters(in: .whitespaces)).flatMap { $0 >= 1 ? $0 : nil } }

    /// Loaded scripts matching what is typed, at most eight.
    private var suggestions: [DebugScript] {
        let needle = script.trimmingCharacters(in: .whitespaces)
        let scripts = session.scripts.filter { !ScriptURL.isInternal($0.url) && (needle.isEmpty || $0.url.localizedCaseInsensitiveContains(needle)) }
        return Array(scripts.prefix(8))
    }

    /// The typed script is one of the suggestions as the match mode writes it.
    private var exactlyChosen: Bool {
        let typed = script.trimmingCharacters(in: .whitespaces)
        return suggestions.contains { item in
            switch match {
            case .fileName: ScriptURL.fileName(item.url) == typed
            case .url: item.url == typed
            case .regex: false
            }
        }
    }

    /// Scripts the pattern matches now, or nil while it is empty or not a valid expression here.
    private var matched: Int? {
        guard let spec = try? pattern.spec(line: 1) else { return nil }
        if spec.matchKind == .urlRegex, (try? NSRegularExpression(pattern: spec.pattern)) == nil { return nil }
        let probe = DebugBreakpoint(breakpointID: "", match: spec.matchKind, pattern: spec.pattern, line: 1)
        return session.scripts.filter { BreakpointPattern.matches(probe, url: $0.url) }.count
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                BreakpointMarker(enabled: true, resolved: true, conditional: editing != nil, width: 32, height: 20)
                    .padding(.top, DS.Space.xs)
                    .frame(width: 36)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(editing == nil ? String.ui("debugger.breakpoint.sheet.title", "중단점 추가")
                                        : String.ui("debugger.breakpoint.sheet.editTitle", "중단점 조건 편집"))
                        .font(DS.Font.title)
                        .accessibilityAddTraits(.isHeader)
                    Text(subtitle)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.m) {
                if let editing {
                    GridRow {
                        label(String.ui("debugger.breakpoint.sheet.location", "위치"))
                        Text(BreakpointPattern.title(of: editing))
                            .font(DS.Font.mono)
                            .help(editing.locations.first?.url ?? editing.pattern)
                    }
                } else {
                    GridRow {
                        label(String.ui("debugger.breakpoint.sheet.script", "스크립트"))
                        VStack(alignment: .leading, spacing: DS.Space.s) {
                            Picker(String.ui("debugger.breakpoint.sheet.match", "일치 방식"), selection: $match) {
                                Text(String.ui("debugger.breakpoint.match.fileName", "파일 이름")).tag(Match.fileName)
                                Text(String.ui("debugger.breakpoint.match.url", "정확한 URL")).tag(Match.url)
                                Text(String.ui("debugger.breakpoint.match.regex", "정규식")).tag(Match.regex)
                            }
                            .pickerStyle(.segmented)
                            .labelsHidden()
                            .fixedSize()
                            TextField(prompt, text: $script)
                                .font(DS.Font.mono)
                                .textFieldStyle(.roundedBorder)
                                .accessibilityLabel(String.ui("debugger.breakpoint.sheet.script", "스크립트"))
                            matchLine
                            if !suggestions.isEmpty, !exactlyChosen {
                                suggestionList
                            }
                        }
                    }
                    GridRow {
                        label(String.ui("debugger.breakpoint.sheet.line", "줄"))
                        TextField("42", text: $line)
                            .font(DS.Font.mono)
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 96)
                            .accessibilityLabel(String.ui("debugger.breakpoint.sheet.line", "줄"))
                    }
                }
                GridRow {
                    label(String.ui("debugger.breakpoint.sheet.condition", "조건"))
                    VStack(alignment: .leading, spacing: DS.Space.xs) {
                        TextField(String.ui("debugger.breakpoint.sheet.condition.prompt", "선택 사항 · 예: order.total > 40000"), text: $condition)
                            .font(DS.Font.mono)
                            .textFieldStyle(.roundedBorder)
                            .accessibilityLabel(String.ui("debugger.breakpoint.sheet.condition", "조건"))
                        Text(editing == nil ? String.ui("debugger.breakpoint.sheet.condition.hint", "참이 되는 JavaScript 식을 쓰면 그때만 멈춥니다.")
                                            : String.ui("debugger.breakpoint.sheet.condition.editHint", "참이 되는 JavaScript 식을 쓰면 그때만 멈춥니다. 비우면 항상 멈춥니다."))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if let error {
                StatusNotice(symbol: "exclamationmark.octagon.fill", tint: DS.Color.danger,
                             title: editing == nil ? String.ui("debugger.failed.breakpointSet", "중단점을 걸지 못했습니다")
                                                   : String.ui("debugger.failed.breakpoint", "중단점을 바꾸지 못했습니다"),
                             message: error.message.asSentence, code: error.code)
            }
            HStack {
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: dismiss)
                    .keyboardShortcut(.cancelAction)
                Button {
                    save()
                } label: {
                    if saving {
                        ProgressView().controlSize(.small).frame(minWidth: 56)
                    } else {
                        Text(editing == nil ? String.ui("debugger.breakpoint.sheet.add", "추가") : String.ui("action.save", "저장"))
                            .frame(minWidth: 56)
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canSave || saving)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 560)
        .task { if editing == nil { await session.loadScripts() } }
    }

    private var subtitle: String {
        let name = session.info.name
        if editing != nil { return .ui("debugger.breakpoint.sheet.editSubtitle", "조건을 바꾸면 \(name)의 모든 대상에 다시 겁니다.") }
        return .ui("debugger.breakpoint.sheet.subtitle", "\(name)의 모든 대상에 겁니다. 아직 로드되지 않은 스크립트는 로드될 때 연결됩니다.")
    }

    private var canSave: Bool {
        if editing != nil { return true }
        return lineNumber != nil && !script.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private var prompt: String {
        switch match {
        case .fileName: .ui("debugger.breakpoint.prompt.fileName", "예: checkout.js 또는 renderer/checkout.js")
        case .url: .ui("debugger.breakpoint.prompt.url", "예: app://shop-admin/renderer/checkout.js")
        case .regex: .ui("debugger.breakpoint.prompt.regex", "예: renderer/.*\\.js$")
        }
    }

    @ViewBuilder
    private var matchLine: some View {
        if let matched {
            if matched == 0, !suggestions.isEmpty {
                Label(String.ui("debugger.breakpoint.sheet.pickSuggestion", "일치하는 스크립트가 없습니다. 아래 목록에서 고르세요."),
                      systemImage: "info.circle")
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            } else if matched == 0 {
                Label(String.ui("debugger.breakpoint.sheet.noMatch", "지금 로드된 스크립트 중 일치하는 것이 없습니다. 로드되면 연결됩니다."),
                      systemImage: "clock")
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            } else {
                let count = String(matched)
                Label {
                    Text(String.ui("debugger.breakpoint.sheet.matches", "로드된 스크립트 \(count)개와 일치합니다"))
                        .foregroundStyle(.secondary)
                } icon: {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(DS.Color.success)
                }
                .font(DS.Font.caption)
            }
        } else if match == .regex, !script.trimmingCharacters(in: .whitespaces).isEmpty {
            Label(String.ui("debugger.breakpoint.sheet.badRegex", "이 정규식은 이 Mac에서 읽을 수 없습니다. 추가하면 데몬이 대상에서 확인합니다."),
                  systemImage: "exclamationmark.triangle.fill")
                .font(DS.Font.caption)
                .foregroundStyle(DS.Color.warning)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var suggestionList: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(suggestions) { item in
                Button {
                    switch match {
                    case .fileName: script = ScriptURL.fileName(item.url)
                    case .url: script = item.url
                    case .regex: script = NSRegularExpression.escapedPattern(for: ScriptURL.fileName(item.url)) + "$"
                    }
                } label: {
                    HStack(spacing: DS.Space.s) {
                        Image(systemName: "doc.text")
                            .foregroundStyle(.secondary)
                            .accessibilityHidden(true)
                        Text(ScriptURL.fileName(item.url))
                            .font(DS.Font.body)
                        Text(ScriptURL.display(item.url))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.head)
                        Spacer(minLength: 0)
                        if let target = session.info.targets.first(where: { $0.id == item.targetID }) {
                            RoleBadge(target: target, fixedWidth: true)
                        }
                    }
                    .padding(.horizontal, DS.Space.s)
                    .frame(height: 24)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.vertical, DS.Space.xs)
        .background(DS.Color.surfaceRaised.opacity(0.6), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
    }

    private func label(_ text: String) -> some View {
        Text(text)
            .font(DS.Font.body)
            .foregroundStyle(.secondary)
            .gridColumnAlignment(.trailing)
    }

    private func save() {
        saving = true
        error = nil
        Task {
            let failure: OperationError?
            if let editing {
                failure = await session.setBreakpointCondition(editing.breakpointID, condition)
            } else if let number = lineNumber {
                failure = await session.setBreakpoint(pattern, line: number, condition: condition)
            } else {
                failure = nil
            }
            saving = false
            if let failure { error = failure } else { dismiss() }
        }
    }
}
