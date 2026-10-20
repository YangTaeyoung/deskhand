import AppKit
import DeskhandClient
import DeskhandMedia
import SwiftUI
import struct DeskhandClient.Host

/// Whether a computer can show its screen, from its connection, Windows session and `screen` capability.
enum ScreenAvailability: Equatable {
    case streamable
    case connecting
    case reconnecting
    case offline
    case blocked
    /// Windows shows the lock or sign-in screen.
    case locked
    /// The agent reports the `screen` capability as not ready.
    case unavailable(CapabilityState, reason: String?)

    init(host: Host) {
        switch host.connection {
        case .offline: self = .offline
        case .blocked: self = .blocked
        case .connecting: self = .connecting
        case .reconnecting: self = .reconnecting
        case .ready, .degraded:
            if host.isSessionLocked {
                self = .locked
            } else if let screen = host.caps.items["screen"], screen.capabilityState != .ready {
                self = .unavailable(screen.capabilityState, reason: screen.reason)
            } else {
                self = .streamable
            }
        }
    }
}

/// A fixed screen for snapshot scenes: a seeded session and a still frame drawn in place of the
/// video, with the cursor at `cursor` (a unit point in the video).
public struct ScreenStandIn {
    public var session: ScreenSession
    public var still: CGImage?
    public var cursor: CGPoint?

    public init(session: ScreenSession, still: CGImage?, cursor: CGPoint? = CGPoint(x: 0.46, y: 0.42)) {
        self.session = session
        self.still = still
        self.cursor = cursor
    }
}

/// Stand-in screens by host ID.
public struct ScreenStandIns {
    public var byHost: [String: ScreenStandIn]

    public init(_ byHost: [String: ScreenStandIn]) {
        self.byHost = byHost
    }
}

extension EnvironmentValues {
    /// Stand-in screens `ScreenView` draws in snapshot scenes instead of attaching streams.
    @Entry public var screenStandIns: ScreenStandIns? = nil
}

extension DisplayInfo {
    /// `디스플레이 1`.
    var title: String { Self.title(index: index) }

    static func title(index: Int) -> String {
        let number = index + 1
        return .ui("display.title", "디스플레이 \(number)")
    }

    /// `3840×2160 · 150%`.
    var detail: String { "\(width)×\(height) · \(Int((scale * 100).rounded()))%" }
}

/// The screen tab: a header with the display picker, input status and stream settings, and the
/// remote screen below it in every state — connecting, live, paused, reconnecting, failed and
/// unavailable. While an AI client holds control the video has a purple edge and a click asks
/// to take control.
public struct ScreenView: View {
    let store: AppStore
    let host: Host
    let chrome: WorkspaceChrome
    @State private var session: ScreenSession
    @Environment(\.screenStandIns) private var standIns
    @Environment(\.displayScale) private var displayScale

    public init(store: AppStore, host: Host, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.host = host
        self.chrome = chrome
        _session = State(initialValue: ScreenSession(backend: store.backend, preset: store.screenPreferences.preset))
    }

    private var preferences: ScreenPreferences { store.screenPreferences }
    private var standIn: ScreenStandIn? { standIns?.byHost[host.hostID] }
    private var shown: ScreenSession { standIn?.session ?? session }
    private var display: Int { preferences.display(for: host) }
    private var displayInfo: DisplayInfo? { host.displays.first { $0.index == display } }
    private var availability: ScreenAvailability { ScreenAvailability(host: host) }
    private var control: ControlState? { store.control(for: host.hostID) }
    private var owner: ControlOwner { ControlOwner(rawValue: control?.owner ?? host.owner) ?? .none }

    /// The remote display size: what the stream reports, else the display list, else 16:9.
    private var remoteSize: CGSize {
        let size = shown.nativeSize
        if size.width > 0, size.height > 0 { return size }
        if let displayInfo { return CGSize(width: displayInfo.width, height: displayInfo.height) }
        return CGSize(width: 1920, height: 1080)
    }

    private var displayTitle: String {
        displayInfo?.title ?? DisplayInfo.title(index: display)
    }

