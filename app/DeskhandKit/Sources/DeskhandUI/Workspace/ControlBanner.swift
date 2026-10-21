import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// What the band above the workspace content says about a computer. Connection problems come
/// first, then a locked Windows session, then the AI pause, then who holds control.
public enum ControlBannerKind: Equatable, Sendable {
    case blocked
    case offline
    case connecting
    case reconnecting
    case sessionLocked
    case aiPaused
    case ai(client: String?)
    case human
    /// Connected and nobody holds control; no band is shown.
    case observing

    public init(host: Host, control: ControlState?) {
        switch host.connection {
        case .blocked: self = .blocked
        case .offline: self = .offline
        case .connecting: self = .connecting
        case .reconnecting: self = .reconnecting
        case .ready, .degraded:
            if host.isSessionLocked {
                self = .sessionLocked
            } else if control?.aiPaused == true {
                self = .aiPaused
            } else {
                switch ControlOwner(rawValue: control?.owner ?? host.owner) ?? .none {
                case .ai: self = .ai(client: control?.ownerClient ?? host.ownerClient)
                case .human: self = .human
                case .none: self = .observing
                }
            }
        }
    }

    /// Taking control applies while observing or while an AI client holds control.
    public var offersTakeOver: Bool {
        switch self {
        case .observing, .ai: true
        default: false
        }
    }

    /// Giving control back to the AI applies while the human holds control or the AI is paused.
    public var offersGiveBack: Bool {
        self == .human || self == .aiPaused
    }

    /// The band title, also announced to VoiceOver when the kind changes.
    public var title: String {
        switch self {
        case .blocked: .ui("notice.blocked.title", "인증서 불일치")
        case .offline: HostOffline.title
        case .connecting: .ui("notice.connecting.title", "연결하는 중…")
        case .reconnecting: .ui("notice.reconnecting.title", "다시 연결하는 중…")
        case .sessionLocked: .ui("banner.locked.title", "Windows가 잠겨 있습니다")
        case .aiPaused: .ui("banner.aiPaused.title", "AI 일시정지됨")
        case .ai(let client):
            client.map { .ui("banner.ai.titleWithClient", "AI가 조작 중 — \($0)") } ?? .ui("banner.ai.title", "AI가 조작 중")
        case .human: .ui("control.human", "내가 조작 중")
        case .observing: .ui("control.none", "관찰 중")
        }
    }
}

/// Band at the top of the workspace content for the computer's `ControlBannerKind`, with the
/// actions that state calls for, followed by the last failed control action.
struct ControlBanner: View {
    let store: AppStore
    let host: Host

    private var kind: ControlBannerKind {
        ControlBannerKind(host: host, control: store.control(for: host.hostID))
    }

    private var actions: ControlActions { store.controlActions }

    var body: some View {
        VStack(spacing: 0) {
            band
            ForEach(actions.failures(for: host.hostID)) { failure in
                ControlFailureBar(failure: failure,
                                  retry: { actions.retry(failure.action, on: host.hostID) },
                                  dismiss: { actions.dismissFailure(failure.action, for: host.hostID) })
            }
        }
    }

