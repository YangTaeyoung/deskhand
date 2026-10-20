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

    static func reason(_ reason: DebugPauseReason) -> String {
        switch reason {
        case .breakpoint: .ui("debugger.reason.breakpoint", "중단점")
        case .exception: .ui("debugger.reason.exception", "예외")
        case .promiseRejection: .ui("debugger.reason.promise", "처리되지 않은 Promise 거부")
        case .pauseRequest: .ui("debugger.reason.pause", "일시정지 요청")
        case .step: .ui("debugger.reason.step", "한 단계 실행")
        case .other: .ui("debugger.reason.other", "debugger 문 또는 기타")
        }
    }

    static func closed(_ reason: DebugCloseReason?, exitCode: Int?) -> String {
        switch reason {
        case .requested: return .ui("debugger.closed.requested", "세션을 닫았습니다")
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
        case .uncaught: .ui("debugger.exceptions.uncaught", "잡히지 않은 예외")
        case .all: .ui("debugger.exceptions.all", "모든 예외")
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
}

/// A pane title bar: a title, an optional detail, and trailing controls.
struct DebugPaneHeader<Trailing: View>: View {
    static var height: CGFloat { 30 }

    let title: String
    var detail: String?
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(title)
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .accessibilityAddTraits(.isHeader)
            if let detail {
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.tertiary)
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
                    .frame(width: axis == .horizontal ? 9 : nil, height: axis == .vertical ? 9 : nil)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        let cursor: NSCursor = axis == .horizontal ? .resizeLeftRight : .resizeUpDown
                        if inside { cursor.push() } else { NSCursor.pop() }
                    }
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
    let target: DebugTarget
    /// Drawn on an accent selection.
    var inverted = false

    var body: some View {
        let main = target.targetRole == .main
        Text(DebugFormat.role(target))
            .font(.system(size: NSFont.smallSystemFontSize - 1, weight: .semibold))
            .foregroundStyle(foreground(main: main))
            .padding(.horizontal, 6)
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

/// A breakpoint marker: filled while it will stop, faded when it is off or all breakpoints are
/// off, outlined while it has not bound to a script.
struct BreakpointMarker: View {
    let enabled: Bool
    let resolved: Bool
    var active = true
    var width: CGFloat = 22
    var height: CGFloat = 14

    var body: some View {
        let live = enabled && active
        ZStack {
            if resolved {
                BreakpointMarkerShape()
                    .fill(DS.Code.breakpoint.opacity(live ? 1 : 0.35))
            } else {
                BreakpointMarkerShape()
                    .fill(DS.Code.breakpoint.opacity(live ? 0.14 : 0.06))
                BreakpointMarkerShape()
                    .stroke(DS.Code.breakpoint.opacity(live ? 0.9 : 0.4), lineWidth: 1.25)
            }
        }
        .frame(width: width, height: height)
        .accessibilityHidden(true)
    }
}

/// A value preview in its type's color, one line, monospaced.
struct ValuePreviewText: View {
    let value: RemoteValue

    var body: some View {
        Text(value.valuePreview)
            .font(DebugFormat.mono)
            .italic(value.type == "function")
            .foregroundStyle(DebugFormat.valueColor(value))
            .lineLimit(1)
            .truncationMode(.tail)
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
                .foregroundStyle(.tertiary)
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