    /// `개발 PC · 디스플레이 1 · 3840×2160`.
    private var loadingDetail: String {
        [host.name, displayTitle, displayInfo.map { "\($0.width)×\($0.height)" }].compactMap { $0 }.joined(separator: " · ")
    }

    /// The video shows while the computer can stream, and dimmed under the reconnecting notice;
    /// the other unavailable states show their message on the empty stage.
    private var showsVideo: Bool {
        availability == .streamable || (availability == .reconnecting && shown.hasFrame)
    }

    private var isLive: Bool { availability == .streamable && shown.phase == .live }

    private struct RunKey: Equatable {
        let hostID: String
        let display: Int
        let streams: Bool
        let retries: Int
    }

    private var runKey: RunKey {
        RunKey(hostID: host.hostID, display: display,
               streams: chrome == .native && standIn == nil && availability == .streamable,
               retries: session.retries)
    }

    public var body: some View {
        VStack(spacing: 0) {
            ScreenHeader(preferences: preferences, host: host, display: display, owner: owner,
                         live: isLive, captured: shown.isCaptured)
                .layoutPriority(1)
            stage
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
        }
        .task(id: runKey) { await run(runKey) }
        .onChange(of: preferences.preset) { _, preset in session.setPreset(preset) }
    }

    private func run(_ key: RunKey) async {
        let hostID = host.hostID
        guard key.streams else {
            if chrome == .native { store.setStreamQuality(nil, for: hostID) }
            return
        }
        session.fallbackSize = displayInfo.map { CGSize(width: $0.width, height: $0.height) } ?? .zero
        session.gen = { [weak store] in store?.control(for: hostID)?.gen ?? 0 }
        session.onQuality = { [weak store] quality in store?.setStreamQuality(quality, for: hostID) }
        await session.run(hostID: hostID, display: key.display, preset: preferences.preset)
    }

    // MARK: Stage

    private var stage: some View {
        GeometryReader { proxy in
            let mapper = PointMapper(remote: remoteSize, view: proxy.size, backingScale: displayScale)
            ZStack(alignment: .topLeading) {
                DS.Color.screenBackdrop
                video(mapper: mapper)
                overlays(mapper: mapper)
            }
        }
        .environment(\.colorScheme, .dark)
        .clipped()
    }

    @ViewBuilder
    private func video(mapper: PointMapper) -> some View {
        if let standIn {
            if showsVideo, shown.hasFrame, let still = standIn.still {
                StillScreen(image: still, mapper: mapper, cursor: standIn.cursor)
            }
        } else if chrome == .native, showsVideo {
            VideoLayerView(
                session: session, owner: owner, inputEnabled: isLive, display: display,
                keyMap: preferences.keyMap, keyboardMode: preferences.keyboardMode, remoteSize: remoteSize,
                accessibilityLabel: String.ui("screen.a11y.label", "\(host.name)의 원격 화면, \(displayTitle)"),
                accessibilityHelp: owner == .ai
                    ? String.ui("screen.a11y.help.ai", "클릭하면 AI에게서 제어권을 가져올지 묻습니다")
                    : String.ui("screen.a11y.help", "클릭하면 키보드와 마우스를 이 PC로 보냅니다. ⌃⌥를 눌렀다 떼면 입력을 돌려받습니다"),
                onTakeoverRequest: { store.controlActions.takeOver(host.hostID) })
        }
    }

