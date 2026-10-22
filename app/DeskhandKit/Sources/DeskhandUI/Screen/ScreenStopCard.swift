import DeskhandClient
import DeskhandMedia
import SwiftUI
import struct DeskhandClient.Host

/// What the screen says when frames stop or the stream cannot open: a symbol, a title, the
/// explanation with the fix, a command to copy when the fix is one, the actions that apply, and
/// the reason or error code.
struct ScreenStopNotice: Equatable {
    enum Action: Equatable {
        /// Attaches again; the title says what that means here.
        case retry(String)
        case diagnose
        case showDisplay(index: Int, title: String)
    }

    let symbol: String
    /// The symbol's color: orange for a stop the PC causes, red for a failure.
    var symbolTint: Color = DS.Color.warning
    let title: String
    let message: String
    var command: String?
    var actions: [Action] = []
    /// `사유 코드` for a pause reason, `오류 코드` for an attach error.
    let codeLabel: String
    let code: String

    /// The notice for a `0x04` pause reason on `display` of `host`.
    static func paused(_ reason: PauseReason, host: Host, display: Int) -> ScreenStopNotice {
        let reasonLabel = String.ui("screen.stop.reasonCode", "사유 코드")
        let code = reason.rawValue
        let reconnect = Action.retry(.ui("screen.stop.reconnect", "다시 연결"))
        switch reason {
        case .session0:
            return ScreenStopNotice(
                symbol: "person.crop.circle.badge.exclamationmark",
                title: .ui("screen.stop.session0.title", "로그인한 Windows 사용자가 없습니다"),
                message: .ui("screen.stop.session0.message", "에이전트가 서비스 세션(세션 0)에서 실행 중이라 보여 줄 화면이 없습니다. PC에 Windows 사용자로 로그인하면 화면이 나타납니다. 계속되면 진단을 실행하세요."),
                actions: [.diagnose], codeLabel: reasonLabel, code: code)
        case .rdpOutputSuppressed:
            return ScreenStopNotice(
                symbol: "rectangle.on.rectangle.slash",
                title: .ui("screen.stop.rdp.title", "원격 데스크톱 창이 최소화되어 화면이 멈췄습니다"),
                message: .ui("screen.stop.rdp.message", "이 PC는 원격 데스크톱(RDP) 세션이라 RDP 창이 최소화되면 Windows가 화면을 그리지 않습니다. RDP 창을 다시 열거나, PC의 관리자 명령 프롬프트에서 아래 명령으로 세션을 콘솔로 옮기세요. RDP 연결은 끊깁니다."),
                command: consoleCommand(host), codeLabel: reasonLabel, code: code)
        case .secureDesktop:
            return ScreenStopNotice(
                symbol: "lock.shield",
                title: .ui("screen.stop.secure.title", "PC에 보안 화면(UAC/잠금)이 떠 있습니다"),
                message: .ui("screen.stop.secure.message", "사용자 계정 컨트롤 창이나 Ctrl+Alt+Del 화면은 원격으로 보거나 조작할 수 없습니다. PC 앞에서 승인하거나 닫으면 화면이 바로 이어집니다."),
                codeLabel: reasonLabel, code: code)
        case .sessionLocked:
            return ScreenStopNotice(
                symbol: "lock.display",
                title: .ui("screen.stop.locked.title", "Windows가 잠겨 있습니다"),
                message: .ui("screen.stop.locked.message", "다른 원격 접속 도구나 PC에서 Windows 잠금을 해제하세요. Deskhand에서는 로그인 화면을 조작할 수 없습니다."),
                codeLabel: reasonLabel, code: code)
        case .sessionDisconnected:
            return ScreenStopNotice(
                symbol: "rectangle.badge.xmark",
                title: .ui("screen.stop.disconnected.title", "Windows 세션이 화면에 연결되어 있지 않습니다"),
                message: .ui("screen.stop.disconnected.message", "사용자 세션은 남아 있지만 원격 데스크톱 연결이 끊겨 그릴 화면이 없습니다. PC 앞에서 로그인하거나, PC의 관리자 명령 프롬프트에서 아래 명령으로 세션을 콘솔로 옮기면 화면이 나타납니다."),
                command: consoleCommand(host), codeLabel: reasonLabel, code: code)
        case .accessLost:
            return ScreenStopNotice(
                symbol: "arrow.triangle.2.circlepath",
                title: .ui("screen.stop.accessLost.title", "화면을 다시 가져오는 중…"),
                message: .ui("screen.stop.accessLost.message", "Windows가 화면 접근을 잠시 잃었습니다. 해상도 변경이나 전체 화면 전환 뒤에 생기며, 자동으로 다시 시도합니다."),
                codeLabel: reasonLabel, code: code)
        case .noOutput:
            let name = DisplayInfo.title(index: display)
            return ScreenStopNotice(
                symbol: "display.trianglebadge.exclamationmark",
                title: .ui("screen.stop.noOutput.title", "이 디스플레이에 출력되는 화면이 없습니다"),
                message: .ui("screen.stop.noOutput.message", "모니터가 꺼져 있거나 연결되지 않아 Windows가 \(name)에 화면을 그리지 않습니다. 모니터를 켜거나 HDMI 더미 플러그를 연결한 뒤 다시 시도하세요."),
                actions: otherDisplays(host, display) + [.retry(.ui("screen.stop.retry", "다시 시도"))],
                codeLabel: reasonLabel, code: code)
        case .noEncoder:
            return ScreenStopNotice(
                symbol: "cpu",
                title: .ui("screen.stop.noEncoder.title", "PC에서 H.264 인코더를 찾지 못했습니다"),
                message: .ui("screen.stop.noEncoder.message", "그래픽 드라이버를 최신 버전으로 업데이트한 뒤 다시 시도하세요. 가상 머신이나 원격 데스크톱 전용 그래픽 어댑터에는 하드웨어 인코더가 없을 수 있습니다."),
                actions: [.retry(.ui("screen.stop.retry", "다시 시도"))], codeLabel: reasonLabel, code: code)
        case .noViewer:
            return ScreenStopNotice(
                symbol: "pause.circle",
                title: .ui("screen.stop.noViewer.title", "PC가 화면 전송을 멈췄습니다"),
                message: .ui("screen.stop.noViewer.message", "보는 사람이 없다고 판단해 캡처를 멈췄습니다. 다시 연결하면 이어집니다."),
                actions: [reconnect], codeLabel: reasonLabel, code: code)
        case .other:
            return ScreenStopNotice(
                symbol: "pause.circle",
                title: .ui("screen.stop.other.title", "화면 전송이 멈췄습니다"),
                message: .ui("screen.stop.other.message", "에이전트가 화면 전송을 멈췄습니다. 다시 연결해도 계속되면 진단을 실행하세요."),
                actions: [reconnect, .diagnose], codeLabel: reasonLabel, code: code)
        }
    }

