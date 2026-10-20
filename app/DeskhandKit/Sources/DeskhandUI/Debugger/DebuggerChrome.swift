import AppKit
import DeskhandClient
import SwiftUI

/// Texts, symbols and colors the debugger panes share.
enum DebugFormat {
    static func frameName(_ function: String) -> String {
        function.isEmpty ? .ui("debugger.anonymous", "(익명 함수)") : function
    }

    /// `api.js:25`.
    static func location(url: String, line: Int) -> String {
        let name = ScriptURL.fileName(url)
        let number = String(line)
        return name + ":" + number
    }

    /// `at applyDiscount (api.js:34)`, as V8 prints stack frames.
    static func stackLine(_ frame: StackFrameInfo) -> String {
        let name = frame.function.isEmpty ? "<anonymous>" : frame.function
        return "at " + name + " (" + location(url: frame.url, line: frame.line) + ")"
    }

    /// 재개 while paused, 일시정지 취소 while a pause waits for code to run, else 일시정지.
    static func pauseTitle(paused: Bool, pending: Bool) -> String {
        if paused { return .ui("debugger.resume", "재개") }
        if pending { return .ui("debugger.pauseCancel", "일시정지 취소") }
        return .ui("debugger.pause", "일시정지")
    }

    static func reason(_ reason: DebugPauseReason) -> String {
        switch reason {
        case .breakpoint: .ui("debugger.reason.breakpoint", "중단점")
        case .exception: .ui("debugger.reason.exception", "예외")
        case .promiseRejection: .ui("debugger.reason.promise", "처리되지 않은 Promise 거부")
        case .pauseRequest: .ui("debugger.reason.pause", "일시정지 요청")
        case .step: .ui("debugger.reason.step", "한 단계 실행")
        case .start: .ui("debugger.reason.start", "첫 줄")
        case .other: .ui("debugger.reason.other", "debugger 문 또는 기타")
        }
    }

    static func closed(_ reason: DebugCloseReason?, exitCode: Int?, kind: DebugSessionKind?) -> String {
        switch reason {
        case .requested:
            return kind == .attach ? .ui("debugger.closed.detached", "연결을 해제했습니다") : .ui("debugger.closed.requested", "세션을 닫았습니다")
        case .processExited:
            if let exitCode {
                let code = String(exitCode)
                return .ui("debugger.closed.exitedCode", "앱이 종료되었습니다 (종료 코드 \(code))")
            }
            return .ui("debugger.closed.exited", "앱이 종료되었습니다")
        case .connectionLost: return .ui("debugger.closed.connection", "원격 PC와 연결이 끊겨 세션이 닫혔습니다")
        case nil: return .ui("debugger.closed.unknown", "데몬이 더 이상 이 세션을 알지 못합니다")
        }
    }

    /// The closed text of the session list: shorter, the remote PC left out.
    static func closedShort(_ reason: DebugCloseReason?, exitCode: Int?, kind: DebugSessionKind?) -> String {
        switch reason {
        case .connectionLost: .ui("debugger.closedShort.connection", "연결이 끊겨 닫혔습니다")
        case nil: .ui("debugger.closedShort.unknown", "데몬에 없는 세션입니다")
        default: closed(reason, exitCode: exitCode, kind: kind)
        }
    }

    static func role(_ target: DebugTarget) -> String {
        switch target.targetRole {
        case .main: .ui("debugger.role.main", "메인")
        case .renderer:
            switch target.type {
            case "worker", "service_worker", "shared_worker": .ui("debugger.role.worker", "워커")
            case "iframe", "webview": .ui("debugger.role.frame", "프레임")
            default: .ui("debugger.role.renderer", "렌더러")
            }
        case nil: target.role
        }
    }

    static func targetTitle(_ target: DebugTarget) -> String {
        target.title.isEmpty ? ScriptURL.display(target.url) : target.title
    }

    static func exceptionMode(_ mode: ExceptionPauseMode) -> String {
        switch mode {
        case .none: .ui("debugger.exceptions.none", "멈추지 않음")
        case .uncaught: .ui("debugger.exceptions.uncaught", "잡히지 않은 것만")
        case .all: .ui("debugger.exceptions.all", "모두")
        }
    }

    /// `Claude Code` for an AI client, the client name or 사람 for others.
    static func actor(_ actor: DebugActor) -> String {
        if let name = actor.clientName, !name.isEmpty { return name }
        switch actor.actor {
        case "ai": return .ui("debugger.actor.ai", "AI")
        case "system": return .ui("debugger.actor.system", "Deskhand")
        default: return .ui("debugger.actor.human", "사람")
        }
    }

