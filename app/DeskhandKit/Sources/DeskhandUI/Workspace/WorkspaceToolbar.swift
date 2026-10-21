import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// What the workspace toolbar shows for a computer.
@MainActor
struct WorkspaceToolbarModel {
    let store: AppStore
    let host: Host

    var control: ControlState? { store.control(for: host.hostID) }
    var owner: ControlOwner { control.flatMap { ControlOwner(rawValue: $0.owner) } ?? host.controlOwner }
    var bannerKind: ControlBannerKind { ControlBannerKind(host: host, control: control) }

    /// The pill shows while a connection exists or is being restored, and on a disconnected
    /// computer while an AI client holds control or the AI is paused.
    var showsPill: Bool {
        switch host.connection {
        case .ready, .degraded, .connecting, .reconnecting: true
        case .offline, .blocked: owner == .ai || control?.aiPaused == true
        }
    }

    /// The control pill with the current values, sized to the large toolbar buttons.
    var pill: ControlPill {
        ControlPill(owner: owner, clientName: control?.ownerClient ?? host.ownerClient,
                    aiPaused: control?.aiPaused == true, sessionLocked: host.isSessionLocked, size: .large)
    }

    /// 제어권 가져오기 shows only while observing. In the other states the control banner carries
    /// the control action, or no control action applies.
    var showsTakeOver: Bool { bannerKind == .observing }

    /// Stream quality while the screen tab is open and a stream reports one.
    var quality: StreamQuality? {
        guard store.layout.tab == .screen, host.connection == .ready || host.connection == .degraded else { return nil }
        return store.streamQuality(for: host.hostID)
    }
}

/// Toolbar items of the workspace window: stream quality, the control pill, 제어권 가져오기
/// while observing, the emergency stop (always) and the inspector toggle.
struct WorkspaceToolbar: ToolbarContent {
    let store: AppStore
    let host: Host

    private var model: WorkspaceToolbarModel { WorkspaceToolbarModel(store: store, host: host) }

    var body: some ToolbarContent {
        if let quality = model.quality {
            ToolbarItem(placement: .primaryAction) {
                StreamQualityLabel(quality: quality, connection: host.connection)
                    .id(quality)
            }
            .sharedBackgroundVisibility(.hidden)
        }
        if model.showsPill {
            let pill = model.pill
            ToolbarItem(placement: .primaryAction) {
                pill
                    .id(pill.identity)
            }
            .sharedBackgroundVisibility(.hidden)
        }
        let takeRunning = store.controlActions.isRunning(.takeOver, on: host.hostID)
        let stopRunning = store.controlActions.isRunning(.stop, on: host.hostID)
        ToolbarItemGroup(placement: .primaryAction) {
            if model.showsTakeOver {
                TakeOverButton(store: store, host: host, running: takeRunning)
                    .id(takeRunning)
            }
            EmergencyStopButton(store: store, host: host, running: stopRunning)
                .id(stopRunning)
        }
        ToolbarSpacer(.fixed, placement: .primaryAction)
        let showsInspector = store.layout.showsInspector
        ToolbarItem(placement: .primaryAction) {
            InspectorToggleButton(layout: store.layout, showsInspector: showsInspector)
                .id(showsInspector)
        }
    }
}

/// Window toolbar drawn as a strip, for offscreen snapshots where the system toolbar does not
/// render: the title and subtitle at the leading edge and the same items as `WorkspaceToolbar`.
public struct WorkspaceToolbarStrip: View {
    /// Height of the window toolbar.
    public static let height: CGFloat = 52

    let store: AppStore

    public init(store: AppStore) {
        self.store = store
    }