    /// The notice for an attach refused with `DISPLAY_UNAVAILABLE`.
    static func displayUnavailable(_ error: OperationError, host: Host, display: Int) -> ScreenStopNotice {
        let name = DisplayInfo.title(index: display)
        return ScreenStopNotice(
            symbol: "display.trianglebadge.exclamationmark",
            title: .ui("screen.stop.display.title", "디스플레이를 찾을 수 없습니다"),
            message: .ui("screen.stop.display.message", "\(name) 연결이 끊겼거나 꺼져 있어 화면을 가져올 수 없습니다. 모니터 연결을 확인한 뒤 다시 시도하거나 다른 디스플레이를 고르세요."),
            actions: otherDisplays(host, display) + [.retry(.ui("screen.stop.retry", "다시 시도"))],
            codeLabel: .ui("error.codeLabel", "오류 코드"), code: error.code ?? "DISPLAY_UNAVAILABLE")
    }

    /// The notice for an attach that failed with `error`: the title names what failed, the body
    /// is the daemon's message (the cause) and the code's remedy as sentences, and retry. A code
    /// without a remedy says to try again and run the diagnosis, and offers 진단.
    static func failed(_ error: OperationError) -> ScreenStopNotice {
        let retry = Action.retry(.ui("screen.stop.retry", "다시 시도"))
        let fix = error.fix ?? .ui("screen.failed.fix", "다시 시도하고, 계속되면 진단을 실행하세요.")
        return ScreenStopNotice(
            symbol: "exclamationmark.octagon", symbolTint: DS.Color.danger,
            title: .ui("screen.failed.title", "화면 스트림을 열지 못했습니다"),
            message: String.sentences(error.message, fix),
            actions: error.fix == nil ? [retry, .diagnose] : [retry],
            codeLabel: .ui("error.codeLabel", "오류 코드"), code: error.code ?? "INTERNAL")
    }