    @ViewBuilder
    private func overlays(mapper: PointMapper) -> some View {
        let rect = mapper.videoRect
        switch availability {
        case .streamable:
            switch shown.phase {
            case .live where shown.isReadyForDisplay || shown.metrics != nil:
                liveOverlays(rect: rect)
            case .live:
                ScreenSkeleton(rect: rect, title: String.ui("screen.connecting", "화면을 불러오는 중…"), detail: loadingDetail)
            case .idle, .connecting, .reconnecting:
                loading(rect: rect, reconnecting: shown.phase == .reconnecting)
            case .paused(let reason):
                Scrim(rect: rect)
                ScreenPausedCard(reason: reason, host: host, display: display) { index in
                    preferences.setDisplay(index, for: host.hostID)
                } retry: {
                    session.retry()
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed(let error):
                if shown.hasFrame { Scrim(rect: rect) }
                StageError(error: error) { session.retry() }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        case .connecting:
            ScreenSkeleton(rect: rect, title: String.ui("screen.connecting.host", "PC에 연결하는 중…"), detail: host.name)
        case .reconnecting:
            if shown.hasFrame {
                Scrim(rect: rect)
                StageCapsule(title: String.ui("screen.reconnecting", "다시 연결하는 중…"), spinner: true)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScreenSkeleton(rect: rect, title: String.ui("screen.reconnecting", "다시 연결하는 중…"), detail: host.name)
            }
        case .offline:
            StageMessage(symbol: "moon.zzz",
                         title: String.ui("screen.offline.title", "PC가 오프라인입니다"),
                         message: String.ui("screen.offline.message", "다시 연결되면 화면이 여기에 나타납니다."))
        case .blocked:
            StageMessage(symbol: "exclamationmark.shield",
                         title: String.ui("screen.blocked.title", "화면을 열 수 없습니다"),
                         message: String.ui("screen.blocked.message", "인증서 문제를 해결하고 다시 연결하면 화면을 볼 수 있습니다."))
        case .locked:
            StageMessage(symbol: "lock.display",
                         title: String.ui("screen.locked.title", "Windows 잠금 화면"),
                         message: String.ui("screen.locked.message", "잠금이 풀리면 화면이 바로 나타납니다. 잠금 화면은 원격으로 볼 수 없습니다."))
        case .unavailable(let state, let reason):
            StageMessage(symbol: "display.trianglebadge.exclamationmark",
                         title: state == .unsupported
                             ? String.ui("screen.unsupported.title", "이 PC는 화면 스트리밍을 지원하지 않습니다")
                             : String.ui("screen.unavailable.title", "지금은 화면을 가져올 수 없습니다"),
                         message: reason ?? String.ui("screen.unavailable.message", "에이전트가 화면 캡처를 준비하지 못했습니다. 진단을 실행해 원인을 확인하세요."))
        }
    }

    @ViewBuilder
    private func loading(rect: CGRect, reconnecting: Bool) -> some View {
        if shown.hasFrame {
            Scrim(rect: rect)
            StageCapsule(title: reconnecting
                             ? String.ui("screen.reconnecting", "다시 연결하는 중…")
                             : String.ui("screen.resuming", "화면을 다시 불러오는 중…"),
                         spinner: true)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScreenSkeleton(rect: rect, title: String.ui("screen.connecting", "화면을 불러오는 중…"), detail: loadingDetail,
                           error: shown.connectError)
        }
    }

    @ViewBuilder
    private func liveOverlays(rect: CGRect) -> some View {
        if owner == .ai {
            AIControlEdge(rect: rect, client: control?.ownerClient ?? host.ownerClient)
        } else if shown.isCaptured {
            Rectangle()
                .strokeBorder(DS.Color.human, lineWidth: 2)
                .frame(width: rect.width, height: rect.height)
                .offset(x: rect.minX, y: rect.minY)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        }
        if preferences.showsStatistics {
            ScreenHUD(metrics: shown.metrics, info: shown.info, stats: shown.stats, captured: shown.isCaptured,
                      remoteSize: remoteSize)
                .fixedSize()
                .offset(x: rect.minX + DS.Space.m, y: rect.minY + (owner == .ai ? DS.Space.xxl : DS.Space.m))
        }
        VStack(spacing: DS.Space.s) {
            Spacer(minLength: 0)
            if let denied = shown.inputDeniedAt {
                InputDeniedToast(takeOver: { store.controlActions.takeOver(host.hostID) },
                                 dismiss: { session.dismissDeniedNotice() })
                    .task(id: denied) {
                        try? await Task.sleep(for: .seconds(5))
                        session.dismissDeniedNotice()
                    }
            }
            if let marked = shown.markedText {
                MarkedTextChip(text: marked)
            }
        }
        .padding(.bottom, DS.Space.l)
        .frame(width: rect.width, height: rect.height)
        .offset(x: rect.minX, y: rect.minY)
    }
}

// MARK: Stage pieces

/// The still frame of a stand-in screen, with an arrow cursor.
private struct StillScreen: View {
    let image: CGImage
    let mapper: PointMapper
    let cursor: CGPoint?

    var body: some View {
        let rect = mapper.videoRect
        ZStack(alignment: .topLeading) {
            Image(decorative: image, scale: 1)
                .resizable()
                .interpolation(.high)
                .aspectRatio(contentMode: .fill)
                .frame(width: rect.width, height: rect.height)
                .clipped()
                .offset(x: rect.minX, y: rect.minY)
            if let cursor {
                let arrow = NSCursor.arrow
                Image(nsImage: arrow.image)
                    .offset(x: rect.minX + rect.width * cursor.x - arrow.hotSpot.x,
                            y: rect.minY + rect.height * cursor.y - arrow.hotSpot.y)
            }
        }
        .accessibilityHidden(true)
    }
}

/// Dims the video rect.
private struct Scrim: View {
    let rect: CGRect

    var body: some View {
        DS.Color.scrim
            .frame(width: rect.width, height: rect.height)
            .offset(x: rect.minX, y: rect.minY)
            .allowsHitTesting(false)
    }
}

/// Placeholder in the video rect while the first frame loads: a soft panel with a moving sheen,
/// a spinner, what is loading, and the last connection failure when attempts keep failing.
struct ScreenSkeleton: View {
    let rect: CGRect
    let title: String
    var detail: String?
    var error: OperationError?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                .fill(.white.opacity(0.05))
                .overlay {
                    if !reduceMotion {
                        TimelineView(.animation) { context in
                            let phase = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1.8) / 1.8
                            LinearGradient(colors: [.clear, .white.opacity(0.06), .clear],
                                           startPoint: UnitPoint(x: phase * 2 - 1, y: 0),
                                           endPoint: UnitPoint(x: phase * 2, y: 1))
                        }
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            VStack(spacing: DS.Space.m) {
                StageSpinner(size: 20)
                VStack(spacing: DS.Space.xs) {
                    Text(title)
                        .font(DS.Font.headline)
                        .foregroundStyle(.primary)
                    if let detail, !detail.isEmpty {
                        Text(detail)
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                if let error {
                    Label(String.ui("screen.connecting.retrying", "\(error.message) · 자동으로 다시 시도합니다"),
                          systemImage: "exclamationmark.triangle.fill")
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Color.warning)
                        .lineLimit(2)
                        .help(error.detail)
                }
            }
            .multilineTextAlignment(.center)
            .padding(DS.Space.l)
        }
        .frame(width: max(rect.width - 2 * DS.Space.l, 0), height: max(rect.height - 2 * DS.Space.l, 0))
        .offset(x: rect.minX + DS.Space.l, y: rect.minY + DS.Space.l)
        .accessibilityElement(children: .combine)
    }
}

/// Indeterminate spinner drawn for the dark stage: a rotating arc.
private struct StageSpinner: View {
    let size: CGFloat

    var body: some View {
        TimelineView(.animation) { context in
            let turn = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1) * 360
            Circle()
                .trim(from: 0, to: 0.72)
                .stroke(.white.opacity(0.85), style: StrokeStyle(lineWidth: max(2, size / 9), lineCap: .round))
                .rotationEffect(.degrees(turn))
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

/// A capsule with an optional spinner, centered over a dimmed frame.
private struct StageCapsule: View {
    let title: String
    let spinner: Bool

    var body: some View {
        HStack(spacing: DS.Space.s) {
            if spinner {
                StageSpinner(size: 14)
            }
            Text(title)
                .font(DS.Font.body.weight(.medium))
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.overlayPanel, in: Capsule())
        .overlay(Capsule().strokeBorder(.white.opacity(0.14)))
        .accessibilityElement(children: .combine)
    }
}

/// A centered symbol, title and message on the empty stage.
private struct StageMessage: View {
    let symbol: String
    let title: String
    let message: String

    var body: some View {
        EmptyStateView(symbol: symbol, title: title, message: message)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// A failure the person must act on, with retry.
private struct StageError: View {
    let error: OperationError
    let retry: () -> Void

    var body: some View {
        VStack(spacing: DS.Space.l) {
            ErrorBanner(message: String.ui("screen.failed.title", "화면 스트림을 열지 못했습니다"),
                        fix: [error.message, error.fix].compactMap { $0 }.joined(separator: " "),
                        code: error.code, retry: retry)
                .frame(maxWidth: 480)
        }
        .padding(DS.Space.xl)
    }
}

/// Purple edge around the video while an AI client drives the PC, with a tag on its top edge.
private struct AIControlEdge: View {
    let rect: CGRect
    let client: String?

    var body: some View {
        ZStack(alignment: .top) {
            Rectangle()
                .strokeBorder(DS.Color.ai, lineWidth: 3)
            Label(String.ui("screen.ai.tag", "AI 조작 중"), systemImage: "sparkles")
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.white)
                .padding(.horizontal, DS.Space.m)
                .padding(.vertical, DS.Space.xs)
                .background(DS.Color.aiFill, in: UnevenRoundedRectangle(bottomLeadingRadius: DS.Radius.s, bottomTrailingRadius: DS.Radius.s))
        }
        .frame(width: rect.width, height: rect.height)
        .offset(x: rect.minX, y: rect.minY)
        .allowsHitTesting(false)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(client.map { String.ui("screen.ai.a11yWithClient", "AI 조작 중 · \($0)") } ?? String.ui("screen.ai.tag", "AI 조작 중"))
    }
}

/// Composing text of the Mac input method, shown until it is committed.
private struct MarkedTextChip: View {
    let text: String

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(String.ui("screen.composing", "조합 중"))
                .foregroundStyle(.secondary)
            Text(text)
                .underline()
                .foregroundStyle(.primary)
        }
        .font(DS.Font.body)
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.overlayPanel, in: Capsule())
        .allowsHitTesting(false)
        .accessibilityElement(children: .combine)
    }
}

/// The daemon dropped input because an AI client holds control.
private struct InputDeniedToast: View {
    let takeOver: () -> Void
    let dismiss: () -> Void

    var body: some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: "hand.raised.fill")
                .foregroundStyle(DS.Color.ai)
                .accessibilityHidden(true)
            Text(String.ui("screen.denied", "AI가 조작 중이라 입력이 전달되지 않았습니다"))
                .font(DS.Font.body)
            Button(ControlActions.Action.takeOver.title, action: takeOver)
                .buttonStyle(.borderedProminent)
                .tint(DS.Color.aiFill)
                .controlSize(.small)
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .help(String.ui("action.dismiss", "닫기"))
            .accessibilityLabel(String.ui("action.dismiss", "닫기"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.overlayPanel, in: Capsule())
        .overlay(Capsule().strokeBorder(.white.opacity(0.14)))
    }
}

/// Why frames stopped, with what to do about it.
struct ScreenPausedCard: View {
    let reason: PauseReason
    let host: Host
    let display: Int
    let selectDisplay: (Int) -> Void
    let retry: () -> Void
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: symbol)
                    .font(.system(size: 20, weight: .medium))
                    .foregroundStyle(DS.Color.warning)
                    .frame(width: 28)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(title)
                        .font(DS.Font.headline)
                    Text(message)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .accessibilityElement(children: .combine)
            }
            if let command {
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
                    .tint(DS.Color.neutral)
                    .controlSize(.small)
                    .help(String.ui("screen.paused.rdp.copyHelp", "명령을 복사합니다"))
                }
                .padding(DS.Space.s)
                .background(.white.opacity(0.06), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .padding(.leading, 28 + DS.Space.m)
            }
            actions
                .padding(.leading, 28 + DS.Space.m)
        }
        .padding(DS.Space.l)
        .frame(maxWidth: 480, alignment: .leading)
        .background(DS.Color.surface, in: RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous).strokeBorder(.white.opacity(0.12)))
        .shadow(color: .black.opacity(0.4), radius: 20, y: 8)
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder
    private var actions: some View {
        switch reason {
        case .displayUnavailable:
            let others = host.displays.filter { $0.index != display }
            if !others.isEmpty {
                HStack(spacing: DS.Space.s) {
                    ForEach(others, id: \.index) { other in
                        Button(String.ui("screen.paused.display.show", "\(other.title) 보기")) { selectDisplay(other.index) }
                    }
                }
                .buttonStyle(.borderedProminent)
            }
        case .noViewer, .other:
            Button {
                retry()
            } label: {
                Label(String.ui("screen.paused.reconnect", "다시 연결"), systemImage: "arrow.clockwise")
            }
            .buttonStyle(.borderedProminent)
        case .secureDesktop, .sessionLocked, .rdpOutputSuppressed:
            EmptyView()
        }
    }

    private var symbol: String {
        switch reason {
        case .secureDesktop: "lock.shield"
        case .sessionLocked: "lock.display"
        case .displayUnavailable: "display.trianglebadge.exclamationmark"
        case .rdpOutputSuppressed: "rectangle.on.rectangle.slash"
        case .noViewer, .other: "pause.circle"
        }
    }

    private var title: String {
        switch reason {
        case .secureDesktop: .ui("screen.paused.secure.title", "PC에 보안 화면(UAC/잠금)이 떠 있습니다")
        case .sessionLocked: .ui("screen.paused.locked.title", "Windows가 잠겨 있습니다")
        case .displayUnavailable: .ui("screen.paused.display.title", "디스플레이를 찾을 수 없습니다")
        case .rdpOutputSuppressed: .ui("screen.paused.rdp.title", "원격 데스크톱 창이 최소화되어 화면이 멈췄습니다")
        case .noViewer: .ui("screen.paused.noViewer.title", "PC가 화면 전송을 멈췄습니다")
        case .other: .ui("screen.paused.other.title", "화면 전송이 일시 중지되었습니다")
        }
    }

    private var message: String {
        switch reason {
        case .secureDesktop:
            .ui("screen.paused.secure.message", "사용자 계정 컨트롤 창이나 Ctrl+Alt+Del 화면은 원격으로 보거나 조작할 수 없습니다. PC 앞에서 승인하거나 닫으면 화면이 바로 이어집니다.")
        case .sessionLocked:
            .ui("screen.paused.locked.message", "PC 앞에서 잠금을 풀면 화면이 바로 이어집니다. 잠금 화면은 원격으로 조작할 수 없습니다.")
        case .displayUnavailable:
            .ui("screen.paused.display.message", "\(DisplayInfo.title(index: display)) 연결이 끊겼거나 모니터가 꺼져 있습니다. 모니터 전원을 확인하거나 다른 디스플레이를 고르세요.")
        case .rdpOutputSuppressed:
            .ui("screen.paused.rdp.message", "이 PC는 원격 데스크톱(RDP) 세션이라, RDP 창이 최소화되면 Windows가 화면 그리기를 멈춥니다. RDP 창을 다시 열거나, PC의 관리자 명령 프롬프트에서 아래 명령으로 세션을 콘솔로 옮기세요. RDP 연결은 끊깁니다.")
        case .noViewer:
            .ui("screen.paused.noViewer.message", "보는 사람이 없다고 판단해 캡처를 멈췄습니다. 다시 연결하면 이어집니다.")
        case .other(let raw):
            .ui("screen.paused.other.message", "에이전트가 알린 이유: \(raw)")
        }
    }

    /// The `tscon` command that moves the RDP session to the console, for `rdpOutputSuppressed`.
    private var command: String? {
        guard reason == .rdpOutputSuppressed else { return nil }
        return "tscon \(host.session?.sessionID ?? 1) /dest:console"
    }
}
