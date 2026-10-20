import AppKit
import DeskhandClient
import SwiftUI

/// The location the session stopped at, or the one the person opened: the script's name and URL,
/// the exception it threw, and the source around the line with the current line highlighted and
/// breakpoints in the gutter. Clicking a line number sets or removes a breakpoint there.
struct SourcePanel: View {
    let session: DebugSessionModel?

    var body: some View {
        VStack(spacing: 0) {
            header
            if let session, let state = session.pauseState, CallStackView.isException(state.pauseReason), session.focus == nil {
                ExceptionBanner(session: session, state: state)
            }
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(DS.Code.background)
        }
        .accessibilityElement(children: .contain)
        .task(id: session?.id) { await session?.loadScripts() }
    }

    private var location: SourceFocus? { session?.shownLocation }

    private var header: some View {
        HStack(spacing: DS.Space.s) {
            if let location {
                Image(systemName: "doc.text")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                HStack(alignment: .firstTextBaseline, spacing: 1) {
                    Text(ScriptURL.fileName(location.url))
                        .font(DS.Font.body.weight(.semibold))
                        .lineLimit(1)
                    Text(lineText(location))
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(.secondary)
                }
                Text(ScriptURL.display(location.url))
                    .font(DS.Font.caption)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.head)
                    .help(location.url)
            } else {
                Text(String.ui("debugger.source", "소스"))
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: DS.Space.s)
            if let session, session.focus != nil, session.pauseState != nil {
                Button(String.ui("debugger.source.backToPause", "멈춘 위치로")) { session.selectFrame(session.selectedFrameIndex) }
                    .controlSize(.small)
                    .help(String.ui("debugger.source.backToPause.help", "선택한 호출 프레임의 위치를 다시 봅니다"))
            }
            if let session, !session.scripts.isEmpty, !session.isClosed {
                ScriptMenu(session: session)
            }
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: DebugPaneHeader<EmptyView>.height)
        .background(DS.Color.surfaceRaised.opacity(0.55))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    private func lineText(_ location: SourceFocus) -> String {
        let line = String(location.line)
        guard let column = location.column else { return ":" + line }
        return ":" + line + ":" + String(column)
    }

    @ViewBuilder
    private var content: some View {
        if let session, let location {
            switch session.sourceState(targetID: location.targetID, scriptID: location.scriptID) {
            case .loaded(let source):
                SourceLines(session: session, source: source, location: location, highlight: highlight(session, location))
                    .id(location)
            case .failed(let error):
                DebugPanePlaceholder(symbol: "doc.questionmark", title: String.ui("debugger.source.failed", "소스를 불러오지 못했습니다"),
                                     message: [error.message.asSentence, error.code.map { String.ui("error.codeInline", "오류 코드 \($0)") }]
                                        .compactMap { $0 }.joined(separator: "\n"))
            case .loading, nil:
                SourceSkeleton()
            }
        } else if let session, session.isClosed {
            DebugPanePlaceholder(symbol: "stop.circle", title: DebugFormat.closed(session.info.closeKind, exitCode: session.info.exitCode),
                                 message: String.ui("debugger.source.closed.hint", "콘솔 기록은 남아 있습니다. 다시 디버그하려면 새 세션을 시작하세요."))
        } else {
            DebugPanePlaceholder(symbol: "play.circle", title: String.ui("debugger.source.running", "실행 중입니다"),
                                 message: String.ui("debugger.source.running.hint", "중단점에 닿거나 일시정지(F8)하면 멈춘 위치가 여기에 나타납니다. ‘스크립트’에서 파일을 열어 줄 번호를 누르면 중단점이 걸립니다."))
        }
    }

    private func highlight(_ session: DebugSessionModel, _ location: SourceFocus) -> SourceLines.Highlight {
        guard session.focus == nil, let state = session.pauseState else { return .focus }
        if session.selectedFrameIndex > 0 { return .caller }
        return CallStackView.isException(state.pauseReason) ? .exception : .current
    }
}

