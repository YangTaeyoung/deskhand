import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The sidebar button; 일시정지·재개 (F8 · ⌘\), 건너뛰기 (F10 · ⌘'), 들어가기 (F11 · ⌘;),
/// 나오기 (⇧F11 · ⇧⌘;); the session's state; the script navigator (⇧⌘O), the exception setting
/// and closing or detaching the session.
struct DebuggerToolbar: View {
    let model: DebuggerModel
    let session: DebugSessionModel?
    let enabled: Bool
    let chrome: WorkspaceChrome
    let navigatorShown: Bool
    let toggleNavigator: () -> Void

    var body: some View {
        let canControl = enabled && (session?.canControl ?? false) && session?.command == nil
        let paused = session?.isSelectedPaused ?? false
        let pending = session?.pausePending ?? false
        let steppable = session?.pauseState != nil
        HStack(spacing: DS.Space.s) {
            ToolbarIconButton(symbol: "sidebar.left", title: navigatorShown ? String.ui("debugger.navigator.hide", "세션 목록 숨기기")
                                                                            : String.ui("debugger.navigator.show", "세션 목록 보기"),
                              selected: navigatorShown, action: toggleNavigator)
            HStack(spacing: DS.Space.xs) {
                ControlButton(symbol: paused ? "play.fill" : (pending ? "pause.circle" : "pause.fill"),
                              title: DebugFormat.pauseTitle(paused: paused, pending: pending),
                              shortcut: "F8 · ⌘\\", tint: paused ? DS.Color.success : (pending ? DS.Color.warning : nil), enabled: canControl) {
                    session?.togglePause()
                }
                ControlButton(symbol: "arrow.turn.up.right", title: String.ui("debugger.stepOver", "건너뛰기"), shortcut: "F10 · ⌘'",
                              enabled: canControl && steppable) { session?.step(.over) }
                ControlButton(symbol: "arrow.turn.right.down", title: String.ui("debugger.stepInto", "들어가기"), shortcut: "F11 · ⌘;",
                              enabled: canControl && steppable) { session?.step(.into) }
                ControlButton(symbol: "arrow.turn.left.up", title: String.ui("debugger.stepOut", "나오기"), shortcut: "⇧F11 · ⇧⌘;",
                              enabled: canControl && steppable) { session?.step(.out) }
            }
            .padding(DS.Space.xs)
            .background(DS.Color.neutral.opacity(0.1), in: RoundedRectangle(cornerRadius: DS.Radius.s + 2, style: .continuous))
            StatusText(model: model, session: session)
                .padding(.leading, DS.Space.xs)
            Spacer(minLength: DS.Space.s)
            if let session, session.isClosed, let request = session.relaunchRequest {
                Button {
                    Task { _ = await model.launch(request) }
                } label: {
                    Label(String.ui("debugger.relaunch", "다시 실행"), systemImage: "arrow.clockwise")
                }
                .disabled(!enabled || model.starting != nil)
                .help(String.ui("debugger.relaunch.help", "같은 실행 파일, 인수, 작업 폴더, 중단점으로 새 디버그 세션을 시작합니다"))
            }
            if let session {
                scriptButton(session)
            }
            if let session, !session.isClosed {
                ExceptionMenu(session: session, enabled: enabled)
                Button {
                    model.pendingClose = session.id
                } label: {
                    Label(session.info.sessionKind == .attach ? String.ui("debugger.detach", "연결 해제") : String.ui("debugger.closeSession", "세션 닫기"),
                          systemImage: "xmark.circle")
                }
                .disabled(session.command == .close)
                .help(session.info.sessionKind == .attach
                      ? String.ui("debugger.detach.help", "앱은 두고 디버거 연결만 해제합니다")
                      : String.ui("debugger.closeSession.help", "세션을 닫습니다. Deskhand가 실행한 앱도 종료할지 묻습니다"))
            }
        }
        .controlSize(.regular)
        .padding(.horizontal, DS.Space.m)
        .frame(height: WorkspaceMetrics.subToolbarHeight)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("debugger.toolbar", "디버거 제어"))
    }

    @ViewBuilder
    private func scriptButton(_ session: DebugSessionModel) -> some View {
        @Bindable var model = model
        let button = Button {
            model.showsScriptNavigator.toggle()
        } label: {
            Label(String.ui("debugger.scripts", "스크립트"), systemImage: "doc.text.magnifyingglass")
        }
        .disabled(session.isClosed && session.scripts.isEmpty)
        .help(String.ui("debugger.scripts.help", "로드된 스크립트를 찾아 엽니다 (⇧⌘O)"))
        .anchorPreference(key: ScriptNavigatorAnchor.self, value: .bounds) { $0 }
        switch chrome {
        case .native:
            button.popover(isPresented: $model.showsScriptNavigator, arrowEdge: .bottom) {
                ScriptNavigator(session: session) { model.showsScriptNavigator = false }
            }
        case .standIn:
            button
        }
    }
}

/// A square icon button of the toolbar, tinted while `selected`.
struct ToolbarIconButton: View {
    let symbol: String
    let title: String
    var selected = false
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(.body, weight: .regular))
                .foregroundStyle(selected ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                .frame(width: 28, height: 24)
                .background(hovering ? DS.Color.neutral.opacity(0.16) : .clear,
                            in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(title)
        .accessibilityLabel(title)
    }
}