    /// The one-line reason under the targets of a session without main.
    static func mainUnavailableShort(_ reason: MainUnavailableReason) -> String {
        switch reason {
        case .inspectorDisabled: .ui("debugger.main.disabledShort", "메인: --inspect를 무시함")
        case .inspectorNotResponding: .ui("debugger.main.notRespondingShort", "메인: 검사기 응답 없음")
        }
    }

    static func rendererUnavailable(_ reason: RendererUnavailableReason) -> String {
        switch reason {
        case .remoteDebuggingDisabled:
            .ui("debugger.renderer.disabled", "이 앱은 원격 디버깅 포트를 열지 않아 렌더러를 디버그할 수 없습니다. 메인 프로세스만 연결했습니다.")
        case .remoteDebuggingNotResponding:
            .ui("debugger.renderer.notResponding", "렌더러의 DevTools가 응답하지 않아 메인 프로세스만 연결했습니다. 응답하면 자동으로 연결합니다.")
        }
    }

    /// The one-line reason under the targets of a session without a renderer.
    static func rendererUnavailableShort(_ reason: RendererUnavailableReason) -> String {
        switch reason {
        case .remoteDebuggingDisabled: .ui("debugger.renderer.disabledShort", "렌더러: 원격 디버깅 꺼짐")
        case .remoteDebuggingNotResponding: .ui("debugger.renderer.notRespondingShort", "렌더러: DevTools 응답 없음")
        }
    }

    static func mainUnavailable(_ reason: MainUnavailableReason) -> String {
        switch reason {
        case .inspectorDisabled:
            .ui("debugger.main.disabled", "이 앱은 --inspect를 무시하도록 빌드되어 메인 프로세스를 디버그할 수 없습니다. 렌더러만 연결했습니다.")
        case .inspectorNotResponding:
            .ui("debugger.main.notResponding", "메인 프로세스의 검사기가 응답하지 않아 렌더러만 연결했습니다.")
        }
    }

    static func levelSymbol(_ level: ConsoleLevel) -> String? {
        switch level {
        case .error: "xmark.octagon.fill"
        case .warning: "exclamationmark.triangle.fill"
        case .info: "info.circle.fill"
        case .debug: "ant"
        case .log: nil
        }
    }

    static func levelTint(_ level: ConsoleLevel) -> Color {
        switch level {
        case .error: DS.Color.danger
        case .warning: DS.Color.warning
        case .info: DS.Color.accent
        case .debug, .log: .secondary
        }
    }

    static func levelTitle(_ level: ConsoleLevel) -> String {
        switch level {
        case .error: .ui("debugger.level.error", "오류")
        case .warning: .ui("debugger.level.warning", "경고")
        case .info: .ui("debugger.level.info", "정보")
        case .log: .ui("debugger.level.log", "로그")
        case .debug: .ui("debugger.level.debug", "디버그")
        }
    }

    /// The color of a value preview by its type.
    static func valueColor(_ value: RemoteValue) -> Color {
        switch value.type {
        case "string": DS.Code.string
        case "number", "bigint": DS.Code.number
        case "boolean", "symbol": DS.Code.keyword
        case "undefined": DS.Code.comment
        case "object" where value.subtype == "null": DS.Code.comment
        case "object" where value.subtype == "error": DS.Code.errorText
        case "function": .secondary
        default: .primary
        }
    }

    /// `14:32:08` in 24-hour time.
    static func clock(_ date: Date) -> String {
        date.formatted(Date.VerbatimFormatStyle(
            format: "\(hour: .twoDigits(clock: .twentyFourHour, hourCycle: .zeroBased)):\(minute: .twoDigits):\(second: .twoDigits)",
            timeZone: .current, calendar: Calendar(identifier: .gregorian)))
    }

    static let monoSize: CGFloat = 12
    static var mono: Font { .system(size: monoSize, design: .monospaced) }
    static var smallMono: Font { .system(size: 11, design: .monospaced) }
}

/// A pane title bar: a title, an optional detail, and trailing controls.
struct DebugPaneHeader<Trailing: View>: View {
    static var height: CGFloat { 32 }

    let title: String
    var detail: String?
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(title)
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .accessibilityAddTraits(.isHeader)
                .fixedSize()
            if let detail {
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: DS.Space.s)
            trailing
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: Self.height)
        .background(DS.Color.surfaceRaised.opacity(0.55))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }
}

extension DebugPaneHeader where Trailing == EmptyView {
    init(title: String, detail: String? = nil) {
        self.init(title: title, detail: detail) { EmptyView() }
    }
}