/// Loaded scripts of the session by target, to open one in the source panel.
private struct ScriptMenu: View {
    let session: DebugSessionModel

    var body: some View {
        Menu {
            ForEach(session.info.targets) { target in
                let scripts = session.scripts.filter { $0.targetID == target.id }
                if !scripts.isEmpty {
                    Section(DebugFormat.role(target) + " · " + DebugFormat.targetTitle(target)) {
                        ForEach(scripts) { script in
                            Button(ScriptURL.fileName(script.url)) {
                                session.open(SourceFocus(targetID: script.targetID, scriptID: script.scriptID, url: script.url, line: 1))
                            }
                        }
                    }
                }
            }
        } label: {
            Label(String.ui("debugger.source.scripts", "스크립트"), systemImage: "doc.text.magnifyingglass")
                .font(DS.Font.caption)
        }
        .menuStyle(.button)
        .buttonStyle(.borderless)
        .fixedSize()
        .help(String.ui("debugger.source.scripts.help", "로드된 스크립트를 열어 중단점을 겁니다"))
    }
}

/// The paused-on-exception notice: what was thrown, with resume.
private struct ExceptionBanner: View {
    let session: DebugSessionModel
    let state: DebugPauseState

    var body: some View {
        HStack(alignment: .top, spacing: DS.Space.m) {
            Image(systemName: "exclamationmark.octagon.fill")
                .font(.system(size: 15))
                .foregroundStyle(DS.Color.danger)
                .padding(.top, 1)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(state.pauseReason == .promiseRejection
                     ? String.ui("debugger.exception.promise", "처리되지 않은 Promise 거부로 멈췄습니다")
                     : String.ui("debugger.exception.title", "잡히지 않은 예외로 멈췄습니다"))
                    .font(DS.Font.body.weight(.semibold))
                Text(state.exception?.valuePreview ?? String.ui("debugger.exception.unknown", "던져진 값을 읽지 못했습니다"))
                    .font(DebugFormat.mono)
                    .foregroundStyle(DS.Color.danger)
                    .textSelection(.enabled)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: DS.Space.s)
            Button {
                session.togglePause()
            } label: {
                Label(String.ui("debugger.resume", "재개"), systemImage: "play.fill")
            }
            .controlSize(.small)
            .disabled(session.command != nil)
            .help(String.ui("debugger.resume.help", "재개 (F8)"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.danger.opacity(0.09))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.danger.opacity(0.25)).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Source lines around a location with line numbers, breakpoint markers and a highlighted line.
/// It opens with the location a third of the way down.
struct SourceLines: View {
    enum Highlight {
        case current
        case caller
        case exception
        case focus
    }

    static let lineHeight: CGFloat = 18
    static let window = 400

    let session: DebugSessionModel
    let source: DebugSource
    let location: SourceFocus
    let highlight: Highlight

    var body: some View {
        let lines = source.lines
        let first = max(1, location.line - Self.window)
        let last = min(lines.count, location.line + Self.window)
        let numbers = first <= last ? Array(first...last) : []
        let digits = max(3, String(last).count)
        GeometryReader { proxy in
            let content = CGFloat(numbers.count) * Self.lineHeight + DS.Space.s * 2
            let target = CGFloat(location.line - first) * Self.lineHeight + DS.Space.s
            let anchor = Self.anchor(target: target, content: content, viewport: proxy.size.height)
            ScrollView([.vertical, .horizontal]) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(numbers, id: \.self) { number in
                        SourceLineRow(session: session, url: location.url, number: number, text: lines[number - 1], digits: digits,
                                      tint: number == location.line ? tint : nil, isLocation: number == location.line && highlight != .focus)
                            .frame(minWidth: proxy.size.width, alignment: .leading)
                    }
                }
                .padding(.vertical, DS.Space.s)
                .frame(minWidth: proxy.size.width, alignment: .leading)
            }
            .defaultScrollAnchor(UnitPoint(x: 0, y: anchor))
        }
        .overlay(alignment: .bottomLeading) {
            if source.truncated {
                Text(String.ui("debugger.source.truncated", "소스가 2 MiB를 넘어 앞부분만 보입니다"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, DS.Space.s)
                    .padding(.vertical, DS.Space.xs)
                    .background(.regularMaterial, in: Capsule())
                    .padding(DS.Space.s)
            }
        }
    }

    /// The scroll anchor that puts `target` a third of the way down a viewport of `viewport` points.
    static func anchor(target: CGFloat, content: CGFloat, viewport: CGFloat) -> CGFloat {
        guard content > viewport, viewport > 0 else { return 0 }
        let offset = min(max(0, target - viewport / 3), content - viewport)
        return offset / (content - viewport)
    }

    private var tint: Color {
        switch highlight {
        case .current: DS.Code.currentLine
        case .caller: DS.Code.callerLine
        case .exception: DS.Code.exceptionLine
        case .focus: DS.Code.focusLine
        }
    }
}

private struct SourceLineRow: View {
    let session: DebugSessionModel
    let url: String
    let number: Int
    let text: String
    let digits: Int
    let tint: Color?
    let isLocation: Bool
    @State private var hovering = false

