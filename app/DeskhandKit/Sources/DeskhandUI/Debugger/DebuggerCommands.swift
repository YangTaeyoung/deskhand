import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The selected computer's debugger tab as the menu commands see it.
@MainActor
struct DebuggerCommandContext {
    let store: AppStore

    var host: Host? { store.selectedHost }
    var model: DebuggerModel? { host.map { store.debugger(for: $0) } }
    /// True while the window shows the debugger tab of the selected computer.
    var isShown: Bool { host != nil && store.layout.tab == .debugger }
    var session: DebugSessionModel? { isShown ? model?.selected : nil }
    var canStart: Bool {
        guard let host, model?.starting == nil else { return false }
        return HostFeatureAvailability(host: host, capability: "tunnel") == .ready
    }
    var canControl: Bool {
        guard let host, let session, session.command == nil else { return false }
        return session.canControl && HostFeatureAvailability(host: host, capability: "tunnel") == .ready
    }
}

/// Function keys as menu key equivalents.
enum FunctionKey {
    static func key(_ code: Int) -> KeyEquivalent {
        KeyEquivalent(Character(UnicodeScalar(UInt32(code)) ?? " "))
    }

    static let f8 = key(NSF8FunctionKey)
    static let f10 = key(NSF10FunctionKey)
    static let f11 = key(NSF11FunctionKey)
}

/// The 디버그 menu: launch and attach, 일시정지·재개 (F8), 건너뛰기 (F10), 들어가기 (F11), 나오기 (⇧F11),
/// adding and switching off breakpoints (⌘Y), the exception setting, clearing the console (⌘L) and
/// closing the session. Shortcuts apply only while the debugger tab is shown.
struct DebuggerMenuCommands: View {
    let store: AppStore

    var body: some View {
        let context = DebuggerCommandContext(store: store)
        let session = context.session
        let paused = session?.pauseState != nil
        Button(String.ui("debugger.launch.menu", "앱 실행…")) { show(.launch) }
            .disabled(!context.canStart)
        Button(String.ui("debugger.attach.menu", "실행 중인 앱에 연결…")) { show(.attach) }
            .disabled(!context.canStart)
        Divider()
        Button(paused ? String.ui("debugger.resume", "재개") : String.ui("debugger.pause", "일시정지")) { session?.togglePause() }
            .keyboardShortcut(context.isShown ? KeyboardShortcut(FunctionKey.f8, modifiers: []) : nil)
            .disabled(!context.canControl)
        Button(String.ui("debugger.stepOver", "건너뛰기")) { session?.step(.over) }
            .keyboardShortcut(context.isShown ? KeyboardShortcut(FunctionKey.f10, modifiers: []) : nil)
            .disabled(!context.canControl || !paused)
        Button(String.ui("debugger.stepInto", "들어가기")) { session?.step(.into) }
            .keyboardShortcut(context.isShown ? KeyboardShortcut(FunctionKey.f11, modifiers: []) : nil)
            .disabled(!context.canControl || !paused)
        Button(String.ui("debugger.stepOut", "나오기")) { session?.step(.out) }
            .keyboardShortcut(context.isShown ? KeyboardShortcut(FunctionKey.f11, modifiers: [.shift]) : nil)
            .disabled(!context.canControl || !paused)
        Divider()
        Button(String.ui("debugger.breakpoint.add.menu", "중단점 추가…")) { show(.breakpoint) }
            .disabled(session == nil || session?.isClosed == true)
        Button(session?.info.breakpointsActive == false ? String.ui("debugger.breakpoints.activate", "중단점 켜기")
                                                        : String.ui("debugger.breakpoints.deactivate", "모든 중단점 끄기")) {
            if let session { session.setBreakpointsActive(!session.info.breakpointsActive) }
        }
        .keyboardShortcut(context.isShown ? KeyboardShortcut("y") : nil)
        .disabled(session == nil || session?.isClosed == true)
        Menu(String.ui("debugger.exceptions", "예외에서 멈춤")) {
            ForEach(ExceptionPauseMode.allCases, id: \.self) { mode in
                Toggle(DebugFormat.exceptionMode(mode), isOn: Binding(
                    get: { session?.info.exceptionMode == mode }, set: { _ in session?.setExceptionMode(mode) }))
            }
        }
        .disabled(session == nil || session?.isClosed == true)
        Divider()
        Button(String.ui("debugger.console.clear", "콘솔 지우기")) { session?.clearConsole() }
            .keyboardShortcut(context.isShown ? KeyboardShortcut("l") : nil)
            .disabled(session == nil)
        Button(session?.info.sessionKind == .attach ? String.ui("debugger.detach.menu", "연결 끊기…")
                                                    : String.ui("debugger.closeSession.menu", "세션 닫기…")) {
            if let session { context.model?.pendingClose = session.id }
        }
        .disabled(session == nil || session?.isClosed == true)
    }

    private func show(_ sheet: DebuggerModel.Sheet) {
        guard let host = store.selectedHost else { return }
        store.layout.tab = .debugger
        store.debugger(for: host).sheet = sheet
    }
}