    @ViewBuilder
    private var band: some View {
        switch kind {
        case .ai:
            BannerBand(style: .solid(DS.Color.aiFill), symbol: ControlOwner.ai.symbol, title: kind.title,
                       message: String.ui("banner.ai.message", "언제든 제어권을 가져올 수 있고, 툴바의 긴급 정지(⇧⌘.)로 AI 입력을 바로 멈출 수 있습니다.")) {
                Button { actions.takeOver(host.hostID) } label: {
                    ControlActionLabel(action: .takeOver, running: actions.isRunning(.takeOver, on: host.hostID))
                }
                .buttonStyle(BandButtonStyle(text: DS.Color.aiFill))
                .disabled(actions.isRunning(.takeOver, on: host.hostID))
            }
        case .human:
            BannerBand(style: .tinted(DS.Color.human), symbol: ControlOwner.human.symbol, title: kind.title,
                       message: String.ui("banner.human.message", "AI의 입력은 거절됩니다. 작업을 마치면 AI에게 넘기세요.")) {
                giveBackButton
            }
        case .aiPaused:
            BannerBand(style: .tinted(DS.Color.warning), symbol: "pause.circle.fill", title: kind.title,
                       message: String.ui("banner.aiPaused.message", "AI에게 넘기기 전까지 AI는 이 PC를 조작할 수 없습니다.")) {
                giveBackButton
            }
        case .sessionLocked:
            BannerBand(style: .tinted(DS.Color.warning), symbol: "lock.fill", title: kind.title,
                       message: String.ui("banner.locked.message", "원격 PC 앞에서 잠금을 해제하세요. 그동안은 사람도 AI도 화면과 입력을 쓸 수 없습니다.")) {
                EmptyView()
            }
        case .connecting, .reconnecting:
            BannerBand(style: .tinted(host.connection.tint), symbol: nil, title: kind.title,
                       message: kind == .connecting
                           ? String.ui("banner.connecting.message", "보안 연결로 에이전트에 접속하고 있습니다.")
                           : String.ui("banner.reconnecting.message", "연결되면 바로 이어서 쓸 수 있습니다. 결과를 모르는 작업은 다시 실행하지 않습니다.")) {
                Button(String.ui("action.disconnect.ask", "연결 끊기…")) { store.requestDisconnect(host) }
                    .buttonStyle(.bordered)
            }
        case .offline:
            BannerBand(style: .tinted(DS.Color.neutral), symbol: "moon.zzz.fill",
                       title: host.lastSeenText.map { String.ui("notice.offline.lastSeen", "마지막 연결 \($0)") }
                           ?? String.ui("notice.offline.never", "연결한 기록이 없습니다"),
                       message: String.ui("notice.offline.message", "PC가 켜져 있으면 ‘연결’을 눌러 다시 시도하세요.")) {
                Button { store.connect(host.hostID) } label: {
                    Label(String.ui("action.connect", "연결"), systemImage: "bolt.horizontal")
                }
                .buttonStyle(.borderedProminent)
            }
        case .blocked:
            BannerBand(style: .tinted(DS.Color.danger), symbol: "exclamationmark.octagon.fill", title: kind.title,
                       message: String.ui("banner.blocked.message", "에이전트를 다시 설치했다면 다시 페어링하세요. 아니라면 다른 PC가 이 주소를 쓰는지 먼저 진단하세요.")) {
                Button(String.ui("action.doctor", "진단 실행")) { store.presentDoctor(for: host.hostID) }
                    .buttonStyle(.bordered)
                Button { store.repair(host.hostID) } label: {
                    Label(String.ui("action.repair", "다시 페어링"), systemImage: "arrow.triangle.2.circlepath")
                }
                .buttonStyle(.borderedProminent)
            }
        case .observing:
            EmptyView()
        }
    }

    /// AI에게 넘기기: a deep purple prominent button with white text, in every band that offers it.
    private var giveBackButton: some View {
        let running = actions.isRunning(.giveBack, on: host.hostID)
        return Button { actions.giveBack(host.hostID) } label: {
            ControlActionLabel(action: .giveBack, running: running)
        }
        .disabled(running)
        .buttonStyle(.borderedProminent)
        .tint(DS.Color.aiFill)
    }
}

/// Icon (or a spinner while running) and title of a control action button.
struct ControlActionLabel: View {
    let action: ControlActions.Action
    let running: Bool