    var body: some View {
        let breakpoints = session.breakpoints(url: url, line: number)
        HStack(spacing: 0) {
            gutter(breakpoints)
            Text(JavaScriptHighlighter.highlight(text))
                .font(DebugFormat.mono)
                .lineLimit(1)
                .fixedSize()
                .padding(.leading, DS.Space.s)
                .padding(.trailing, DS.Space.l)
            Spacer(minLength: 0)
        }
        .frame(height: SourceLines.lineHeight)
        .background(tint ?? .clear)
        .overlay(alignment: .leading) {
            if isLocation, tint != nil {
                Rectangle()
                    .fill(locationColor)
                    .frame(width: 2)
                    .offset(x: gutterWidth)
            }
        }
    }

    private var locationColor: Color {
        tint == DS.Code.exceptionLine ? DS.Color.danger : (tint == DS.Code.callerLine ? DS.Color.neutral : DS.Color.success)
    }

    private var gutterWidth: CGFloat { CGFloat(digits) * 7.5 + 22 }

    private func gutter(_ breakpoints: [DebugBreakpoint]) -> some View {
        let marked = !breakpoints.isEmpty
        let enabled = breakpoints.contains { $0.enabled }
        return Button {
            session.toggleBreakpoint(url: url, line: number)
        } label: {
            ZStack(alignment: .trailing) {
                if marked {
                    BreakpointMarker(enabled: enabled, resolved: true, active: session.info.breakpointsActive,
                                     width: gutterWidth - 4, height: 15)
                } else if hovering, !session.isClosed {
                    BreakpointMarker(enabled: true, resolved: false, active: true, width: gutterWidth - 4, height: 15)
                        .opacity(0.5)
                }
                Text(String(number))
                    .font(.system(size: 11, design: .monospaced))
                    .monospacedDigit()
                    .foregroundStyle(numberStyle(marked: marked, enabled: enabled))
                    .padding(.trailing, 9)
            }
            .frame(width: gutterWidth, height: SourceLines.lineHeight, alignment: .trailing)
            .background(DS.Code.gutter)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(session.isClosed)
        .onHover { hovering = $0 }
        .help(marked ? String.ui("debugger.gutter.remove", "이 줄의 중단점 지우기") : String.ui("debugger.gutter.add", "이 줄에 중단점 걸기"))
        .accessibilityLabel(marked ? String.ui("debugger.gutter.remove.a11y", "\(String(number))번 줄 중단점 지우기")
                                   : String.ui("debugger.gutter.add.a11y", "\(String(number))번 줄에 중단점 걸기"))
    }

    private func numberStyle(marked: Bool, enabled: Bool) -> AnyShapeStyle {
        if marked, enabled, session.info.breakpointsActive { return AnyShapeStyle(DS.Color.onFill) }
        if isLocation { return AnyShapeStyle(.primary) }
        return AnyShapeStyle(.tertiary)
    }
}

/// Gray lines in the shape of source while it loads.
private struct SourceSkeleton: View {
    private static let widths: [CGFloat] = [0.42, 0.66, 0.58, 0.3, 0.72, 0.5, 0.62, 0.36, 0.55, 0.68, 0.46, 0.6]

