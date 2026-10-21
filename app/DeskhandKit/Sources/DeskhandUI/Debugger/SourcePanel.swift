import AppKit
import DeskhandClient
import SwiftUI

/// The location the session stopped at, or the one the person opened: the script's name and URL,
/// the exception it threw, and the source around the line with the current line highlighted and
/// breakpoints in a gutter that stays put while the code scrolls sideways. Clicking a line number
/// sets or removes a breakpoint there. Without a location it says why: running, a pause still
/// being read or not read, a pause waiting for code to run, a detached target or a closed session.
struct SourcePanel: View {
    let model: DebuggerModel
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
    }

    private var location: SourceFocus? { session?.shownLocation }

    private var header: some View {
        HStack(spacing: DS.Space.s) {
            if let location {
                Image(systemName: "doc.text")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                HStack(alignment: .firstTextBaseline, spacing: 0) {
                    Text(ScriptURL.fileName(location.url))
                        .font(DS.Font.body.weight(.semibold))
                        .lineLimit(1)
                    Text(lineText(location))
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(.secondary)
                }
                .fixedSize()
                Text(ScriptURL.display(location.url))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
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
                SourceLines(model: model, session: session, source: source, location: location, highlight: highlight(session))
                    .id(location)
            case .failed(let error):
                RetryPanel(symbol: "doc.questionmark", title: String.ui("debugger.source.failed", "소스를 불러오지 못했습니다"),
                           error: error) { session.retrySource() }
            case .loading, nil:
                LoadingLine(text: String.ui("debugger.source.loading", "소스를 불러오는 중…"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        } else if let session, session.isClosed {
            DebugPanePlaceholder(symbol: "stop.circle", title: String.ui("debugger.source.closed", "닫힌 세션입니다"),
                                 message: String.ui("debugger.source.closed.hint", "콘솔 기록은 남아 있습니다. 다시 디버그하려면 새 세션을 시작하세요."))
        } else if let session, session.awaitingState {
            if let targetID = session.selectedTargetID, let failure = session.stateFailures[targetID] {
                RetryPanel(symbol: "pause.circle", title: String.ui("debugger.source.stateFailed", "멈춘 위치를 불러오지 못했습니다"),
                           error: failure) { session.retryState() }
            } else {
                LoadingLine(text: String.ui("debugger.source.stateLoading", "멈춘 위치를 불러오는 중…"))
            }
        } else if let session, session.pausePending {
            DebugPanePlaceholder(symbol: "pause.circle",
                                 title: String.ui("debugger.source.pausePending", "일시정지를 기다리는 중입니다"),
                                 message: String.ui("debugger.source.pausePending.hint", "대상이 지금은 코드를 실행하지 않습니다. 다음 코드가 실행되는 순간 멈춥니다. 창을 클릭하거나 앱을 조작해 보세요. 그만두려면 일시정지 취소(F8 또는 ⌘\\)를 누르세요."))
        } else if let session, let target = session.selectedTarget, !target.attached {
            VStack(spacing: DS.Space.m) {
                DebugPanePlaceholder(symbol: "cable.connector",
                                     title: String.ui("debugger.source.detached", "이 대상에는 연결되어 있지 않습니다"),
                                     message: String.ui("debugger.source.detached.hint", "연결하면 이 대상의 중단점과 콘솔을 받습니다."))
                    .fixedSize(horizontal: false, vertical: true)
                Button(String.ui("debugger.target.attach", "연결")) { session.attachTarget(target.id) }
                    .controlSize(.small)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            DebugPanePlaceholder(symbol: "play.circle", title: String.ui("debugger.source.running", "실행 중입니다"),
                                 message: String.ui("debugger.source.running.hint", "중단점에 닿거나 일시정지(F8 또는 ⌘\\)하면 멈춘 위치와 호출 스택, 변수가 나타납니다. 스크립트(⇧⌘O)를 열어 줄 번호를 누르면 중단점이 걸립니다."))
        }
    }

    private func highlight(_ session: DebugSessionModel) -> SourceLines.Highlight {
        guard session.focus == nil, let state = session.pauseState else { return .focus }
        if session.selectedFrameIndex > 0 { return .caller }
        return CallStackView.isException(state.pauseReason) ? .exception : .current
    }
}

/// A pane message for a read that failed: what failed, the daemon's reason and code, and 다시 시도.
struct RetryPanel: View {
    let symbol: String
    let title: String
    let error: OperationError
    let retry: () -> Void

    var body: some View {
        VStack(spacing: DS.Space.m) {
            DebugPanePlaceholder(symbol: symbol, title: title, message: error.sentences)
                .fixedSize(horizontal: false, vertical: true)
            if let code = error.code {
                ErrorCodeLine(code: code)
            }
            Button(String.ui("action.retry", "다시 시도"), action: retry)
                .controlSize(.small)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
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
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(state.pauseReason == .promiseRejection
                     ? String.ui("debugger.exception.promise", "처리되지 않은 Promise 거부로 멈췄습니다")
                     : String.ui("debugger.exception.title", "예외로 멈췄습니다"))
                    .font(DS.Font.body.weight(.semibold))
                Text(state.exception?.valuePreview ?? String.ui("debugger.exception.unknown", "던져진 값을 읽지 못했습니다"))
                    .font(DebugFormat.mono)
                    .foregroundStyle(DS.Code.errorText)
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
            .help(String.ui("debugger.resume.help", "재개 (F8 · ⌘\\)"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Code.errorRow)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.danger.opacity(0.25)).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Source lines around a location with a pinned gutter of line numbers and breakpoint markers, and
/// a highlighted line. Lines longer than `SourceText.windowWidth` show a window around the
/// location's column between elision markers. It opens with the location's line a third of the
/// way down on a row boundary and, for a far column, the column a third of the way across.
struct SourceLines: View {
    enum Highlight {
        case current
        case caller
        case exception
        case focus
    }

    static let lineHeight: CGFloat = 20
    /// Lines shown on each side of the location.
    static let window = 400
    static let codeLeading = DS.Space.s
    static let codeTrailing = DS.Space.l

    let model: DebuggerModel
    let session: DebugSessionModel
    let source: LoadedSource
    let location: SourceFocus
    let highlight: Highlight

    var body: some View {
        let lines = source.text.lines
        let first = max(1, location.line - Self.window)
        let last = min(lines.count, location.line + Self.window)
        let numbers = first <= last ? Array(first...last) : []
        let gutter = Self.gutterWidth(digits: String(last).count)
        let column = location.column ?? 1
        GeometryReader { proxy in
            SourceScroll(model: model, session: session, source: source.text, url: location.url, numbers: numbers,
                         locationLine: location.line, column: column, gutter: gutter, tint: tint, highlight: highlight,
                         viewport: proxy.size)
        }
        .overlay(alignment: .bottomTrailing) {
            if source.source.truncated {
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

    /// The width of a gutter for line numbers of `digits` digits, on the 4-pt grid.
    static func gutterWidth(digits: Int) -> CGFloat {
        let raw = CGFloat(max(3, digits)) * 7 + 24
        return (raw / 4).rounded(.up) * 4
    }

    /// The vertical scroll offset that puts `target` a third of the way down a viewport of
    /// `viewport` points, on a boundary of rows `row` points tall, within the content.
    static func offset(target: CGFloat, content: CGFloat, viewport: CGFloat, row: CGFloat) -> CGFloat {
        guard content > viewport, viewport > 0, row > 0 else { return 0 }
        let raw = max(0, target - viewport / 3)
        let snapped = (raw / row).rounded(.down) * row
        return min(snapped, content - viewport)
    }

    /// The horizontal offset that shows a point `x` points into the content a third of the way
    /// across, or 0 when it already shows in the first two thirds.
    static func horizontalOffset(x: CGFloat, viewport: CGFloat) -> CGFloat {
        guard viewport > 0, x > viewport * 2 / 3 else { return 0 }
        return (x - viewport / 3).rounded(.down)
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

/// The scroll view of `SourceLines`, positioned at the location when it appears and again when
/// the viewport changes before the person scrolls.
private struct SourceScroll: View {
    let model: DebuggerModel
    let session: DebugSessionModel
    let source: SourceText
    let url: String
    let numbers: [Int]
    let locationLine: Int
    let column: Int
    let gutter: CGFloat
    let tint: Color
    let highlight: SourceLines.Highlight
    let viewport: CGSize
    @State private var position: ScrollPosition
    @State private var scrollX: CGFloat = 0
    @State private var positioned: CGSize

    init(model: DebuggerModel, session: DebugSessionModel, source: SourceText, url: String, numbers: [Int], locationLine: Int, column: Int,
         gutter: CGFloat, tint: Color, highlight: SourceLines.Highlight, viewport: CGSize) {
        self.model = model
        self.session = session
        self.source = source
        self.url = url
        self.numbers = numbers
        self.locationLine = locationLine
        self.column = column
        self.gutter = gutter
        self.tint = tint
        self.highlight = highlight
        self.viewport = viewport
        let point = Self.start(source: source, numbers: numbers, line: locationLine, column: column, gutter: gutter, viewport: viewport)
        _position = State(initialValue: ScrollPosition(point: point))
        _positioned = State(initialValue: viewport)
    }

    /// The scroll offset that shows the location's line and column.
    static func start(source: SourceText, numbers: [Int], line: Int, column: Int, gutter: CGFloat, viewport: CGSize) -> CGPoint {
        let row = SourceLines.lineHeight
        let index = CGFloat((numbers.firstIndex(of: line) ?? 0))
        let content = CGFloat(numbers.count) * row + DS.Space.s
        let y = SourceLines.offset(target: index * row, content: content, viewport: viewport.height, row: row)
        let highlighted = source.highlightedWindow(line: line, around: column)
        let prefix = String(highlighted.window.text.utf16.prefix(max(0, highlighted.window.column - 1))) ?? ""
        let font = NSFont.monospacedSystemFont(ofSize: DebugFormat.monoSize, weight: .regular)
        let prefixWidth = NSAttributedString(string: prefix, attributes: [.font: font]).size().width
        let elision = highlighted.window.hiddenBefore > 0 ? ElisionMarker.width + DS.Space.xs : 0
        let x = SourceLines.codeLeading + elision + prefixWidth
        return CGPoint(x: SourceLines.horizontalOffset(x: x, viewport: viewport.width - gutter), y: y)
    }

    var body: some View {
        let digits = String(numbers.last ?? 1).count
        ScrollView([.vertical, .horizontal]) {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(numbers, id: \.self) { number in
                    SourceLineRow(model: model, session: session, url: url, number: number,
                                  line: source.highlightedWindow(line: number, around: column), digits: digits, gutter: gutter,
                                  scrollX: scrollX, tint: number == locationLine ? tint : nil,
                                  isLocation: number == locationLine && highlight != .focus, locationTint: locationTint)
                        .frame(minWidth: viewport.width, alignment: .leading)
                }
            }
            .padding(.bottom, DS.Space.s)
            .frame(minWidth: viewport.width, minHeight: viewport.height, alignment: .topLeading)
        }
        .scrollPosition($position)
        .onScrollGeometryChange(for: CGFloat.self) { $0.contentOffset.x } action: { _, x in
            scrollX = max(0, x)
        }
        .onAppear {
            let point = Self.start(source: source, numbers: numbers, line: locationLine, column: column, gutter: gutter, viewport: viewport)
            position.scrollTo(point: point)
        }
        .onChange(of: viewport) { _, size in
            guard !position.isPositionedByUser, abs(size.height - positioned.height) > 1 || abs(size.width - positioned.width) > 1 else { return }
            positioned = size
            position.scrollTo(point: Self.start(source: source, numbers: numbers, line: locationLine, column: column, gutter: gutter, viewport: size))
        }
    }

    private var locationTint: Color {
        switch highlight {
        case .exception: DS.Color.danger
        case .caller: DS.Color.neutral
        default: DS.Color.success
        }
    }
}

/// `앞 9,001자` before a window of a long line, or `뒤 9,004자` after it.
struct ElisionMarker: View {
    static let width: CGFloat = 88

    let count: Int
    let leading: Bool

    var body: some View {
        let number = DS.Format.count(count)
        Text(leading ? String.ui("debugger.source.hiddenBefore", "‹ 앞 \(number)자") : String.ui("debugger.source.hiddenAfter", "뒤 \(number)자 ›"))
            .font(.system(size: 10, weight: .medium))
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .padding(.horizontal, DS.Space.xs)
            .frame(width: leading ? Self.width : nil, height: 16)
            .background(DS.Color.neutral.opacity(0.16), in: Capsule())
            .help(String.ui("debugger.source.window.help", "긴 줄은 멈춘 열 주변 \(DS.Format.count(SourceText.windowWidth))자만 보입니다"))
    }
}

private struct SourceLineRow: View {
    let model: DebuggerModel
    let session: DebugSessionModel
    let url: String
    let number: Int
    let line: HighlightedLine
    let digits: Int
    let gutter: CGFloat
    let scrollX: CGFloat
    let tint: Color?
    let isLocation: Bool
    let locationTint: Color

    var body: some View {
        HStack(spacing: 0) {
            SourceGutterCell(model: model, session: session, url: url, number: number, width: gutter, isLocation: isLocation,
                             locationTint: tint == nil ? nil : locationTint)
                .offset(x: scrollX)
                .zIndex(1)
            HStack(spacing: DS.Space.xs) {
                if line.window.hiddenBefore > 0 { ElisionMarker(count: line.window.hiddenBefore, leading: true) }
                Text(line.text)
                    .font(DebugFormat.mono)
                    .lineLimit(1)
                    .fixedSize()
                if line.window.hiddenAfter > 0 { ElisionMarker(count: line.window.hiddenAfter, leading: false) }
            }
            .padding(.leading, SourceLines.codeLeading)
            .padding(.trailing, SourceLines.codeTrailing)
            Spacer(minLength: 0)
        }
        .frame(height: SourceLines.lineHeight)
        .background(tint ?? .clear)
    }
}

/// A line number with its breakpoint markers; clicking sets or removes the line's breakpoint,
/// and the context menu turns it on or off, edits its condition or removes it.
private struct SourceGutterCell: View {
    let model: DebuggerModel
    let session: DebugSessionModel
    let url: String
    let number: Int
    let width: CGFloat
    let isLocation: Bool
    /// The color of the location bar at the trailing edge, on the location line.
    let locationTint: Color?
    @State private var hovering = false

    var body: some View {
        let breakpoints = session.breakpoints(url: url, line: number)
        let marked = !breakpoints.isEmpty
        let enabled = breakpoints.contains { $0.enabled }
        let conditional = breakpoints.contains { !($0.condition ?? "").isEmpty }
        let pending = session.pendingLines.contains(url + ":" + String(number))
        Button {
            session.toggleBreakpoint(url: url, line: number)
        } label: {
            ZStack(alignment: .trailing) {
                if marked {
                    BreakpointMarker(enabled: enabled, resolved: true, active: session.info.breakpointsActive && !session.isClosed,
                                     conditional: conditional, width: width - DS.Space.xs, height: 16)
                } else if hovering, !session.isClosed {
                    BreakpointMarker(enabled: true, resolved: false, active: true, width: width - DS.Space.xs, height: 16)
                        .opacity(0.5)
                }
                Text(String(number))
                    .font(.system(size: 11, design: .monospaced))
                    .monospacedDigit()
                    .foregroundStyle(numberStyle(marked: marked, enabled: enabled))
                    .padding(.trailing, DS.Space.s)
            }
            .frame(width: width, height: SourceLines.lineHeight, alignment: .trailing)
            .background(DS.Code.gutter)
            .background(DS.Code.background)
            .overlay(alignment: .trailing) {
                if let locationTint {
                    Rectangle().fill(locationTint).frame(width: 2)
                } else {
                    Rectangle().fill(DS.Color.separator.opacity(0.6)).frame(width: 1)
                }
            }
            .opacity(pending ? 0.6 : 1)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(session.isClosed)
        .onHover { hovering = $0 }
        .contextMenu {
            if let breakpoint = breakpoints.first, !session.isClosed {
                Button(breakpoint.enabled ? String.ui("debugger.breakpoint.disable", "중단점 끄기") : String.ui("debugger.breakpoint.enable", "중단점 켜기")) {
                    session.setBreakpointEnabled(breakpoint.breakpointID, !breakpoint.enabled)
                }
                Button(String.ui("debugger.breakpoint.editCondition", "조건 편집…")) { model.sheet = .condition(breakpoint.breakpointID) }
                Divider()
                Button(String.ui("debugger.breakpoint.remove", "중단점 지우기"), role: .destructive) {
                    for bp in breakpoints { session.removeBreakpoint(bp.breakpointID) }
                }
            } else if !session.isClosed {
                Button(String.ui("debugger.gutter.add", "이 줄에 중단점 걸기")) { session.toggleBreakpoint(url: url, line: number) }
            }
        }
        .help(marked ? String.ui("debugger.gutter.remove", "이 줄의 중단점 지우기") : String.ui("debugger.gutter.add", "이 줄에 중단점 걸기"))
        .accessibilityLabel(marked ? String.ui("debugger.gutter.remove.a11y", "\(String(number))번 줄 중단점 지우기")
                                   : String.ui("debugger.gutter.add.a11y", "\(String(number))번 줄에 중단점 걸기"))
    }

    private func numberStyle(marked: Bool, enabled: Bool) -> AnyShapeStyle {
        if marked, enabled, session.info.breakpointsActive, !session.isClosed { return AnyShapeStyle(DS.Color.onFill) }
        if isLocation { return AnyShapeStyle(.primary) }
        return AnyShapeStyle(.secondary)
    }
}