/// An icon button of the execution controls with its name and shortcuts in the help tag.
struct ControlButton: View {
    let symbol: String
    let title: String
    let shortcut: String
    var tint: Color?
    let enabled: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(.body, weight: .semibold))
                .foregroundStyle(foreground)
                .frame(width: 32, height: 24)
                .background(hovering && enabled ? DS.Color.neutral.opacity(0.16) : .clear,
                            in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .onHover { hovering = $0 }
        .help(title + " (" + shortcut + ")")
        .accessibilityLabel(title)
        .accessibilityHint(shortcut)
    }

    private var foreground: AnyShapeStyle {
        guard enabled else { return AnyShapeStyle(.tertiary) }
        if let tint { return AnyShapeStyle(tint) }
        return AnyShapeStyle(.primary)
    }
}

/// The selected session's state in a line.
struct StatusText: View {
    let model: DebuggerModel
    let session: DebugSessionModel?

    var body: some View {
        HStack(spacing: DS.Space.s) {
            indicator
            Text(text)
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private var indicator: some View {
        if let session, session.command == nil, model.starting == nil, session.stateFailure != nil {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(DS.Color.warning)
        } else if model.starting != nil || session?.command != nil || session?.awaitingState == true {
            ProgressView().controlSize(.small)
        } else if let session {
            switch session.phase {
            case .paused(let reason):
                Image(systemName: CallStackView.isException(reason) ? "exclamationmark.octagon.fill" : "pause.circle.fill")
                    .foregroundStyle(CallStackView.isException(reason) ? DS.Color.danger : DS.Color.warning)
            case .closed:
                Image(systemName: "stop.circle")
                    .foregroundStyle(.secondary)
            default:
                if session.pausePending {
                    Image(systemName: "pause.circle")
                        .foregroundStyle(DS.Color.warning)
                } else {
                    runningDot
                }
            }
        }
    }

    private var runningDot: some View {
        Image(systemName: "circle.fill")
            .font(.system(size: 7))
            .foregroundStyle(DS.Color.success)
    }

    private var text: String {
        if let starting = model.starting {
            let name = starting.name
            return starting.kind == .launch ? .ui("debugger.status.launching", "\(name) 실행하는 중…")
                                            : .ui("debugger.status.attaching", "\(name)에 연결하는 중…")
        }
        guard let session else { return .ui("debugger.status.none", "세션 없음") }
        switch session.command {
        case .pause: return .ui("debugger.status.pausing", "일시정지하는 중…")
        case .resume: return .ui("debugger.status.resuming", "재개하는 중…")
        case .step: return .ui("debugger.status.stepping", "한 단계 실행하는 중…")
        case .close: return session.info.sessionKind == .attach ? .ui("debugger.status.detaching", "연결을 해제하는 중…")
                                                                : .ui("debugger.status.closing", "세션을 닫는 중…")
        case nil: break
        }
        let target = session.selectedTarget.map { DebugFormat.role($0) + " · " + DebugFormat.targetTitle($0) } ?? ""
        switch session.phase {
        case .paused(let reason):
            let why = DebugFormat.reason(reason)
            if let frame = session.pauseState?.callFrames.first {
                let location = DebugFormat.location(url: frame.url, line: frame.line)
                if let by = session.pauseState?.requestedBy, by.actor == "ai" {
                    let actor = DebugFormat.actor(by)
                    return .ui("debugger.status.pausedAtBy", "일시정지됨 · \(why) · \(location) · \(actor)")
                }
                return .ui("debugger.status.pausedAt", "일시정지됨 · \(why) · \(location)")
            }
            return .ui("debugger.status.paused", "일시정지됨 · \(why)")
        case .closed(let reason):
            return DebugFormat.closed(reason, exitCode: session.info.exitCode, kind: session.info.sessionKind)
        default:
            if session.stateFailure != nil { return .ui("debugger.status.pauseUnread", "일시정지됨 · 멈춘 위치를 불러오지 못했습니다") }
            if session.awaitingState { return .ui("debugger.status.readingPause", "일시정지됨 · 멈춘 위치를 읽는 중") }
            if session.pausePending { return .ui("debugger.status.pausePending", "일시정지 요청됨 · 다음 코드에서 멈춥니다") }
            if !session.pausedTargets.isEmpty {
                let other = session.pausedTargets.map(DebugFormat.role).joined(separator: ", ")
                return .ui("debugger.status.otherPaused", "\(target) 실행 중 · \(other) 일시정지됨")
            }
            return .ui("debugger.status.running", "실행 중 · \(target)")
        }
    }
}

/// 예외에서 멈춤: 멈추지 않음, 잡히지 않은 것만, 모두.
struct ExceptionMenu: View {
    let session: DebugSessionModel
    let enabled: Bool

    var body: some View {
        Menu {
            Picker(String.ui("debugger.exceptions", "예외에서 멈춤"), selection: Binding(
                get: { session.info.exceptionMode }, set: { session.setExceptionMode($0) })) {
                ForEach(ExceptionPauseMode.allCases, id: \.self) { mode in
                    Text(DebugFormat.exceptionMode(mode)).tag(mode)
                }
            }
            .pickerStyle(.inline)
        } label: {
            Label(String.ui("debugger.exceptions.label", "예외: \(DebugFormat.exceptionMode(session.info.exceptionMode))"),
                  systemImage: "exclamationmark.octagon")
        }
        .fixedSize()
        .disabled(!enabled)
        .help(String.ui("debugger.exceptions.help", "예외가 던져질 때 멈출지 고릅니다"))
    }
}

// MARK: Idle