    var body: some View {
        GeometryReader { proxy in
            let rows = max(1, Int((proxy.size.height - DS.Space.l) / SourceLines.lineHeight))
            VStack(alignment: .leading, spacing: 0) {
                ForEach(0..<rows, id: \.self) { index in
                    HStack(spacing: DS.Space.m) {
                        RoundedRectangle(cornerRadius: 3).fill(DS.Color.neutral.opacity(0.14)).frame(width: 22, height: 8)
                        RoundedRectangle(cornerRadius: 3).fill(DS.Color.neutral.opacity(0.14))
                            .frame(width: max(40, proxy.size.width * 0.7 * Self.widths[index % Self.widths.count]), height: 8)
                    }
                    .frame(height: SourceLines.lineHeight)
                    .padding(.leading, DS.Space.m)
                }
            }
            .padding(.vertical, DS.Space.s)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("debugger.source.loading", "소스를 불러오는 중…"))
    }
}

/// Colors JavaScript keywords, strings, numbers and comments of one line.
enum JavaScriptHighlighter {
    static let keywords: Set<String> = [
        "async", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do", "else",
        "export", "extends", "false", "finally", "for", "from", "function", "if", "import", "in", "instanceof", "let", "new", "null",
        "of", "return", "static", "super", "switch", "this", "throw", "true", "try", "typeof", "undefined", "var", "void", "while",
        "with", "yield",
    ]

    enum Token: Equatable {
        case plain
        case keyword
        case string
        case number
        case comment
    }

    /// The line split into runs and their token kinds.
    static func tokens(_ line: String) -> [(text: String, token: Token)] {
        var runs: [(text: String, token: Token)] = []
        var plain = ""
        let chars = Array(line)
        var i = 0
        func flush() {
            if !plain.isEmpty { runs.append((plain, .plain)); plain = "" }
        }
        while i < chars.count {
            let c = chars[i]
            if c == "/", i + 1 < chars.count, chars[i + 1] == "/" {
                flush()
                runs.append((String(chars[i...]), .comment))
                return runs
            }
            if c == "\"" || c == "'" || c == "`" {
                flush()
                var j = i + 1
                while j < chars.count, chars[j] != c {
                    if chars[j] == "\\" { j += 1 }
                    j += 1
                }
                let end = min(j, chars.count - 1)
                runs.append((String(chars[i...end]), .string))
                i = end + 1
                continue
            }
            if c.isNumber, (i == 0 || !(chars[i - 1].isLetter || chars[i - 1] == "_" || chars[i - 1] == "$")) {
                flush()
                var j = i
                while j < chars.count, chars[j].isNumber || chars[j] == "." || chars[j] == "_" { j += 1 }
                runs.append((String(chars[i..<j]), .number))
                i = j
                continue
            }
            if c.isLetter || c == "_" || c == "$" {
                var j = i
                while j < chars.count, chars[j].isLetter || chars[j].isNumber || chars[j] == "_" || chars[j] == "$" { j += 1 }
                let word = String(chars[i..<j])
                if keywords.contains(word), i == 0 || chars[i - 1] != "." {
                    flush()
                    runs.append((word, .keyword))
                } else {
                    plain += word
                }
                i = j
                continue
            }
            plain.append(c)
            i += 1
        }
        flush()
        return runs
    }

    static func highlight(_ line: String) -> AttributedString {
        var result = AttributedString()
        for run in tokens(line) {
            var piece = AttributedString(run.text)
            switch run.token {
            case .plain: piece.foregroundColor = .primary
            case .keyword: piece.foregroundColor = DS.Code.keyword
            case .string: piece.foregroundColor = DS.Code.string
            case .number: piece.foregroundColor = DS.Code.number
            case .comment: piece.foregroundColor = DS.Code.comment
            }
            result += piece
        }
        return result
    }
}