    var body: some View {
        Group {
            if running {
                HStack(spacing: DS.Space.s) {
                    ProgressView()
                        .controlSize(.small)
                    Text(action.title)
                        .lineLimit(1)
                }
            } else {
                Label(action.title, systemImage: action.symbol)
                    .labelStyle(.titleAndIcon)
                    .lineLimit(1)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(running ? String.ui("a11y.controlRunning", "\(action.title), 진행 중") : action.title)
    }
}

extension ControlActions.Action {
    public var title: String {
        switch self {
        case .takeOver: .ui("control.takeOver", "제어권 가져오기")
        case .giveBack: .ui("control.giveBack", "AI에게 넘기기")
        case .stop: .ui("control.stop", "긴급 정지")
        }
    }

    public var symbol: String {
        switch self {
        case .takeOver: "hand.raised.fill"
        case .giveBack: "sparkles"
        case .stop: "stop.fill"
        }
    }

    /// Headline of the failure bar when the action fails.
    var failureTitle: String {
        switch self {
        case .takeOver: .ui("control.failed.takeOver", "제어권을 가져오지 못했습니다")
        case .giveBack: .ui("control.failed.giveBack", "AI에게 넘기지 못했습니다")
        case .stop: .ui("control.failed.stop", "긴급 정지하지 못했습니다")
        }
    }
}

/// Full-width band: a symbol (or a spinner when `symbol` is nil), a title and a message, with
/// actions at the trailing edge. `solid` fills the band with the color and draws its content
/// light; `tinted` uses a pale wash of the color.
struct BannerBand<Actions: View>: View {
    enum Style {
        case solid(Color)
        case tinted(Color)
    }

    let style: Style
    let symbol: String?
    let title: String
    let message: String
    @ViewBuilder let actions: Actions

    /// Width the title and message keep beside the actions; below it the actions move under the text.
    private static var textIdealWidth: CGFloat { 280 }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: DS.Space.m) {
                icon
                text
                    .frame(minWidth: 0, idealWidth: Self.textIdealWidth, maxWidth: .infinity, alignment: .leading)
                actionRow
            }
            HStack(alignment: .top, spacing: DS.Space.m) {
                icon
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    text
                    actionRow
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .environment(\.colorScheme, isSolid ? .dark : colorScheme)
        .background(background)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(separator)
                .frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }

    @Environment(\.colorScheme) private var colorScheme

    private var text: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(title)
                .font(DS.Font.headline)
                .foregroundStyle(.primary)
                .lineLimit(2)
            Text(message)
                .font(DS.Font.caption)
                .foregroundStyle(isSolid ? DS.Color.onFill : DS.Status.secondaryOnTint)
                .lineLimit(2)
                .help(message)
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }

    private var actionRow: some View {
        HStack(spacing: DS.Space.s) {
            actions
        }
        .controlSize(.regular)
        .fixedSize()
    }

    private var isSolid: Bool {
        if case .solid = style { return true }
        return false
    }

    private var tint: Color {
        switch style {
        case .solid(let color), .tinted(let color): color
        }
    }

    private var icon: some View {
        Circle()
            .fill(isSolid ? AnyShapeStyle(.white.opacity(0.18)) : AnyShapeStyle(tint.opacity(0.16)))
            .frame(width: 28, height: 28)
            .overlay {
                if let symbol {
                    Image(systemName: symbol)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(isSolid ? AnyShapeStyle(.white) : AnyShapeStyle(tint))
                } else {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .accessibilityHidden(true)
    }

    private var background: some ShapeStyle {
        switch style {
        case .solid(let color): AnyShapeStyle(color)
        case .tinted(let color): AnyShapeStyle(color.opacity(DS.Status.bandOpacity))
        }
    }

    private var separator: Color {
        switch style {
        case .solid: .black.opacity(0.12)
        case .tinted(let color): color.opacity(0.22)
        }
    }
}

/// The button on a solid band: a white capsule with `text`-colored text. `text` is a fill color
/// that white reaches 4.5:1 against, such as `DS.Color.aiFill`, so the text reads on white.
struct BandButtonStyle: ButtonStyle {
    let text: Color
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(DS.Font.body.weight(.semibold))
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.s)
            .foregroundStyle(text)
            .background(DS.Color.onFill, in: Capsule())
            .contentShape(Capsule())
            .opacity(configuration.isPressed ? 0.78 : (isEnabled ? 1 : 0.7))
    }
}

/// Inline failure of one control action: what failed, the daemon's message and fix, the error
/// code, an accent retry button and a dismiss button.
struct ControlFailureBar: View {
    let failure: ControlActions.Failure
    let retry: () -> Void
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            Image(systemName: "exclamationmark.octagon.fill")
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(DS.Color.danger)
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 0) {
                Text(failure.action.failureTitle)
                    .font(DS.Font.headline)
                Text(failure.error.sentences)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .help(failure.error.sentences)
                if let code = failure.error.code {
                    ErrorCodeLine(code: code)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            Button(action: retry) {
                Label(String.ui("action.retry", "다시 시도"), systemImage: "arrow.clockwise")
            }
            .buttonStyle(.borderedProminent)
            .tint(DS.Color.accent)
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 11, weight: .bold))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .help(String.ui("action.dismiss", "닫기"))
            .accessibilityLabel(String.ui("action.dismiss", "닫기"))
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .background(DS.Color.danger.opacity(0.08))
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.danger.opacity(0.22))
                .frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }
}