/// Two panes split along `axis` by a draggable hairline; the first takes `fraction` of the space
/// within its `minimum` and the second's.
struct DebugSplit<First: View, Second: View>: View {
    let axis: Axis
    @Binding var fraction: Double
    var minimumFirst: CGFloat = 120
    var minimumSecond: CGFloat = 120
    @ViewBuilder var first: First
    @ViewBuilder var second: Second
    @State private var dragStart: Double?

    var body: some View {
        GeometryReader { proxy in
            let length = axis == .horizontal ? proxy.size.width : proxy.size.height
            let firstLength = Self.firstLength(total: length, fraction: fraction, minimumFirst: minimumFirst, minimumSecond: minimumSecond)
            if axis == .horizontal {
                HStack(spacing: 0) {
                    first.frame(width: max(0, firstLength))
                    divider(length: length)
                    second.frame(maxWidth: .infinity)
                }
            } else {
                VStack(spacing: 0) {
                    first.frame(height: max(0, firstLength))
                    divider(length: length)
                    second.frame(maxHeight: .infinity)
                }
            }
        }
        .frame(minWidth: 0, minHeight: 0)
    }

    static func firstLength(total: CGFloat, fraction: Double, minimumFirst: CGFloat, minimumSecond: CGFloat) -> CGFloat {
        guard total > minimumFirst + minimumSecond else { return total / 2 }
        return min(max(total * fraction, minimumFirst), total - minimumSecond)
    }

    private func divider(length: CGFloat) -> some View {
        Rectangle()
            .fill(DS.Color.separator)
            .frame(width: axis == .horizontal ? 1 : nil, height: axis == .vertical ? 1 : nil)
            .overlay {
                Color.clear
                    .frame(width: axis == .horizontal ? 8 : nil, height: axis == .vertical ? 8 : nil)
                    .contentShape(Rectangle())
                    .pointerStyle(.frameResize(position: axis == .horizontal ? .trailing : .bottom))
                    .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .global)
                        .onChanged { value in
                            guard length > 0 else { return }
                            let start = dragStart ?? fraction
                            dragStart = start
                            let delta = axis == .horizontal ? value.translation.width : value.translation.height
                            fraction = min(0.85, max(0.15, start + Double(delta / length)))
                        }
                        .onEnded { _ in dragStart = nil })
            }
            .accessibilityHidden(true)
    }
}

/// The role of a target as a small capsule: 메인, 렌더러, 워커 or 프레임.
struct RoleBadge: View {
    /// A width every role fits, for aligned columns.
    static let width: CGFloat = 48

    let target: DebugTarget
    /// Drawn on an accent selection.
    var inverted = false
    /// Takes `width` so a column of badges lines up.
    var fixedWidth = false

    var body: some View {
        let main = target.targetRole == .main
        Text(DebugFormat.role(target))
            .font(.system(size: NSFont.smallSystemFontSize - 1, weight: .semibold))
            .foregroundStyle(foreground(main: main))
            .padding(.horizontal, DS.Space.s)
            .frame(minWidth: fixedWidth ? Self.width : nil)
            .frame(height: 16)
            .background(background(main: main), in: Capsule())
            .fixedSize()
    }

    private func foreground(main: Bool) -> AnyShapeStyle {
        if inverted { return AnyShapeStyle(DS.Color.onFill) }
        return main ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary)
    }

    private func background(main: Bool) -> Color {
        if inverted { return DS.Color.onFill.opacity(0.22) }
        return main ? DS.Color.accent.opacity(0.14) : DS.Color.neutral.opacity(0.16)
    }
}

/// The breakpoint marker of the gutter and the breakpoints list: a tag pointing right.
struct BreakpointMarkerShape: Shape {
    func path(in rect: CGRect) -> Path {
        let tip = min(rect.height * 0.5, rect.width * 0.4)
        let r = min(2.5, rect.height / 4)
        var path = Path()
        path.move(to: CGPoint(x: rect.minX + r, y: rect.minY))
        path.addLine(to: CGPoint(x: rect.maxX - tip, y: rect.minY))
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.midY))
        path.addLine(to: CGPoint(x: rect.maxX - tip, y: rect.maxY))
        path.addLine(to: CGPoint(x: rect.minX + r, y: rect.maxY))
        path.addQuadCurve(to: CGPoint(x: rect.minX, y: rect.maxY - r), control: CGPoint(x: rect.minX, y: rect.maxY))
        path.addLine(to: CGPoint(x: rect.minX, y: rect.minY + r))
        path.addQuadCurve(to: CGPoint(x: rect.minX + r, y: rect.minY), control: CGPoint(x: rect.minX, y: rect.minY))
        path.closeSubpath()
        return path
    }
}

