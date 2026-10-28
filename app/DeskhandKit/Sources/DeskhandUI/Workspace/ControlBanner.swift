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

    /// The AI band's second line; after a failed emergency stop it no longer promises one.
    public static func aiMessage(stopFailed: Bool) -> String {
        stopFailed
            ? .ui("banner.ai.stopFailed", "제어권을 직접 가져오세요.")
            : .ui("banner.ai.message", "언제든 제어권을 가져올 수 있고, 툴바의 긴급 정지(⇧⌘.)로 AI 입력을 바로 멈출 수 있습니다.")
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
    private var repairFailed: Bool { kind == .blocked && store.repairError(for: host.hostID) != nil }

    var body: some View {
        VStack(spacing: 0) {
            if !repairFailed { band }
            if let error = store.controlError(for: host.hostID) {
                ControlFailureBar(title: String.ui("control.failed.read", "제어권 상태를 읽지 못했습니다"), error: error,
                                  retry: { Task { await store.loadControl(for: host.hostID) } },
                                  dismiss: { store.dismissControlError(for: host.hostID) })
            }
            ForEach(actions.failures(for: host.hostID)) { failure in
                ControlFailureBar(title: failure.action.failureTitle, error: failure.error,
                                  retry: failure.action == .automaticRelease ? nil : { actions.retry(failure.action, on: host.hostID) },
                                  dismiss: { actions.dismissFailure(failure.action, for: host.hostID) })
            }
            if let error = store.repairError(for: host.hostID) {
                ControlFailureBar(title: String.ui("repair.failed", "다시 페어링하지 못했습니다"), error: error,
                                  retry: { store.requestRepair(host) },
                                  dismiss: { store.dismissRepairError(for: host.hostID) },
                                  diagnose: kind == .blocked ? { store.presentDoctor(for: host.hostID) } : nil)
            }
        }
    }

    @ViewBuilder
    private var band: some View {
        switch kind {
        case .ai:
            BannerBand(style: .solid(DS.Color.aiFill), symbol: ControlOwner.ai.symbol, title: kind.title,
                       message: ControlBannerKind.aiMessage(stopFailed: actions.failures(for: host.hostID).contains { $0.action == .stop })) {
                Button { actions.forceTakeOver(host.hostID) } label: {
                    ControlActionLabel(action: .takeOver, running: actions.isRunning(.takeOver, on: host.hostID))
                }
                .buttonStyle(BandButtonStyle(text: DS.Color.aiFill))
                .disabled(actions.isRunning(.takeOver, on: host.hostID))
            }
        case .human:
            BannerBand(style: .tinted(DS.Color.human), symbol: ControlOwner.human.symbol, title: kind.title,
                       message: String.ui("banner.human.message", "사용자가 조작하는 동안 AI는 기다립니다.")) {
                giveBackButton
            }
        case .aiPaused:
            BannerBand(style: .tinted(DS.Color.warning), symbol: "pause.circle.fill", title: kind.title,
                       message: String.ui("banner.aiPaused.message", "AI에게 넘기기 전까지 AI는 이 PC를 조작할 수 없습니다.")) {
                giveBackButton
            }
        case .sessionLocked:
            BannerBand(style: .tinted(DS.Color.warning), symbol: "lock.fill", title: kind.title,
                       message: String.ui("banner.locked.message", "Windows 계정으로 로그인해 원격 화면을 연결하세요.")) {
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
                .tint(DS.Status.selectionFill)
            }
        case .blocked:
            let repairing = store.isRepairing(host.hostID)
            BannerBand(style: .tinted(DS.Color.danger), symbol: "exclamationmark.octagon.fill", title: kind.title,
                       message: repairing
                           ? String.ui("banner.blocked.repairing", "SSH로 에이전트를 다시 설치하고 이 Mac과 다시 페어링하고 있습니다. 그동안 이 PC의 원격 작업이 끊깁니다.")
                           : String.ui("banner.blocked.message", "에이전트를 다시 설치했다면 다시 페어링하세요. 아니라면 다른 PC가 이 주소를 쓰는지 먼저 진단하세요.")) {
                Button(String.ui("action.doctor", "진단 실행")) { store.presentDoctor(for: host.hostID) }
                    .buttonStyle(BandSecondaryButtonStyle())
                    .disabled(repairing)
                if repairing {
                    HStack(spacing: DS.Space.s) {
                        ProgressView().controlSize(.small)
                        Text(String.ui("settings.connection.progress.repair", "다시 페어링하는 중…"))
                            .foregroundStyle(.primary)
                    }
                    .padding(.horizontal, DS.Space.m)
                    .padding(.vertical, DS.Space.xs)
                    .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous).strokeBorder(.separator))
                    .accessibilityElement(children: .combine)
                } else {
                    Button { store.requestRepair(host) } label: {
                        Label(String.ui("action.repair.ask", "다시 페어링…"), systemImage: "arrow.triangle.2.circlepath")
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(DS.Status.selectionFill)
                }
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
                HStack(spacing: DS.Space.s) {
                    Image(systemName: action.symbol)
                    Text(action.title)
                        .lineLimit(1)
                }
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
        case .giveBack: .ui("control.giveBack", "AI 바로 재개")
        case .automaticRelease: "자동 복귀"
        case .stop: .ui("control.stop", "긴급 정지")
        }
    }

    public var symbol: String {
        switch self {
        case .takeOver: "hand.raised.fill"
        case .giveBack, .automaticRelease: "sparkles"
        case .stop: "stop.fill"
        }
    }

    /// Headline of the failure bar when the action fails.
    var failureTitle: String {
        switch self {
        case .takeOver: .ui("control.failed.takeOver", "제어권을 가져오지 못했습니다")
        case .giveBack: .ui("control.failed.giveBack", "AI에게 넘기지 못했습니다")
        case .automaticRelease: "자동 복귀하지 못했습니다"
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
            HStack(spacing: DS.Space.s) {
                icon
                text
                    .frame(minWidth: 0, idealWidth: Self.textIdealWidth, maxWidth: .infinity, alignment: .leading)
                actionRow
            }
            HStack(alignment: .top, spacing: DS.Space.s) {
                icon
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    text
                    actionRow
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, 6)
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
        ViewThatFits(in: .horizontal) {
            HStack(spacing: DS.Space.s) {
                Text(title)
                    .font(DS.Font.body.weight(.semibold))
                    .foregroundStyle(isSolid ? DS.Color.onFill : SwiftUI.Color.primary)
                    .lineLimit(1)
                Text(message)
                    .font(DS.Font.caption)
                    .foregroundStyle(isSolid ? DS.Color.onFill.opacity(0.85) : DS.Status.secondaryOnTint)
                    .lineLimit(1)
                    .help(message)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(title)
                    .font(DS.Font.body.weight(.semibold))
                    .foregroundStyle(isSolid ? DS.Color.onFill : SwiftUI.Color.primary)
                    .lineLimit(1)
                Text(message)
                    .font(DS.Font.caption)
                    .foregroundStyle(isSolid ? DS.Color.onFill.opacity(0.85) : DS.Status.secondaryOnTint)
                    .lineLimit(1)
                    .help(message)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }

    private var actionRow: some View {
        HStack(spacing: DS.Space.s) {
            actions
        }
        .controlSize(.small)
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
        RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
            .fill(isSolid ? AnyShapeStyle(.white.opacity(0.18)) : AnyShapeStyle(tint.opacity(0.16)))
            .frame(width: 22, height: 22)
            .overlay {
                if let symbol {
                    Image(systemName: symbol)
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(isSolid ? AnyShapeStyle(.white) : AnyShapeStyle(tint))
                } else {
                    ProgressView()
                        .controlSize(.mini)
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
/// Bordered secondary button for tinted bands; the disabled state keeps the label in `secondaryOnTint`.
struct BandSecondaryButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(isEnabled ? AnyShapeStyle(.primary) : AnyShapeStyle(DS.Status.secondaryOnTint))
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.xs)
            .background(.quaternary.opacity(configuration.isPressed ? 1 : 0.6),
                        in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
    }
}

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
    let title: String
    let error: OperationError
    let retry: (() -> Void)?
    let dismiss: () -> Void
    var diagnose: (() -> Void)?

    var body: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            Image(systemName: "exclamationmark.octagon.fill")
                .font(.system(.title3, weight: .semibold))
                .foregroundStyle(DS.Color.danger)
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 0) {
                Text(title)
                    .font(DS.Font.headline)
                Text(error.sentences)
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.secondaryOnTint)
                    .lineLimit(3)
                    .help(error.sentences)
                if let code = error.code {
                    ErrorCodeLine(code: code)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            if let diagnose {
                Button(String.ui("action.doctor", "진단 실행"), action: diagnose)
                    .buttonStyle(.bordered)
            }
            if let retry {
                Button(action: retry) {
                    Label(String.ui("action.retry", "다시 시도"), systemImage: "arrow.clockwise")
                }
                .buttonStyle(.borderedProminent)
                .tint(DS.Status.selectionFill)
            }
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(.subheadline, weight: .bold))
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