    public var body: some View {
        HStack(spacing: DS.Space.m) {
            VStack(alignment: .leading, spacing: 0) {
                Text(store.selectedHost?.name ?? "Deskhand")
                    .font(DS.Font.headline)
                if let host = store.selectedHost {
                    Text(host.sessionSubtitle)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .lineLimit(1)
            .layoutPriority(1)
            Spacer(minLength: DS.Space.l)
            if let host = store.selectedHost {
                let model = WorkspaceToolbarModel(store: store, host: host)
                ViewThatFits(in: .horizontal) {
                    items(model, showsQuality: true)
                    items(model, showsQuality: false)
                }
            }
        }
        .controlSize(.large)
        .padding(.horizontal, DS.Space.l)
        .frame(height: Self.height)
        .frame(maxWidth: .infinity)
        .background(DS.Color.surface)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
    }

    /// The toolbar items in order; the quality readout is the first to go when space runs out.
    private func items(_ model: WorkspaceToolbarModel, showsQuality: Bool) -> some View {
        HStack(spacing: DS.Space.m) {
            if showsQuality, let quality = model.quality {
                StreamQualityLabel(quality: quality, connection: model.host.connection)
            }
            if model.showsPill {
                model.pill
            }
            HStack(spacing: DS.Space.s) {
                if model.showsTakeOver {
                    TakeOverButton(store: model.store, host: model.host,
                                   running: model.store.controlActions.isRunning(.takeOver, on: model.host.hostID))
                        .buttonStyle(.bordered)
                }
                EmergencyStopButton(store: model.store, host: model.host,
                                    running: model.store.controlActions.isRunning(.stop, on: model.host.hostID))
            }
            InspectorToggleButton(layout: model.store.layout, showsInspector: model.store.layout.showsInspector)
                .buttonStyle(.bordered)
                .padding(.leading, DS.Space.s)
        }
        .fixedSize()
    }
}

/// `60 fps · 18 ms` with a gauge symbol, or the received bitrate in place of the latency until
/// the daemon reports a latency measurement; tinted orange while the connection is degraded.
struct StreamQualityLabel: View {
    let quality: StreamQuality
    let connection: ConnectionState

    private var fps: Int { Int(quality.fps.rounded()) }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: connection == .degraded ? "wifi.exclamationmark" : "gauge.with.dots.needle.50percent")
                .foregroundStyle(connection == .degraded ? AnyShapeStyle(DS.Color.warning) : AnyShapeStyle(.secondary))
            Text(text)
                .monospacedDigit()
        }
        .font(DS.Font.caption)
        .foregroundStyle(.secondary)
        .fixedSize()
        .help(help)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
    }

    private var text: String {
        if let latency = quality.latencyMS { return .ui("quality.short", "\(fps) fps · \(latency) ms") }
        if let megabits = quality.megabitsText { return .ui("quality.short.bitrate", "\(fps) fps · \(megabits) Mbps") }
        return .ui("quality.short.fps", "\(fps) fps")
    }

    private var help: String {
        if let latency = quality.latencyMS {
            return .ui("quality.help", "화면 스트림: 초당 \(fps)프레임, 캡처에서 표시까지 \(latency) ms")
        }
        let megabits = quality.megabitsText ?? "—"
        return .ui("quality.help.bitrate", "화면 스트림: 초당 \(fps)프레임, \(megabits) Mbps 수신. 지연은 데몬이 측정값을 보내면 표시합니다.")
    }

    private var accessibilityText: String {
        if let latency = quality.latencyMS { return .ui("quality.a11y", "화면 스트림 초당 \(fps)프레임, 지연 \(latency)밀리초") }
        let megabits = quality.megabitsText ?? "—"
        return .ui("quality.a11y.bitrate", "화면 스트림 초당 \(fps)프레임, \(megabits)메가비트")
    }
}

/// 제어권 가져오기, with a spinner while the call runs. `running` is read where the toolbar
/// is built.
struct TakeOverButton: View {
    let store: AppStore
    let host: Host
    let running: Bool

    var body: some View {
        Button {
            store.controlActions.takeOver(host.hostID)
        } label: {
            ControlActionLabel(action: .takeOver, running: running)
        }
        .disabled(running)
        .help(String.ui("help.takeOver", "AI 대신 내가 이 PC를 조작합니다"))
    }
}

/// Red 긴급 정지 button (⇧⌘. in the menu bar), with a spinner while a call runs; it stays
/// pressable and sends another stop. `running` is read where the toolbar is built.
struct EmergencyStopButton: View {
    let store: AppStore
    let host: Host
    let running: Bool

    var body: some View {
        Button {
            store.controlActions.stop(host.hostID)
        } label: {
            ControlActionLabel(action: .stop, running: running)
        }
        .buttonStyle(.borderedProminent)
        .tint(DS.Color.danger)
        .help(String.ui("help.stop", "AI 입력을 즉시 멈추고 내가 제어권을 가집니다 (⇧⌘.)"))
    }
}

/// Shows or hides the inspector (⌥⌘0). `showsInspector` is read where the toolbar is built;
/// the title and tooltip follow it.
struct InspectorToggleButton: View {
    let layout: WorkspaceLayout
    let showsInspector: Bool

    var body: some View {
        Button {
            layout.showsInspector.toggle()
        } label: {
            Label(showsInspector ? String.ui("menu.hideInspector", "인스펙터 가리기") : String.ui("menu.showInspector", "인스펙터 보기"),
                  systemImage: "sidebar.right")
                .labelStyle(.iconOnly)
        }
        .help(showsInspector
              ? String.ui("help.hideInspector", "인스펙터 가리기 (⌥⌘0)")
              : String.ui("help.showInspector", "인스펙터 보기 (⌥⌘0)"))
    }
}
