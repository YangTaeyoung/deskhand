import DeskhandClient
import SwiftUI

/// Adds a breakpoint by script and line: the script as a file name, an exact URL or a regular
/// expression, with the loaded scripts offered; an optional condition.
public struct BreakpointSheet: View {
    public enum Match: String, CaseIterable, Identifiable, Sendable {
        case fileName
        case url
        case regex

        public var id: String { rawValue }
    }

    let session: DebugSessionModel
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
        self.dismiss = dismiss
        _match = State(initialValue: match)
        _script = State(initialValue: script)
        _line = State(initialValue: line)
        _condition = State(initialValue: condition)
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
        let scripts = session.scripts.filter { needle.isEmpty || $0.url.localizedCaseInsensitiveContains(needle) }
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

    /// Scripts the pattern matches now.
    private var matched: Int? {
        guard !script.trimmingCharacters(in: .whitespaces).isEmpty, let request = try? pattern.match() else { return nil }
        let probe: DebugBreakpoint
        switch request {
        case .url(let url): probe = DebugBreakpoint(breakpointID: "", url: url, line: 1)
        case .regex(let regex): probe = DebugBreakpoint(breakpointID: "", urlRegex: regex, line: 1)
        }
        return session.scripts.filter { BreakpointPattern.matches(probe, url: $0.url) }.count
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                BreakpointMarker(enabled: true, resolved: true, width: 30, height: 20)
                    .padding(.top, 4)
                    .frame(width: 36)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(String.ui("debugger.breakpoint.sheet.title", "중단점 추가"))
                        .font(DS.Font.title)
                        .accessibilityAddTraits(.isHeader)
                    Text(String.ui("debugger.breakpoint.sheet.subtitle", "\(session.info.name)의 모든 대상에 겁니다. 아직 로드되지 않은 스크립트는 로드될 때 연결됩니다."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.m) {
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
                GridRow {
                    label(String.ui("debugger.breakpoint.sheet.condition", "조건"))
                    VStack(alignment: .leading, spacing: DS.Space.xs) {
                        TextField(String.ui("debugger.breakpoint.sheet.condition.prompt", "선택 사항 · 예: order.total > 40000"), text: $condition)
                            .font(DS.Font.mono)
                            .textFieldStyle(.roundedBorder)
                            .accessibilityLabel(String.ui("debugger.breakpoint.sheet.condition", "조건"))
                        Text(String.ui("debugger.breakpoint.sheet.condition.hint", "참이 되는 JavaScript 식을 쓰면 그때만 멈춥니다."))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if let error {
                StatusNotice(symbol: "exclamationmark.octagon.fill", tint: DS.Color.danger,
                             title: String.ui("debugger.failed.breakpointSet", "중단점을 걸지 못했습니다"), message: error.message.asSentence,
                             code: error.code)
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
                        Text(String.ui("debugger.breakpoint.sheet.add", "추가")).frame(minWidth: 56)
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(lineNumber == nil || script.trimmingCharacters(in: .whitespaces).isEmpty || saving)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 560)
        .task { await session.loadScripts() }
    }

    private var prompt: String {
        switch match {
        case .fileName: .ui("debugger.breakpoint.prompt.fileName", "예: checkout.js")
        case .url: .ui("debugger.breakpoint.prompt.url", "예: app://shop-admin/renderer/checkout.js")
        case .regex: .ui("debugger.breakpoint.prompt.regex", "예: renderer/.*\\.js$")
        }
    }

    @ViewBuilder
    private var matchLine: some View {
        if let matched {
            if matched == 0, !suggestions.isEmpty {
                Label(String.ui("debugger.breakpoint.sheet.pickSuggestion", "이름이 정확히 같은 스크립트가 없습니다. 아래 목록에서 고르세요."),
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
                Label(String.ui("debugger.breakpoint.sheet.matches", "로드된 스크립트 \(count)개와 일치합니다"), systemImage: "checkmark.circle.fill")
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Color.success)
            }
        } else if match == .regex, !script.isEmpty, case .failure(let problem) = Result(catching: { try pattern.match() }) {
            Label(problem.localizedDescription, systemImage: "exclamationmark.triangle.fill")
                .font(DS.Font.caption)
                .foregroundStyle(DS.Color.warning)
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
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                            .truncationMode(.head)
                        Spacer(minLength: 0)
                        if let target = session.info.targets.first(where: { $0.id == item.targetID }) {
                            RoleBadge(target: target)
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
        guard let number = lineNumber else { return }
        saving = true
        error = nil
        Task {
            let failure = await session.setBreakpoint(pattern, line: number, condition: condition)
            saving = false
            if let failure { error = failure } else { dismiss() }
        }
    }
}