/// A breakpoint marker: filled while it will stop, faded when it is off, all breakpoints are off
/// or the session closed, outlined while it has not bound to a script; a conditional breakpoint
/// carries a `?` at its leading end.
struct BreakpointMarker: View {
    let enabled: Bool
    let resolved: Bool
    var active = true
    var conditional = false
    var width: CGFloat = 20
    var height: CGFloat = 14

    var body: some View {
        let live = enabled && active
        ZStack(alignment: .leading) {
            if resolved {
                BreakpointMarkerShape()
                    .fill(DS.Code.breakpoint.opacity(live ? 1 : 0.35))
            } else {
                BreakpointMarkerShape()
                    .fill(DS.Code.breakpoint.opacity(live ? 0.14 : 0.06))
                BreakpointMarkerShape()
                    .stroke(DS.Code.breakpoint.opacity(live ? 0.9 : 0.4), lineWidth: 1.25)
            }
            if conditional {
                Text("?")
                    .font(.system(size: height * 0.7, weight: .heavy, design: .rounded))
                    .foregroundStyle(resolved ? AnyShapeStyle(DS.Color.onFill.opacity(live ? 1 : 0.8)) : AnyShapeStyle(DS.Code.breakpoint))
                    .padding(.leading, min(DS.Space.xs, width / 6))
            }
        }
        .frame(width: width, height: height)
        .accessibilityHidden(true)
    }
}

/// A value preview in its type's color on one line: the longest form that fits, ending at a
/// property boundary, so a narrow column never shows a cut property or a doubled `…`.
struct ValuePreviewText: View {
    let value: RemoteValue

    var body: some View {
        let forms = ValuePreview.candidates(value.valuePreview)
        ViewThatFits(in: .horizontal) {
            ForEach(Array(forms.dropLast().enumerated()), id: \.offset) { _, form in
                text(form).fixedSize()
            }
            text(forms.last ?? value.valuePreview)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .help(value.valuePreview)
    }

    private func text(_ form: String) -> some View {
        Text(form)
            .font(DebugFormat.mono)
            .italic(value.type == "function")
            .foregroundStyle(DebugFormat.valueColor(value))
            .lineLimit(1)
    }
}

/// A name and a value side by side: the name takes what it needs, up to what the value leaves
/// when it fits whole and at least 45% of the width; the value takes the rest.
struct NameValueLayout: Layout {
    var spacing: CGFloat = DS.Space.xs

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard subviews.count == 2 else { return .zero }
        let name = subviews[0].sizeThatFits(.unspecified)
        let value = subviews[1].sizeThatFits(.unspecified)
        let width = proposal.width ?? (name.width + spacing + value.width)
        return CGSize(width: width, height: max(name.height, value.height))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        guard subviews.count == 2 else { return }
        let ideal = subviews[0].sizeThatFits(.unspecified).width
        let value = subviews[1].sizeThatFits(.unspecified).width
        let nameWidth = min(ideal, max((bounds.width - spacing) * 0.45, bounds.width - spacing - value))
        subviews[0].place(at: CGPoint(x: bounds.minX, y: bounds.midY), anchor: .leading,
                          proposal: ProposedViewSize(width: nameWidth, height: bounds.height))
        let valueX = bounds.minX + nameWidth + spacing
        subviews[1].place(at: CGPoint(x: valueX, y: bounds.midY), anchor: .leading,
                          proposal: ProposedViewSize(width: max(0, bounds.maxX - valueX), height: bounds.height))
    }
}

/// A small spinner with a caption, for panes that are reading.
struct DebugLoadingLine: View {
    let text: String

    var body: some View {
        HStack(spacing: DS.Space.s) {
            ProgressView().controlSize(.small)
            Text(text)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A short centered message inside a pane: a symbol, a title and an explanation.
struct DebugPanePlaceholder: View {
    let symbol: String
    let title: String
    var message: String?

    var body: some View {
        VStack(spacing: DS.Space.s) {
            Image(systemName: symbol)
                .font(.system(size: 22, weight: .regular))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text(title)
                .font(DS.Font.body.weight(.semibold))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            if let message {
                Text(message)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 280)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(DS.Space.l)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .combine)
    }
}

/// A one-line hint at the top of a pane that has nothing to show yet.
struct DebugPaneHint: View {
    let text: String

    var body: some View {
        Text(text)
            .font(DS.Font.caption)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.s)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}