    /// True when the PC's agent cannot stream its screen at all, so trying again cannot help.
    static func isUnsupported(_ error: OperationError) -> Bool {
        error.code == "CAPABILITY_UNSUPPORTED"
    }

    private static func otherDisplays(_ host: Host, _ display: Int) -> [Action] {
        host.displays.filter { $0.index != display }.map { .showDisplay(index: $0.index, title: $0.title) }
    }

    /// `tscon` moving the host's session to the console.
    private static func consoleCommand(_ host: Host) -> String {
        "tscon \(host.session?.sessionID ?? 1) /dest:console"
    }
}

/// A `ScreenStopNotice` as a card over the dimmed screen.
struct ScreenStopCard: View {
    let notice: ScreenStopNotice
    let perform: (ScreenStopNotice.Action) -> Void
    @State private var copied = false

    private static let symbolWidth: CGFloat = 28

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: notice.symbol)
                    .font(.title2.weight(.medium))
                    .foregroundStyle(notice.symbolTint)
                    .frame(width: Self.symbolWidth)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(notice.title)
                        .font(DS.Font.headline)
                    Text(notice.message)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .accessibilityElement(children: .combine)
            }
            Group {
                if let command = notice.command {
                    commandBox(command)
                }
                ErrorCodeLine(label: notice.codeLabel, code: notice.code)
                if !notice.actions.isEmpty {
                    FlowLayout(spacing: DS.Space.s) {
                        ForEach(Array(notice.actions.enumerated()), id: \.offset) { index, action in
                            actionButton(action, primary: index == 0)
                        }
                    }
                }
            }
            .padding(.leading, Self.symbolWidth + DS.Space.m)
        }
        .padding(DS.Space.l)
        .frame(maxWidth: 480, alignment: .leading)
        .background(DS.Color.surface, in: RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous).strokeBorder(DS.Color.overlayHairline))
        .shadow(color: DS.Color.overlayShadow, radius: 20, y: 8)
        .accessibilityElement(children: .contain)
    }

    private func commandBox(_ command: String) -> some View {
        HStack(spacing: DS.Space.s) {
            Text(command)
                .font(DS.Font.mono)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button {
                Pasteboard.copy(command)
                copied = true
            } label: {
                Label(copied ? String.ui("action.copied", "복사됨") : String.ui("action.copy", "복사"),
                      systemImage: copied ? "checkmark" : "doc.on.doc")
            }
            .buttonStyle(.borderedProminent)
            .tint(DS.Color.neutralFill)
            .controlSize(.small)
            .help(String.ui("screen.paused.rdp.copyHelp", "명령을 복사합니다"))
        }
        .padding(DS.Space.s)
        .background(DS.Color.overlayWash, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
    }

    @ViewBuilder
    private func actionButton(_ action: ScreenStopNotice.Action, primary: Bool) -> some View {
        let button = Button {
            perform(action)
        } label: {
            switch action {
            case .retry(let title):
                Label(title, systemImage: "arrow.clockwise")
            case .diagnose:
                Label(String.ui("action.doctor", "진단 실행"), systemImage: "stethoscope")
            case .showDisplay(_, let title):
                Label(String.ui("screen.paused.display.show", "\(title) 보기"), systemImage: "display")
            }
        }
        if primary {
            button.buttonStyle(.borderedProminent)
                .tint(DS.Status.selectionFill)
        } else {
            button.buttonStyle(.bordered)
        }
    }
}
