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

    /// Control items are shown unless the computer is offline or blocked.
    var showsControls: Bool {
        switch host.connection {
        case .ready, .degraded, .connecting, .reconnecting: true
        case .offline, .blocked: false
        }
    }

    /// Take-over and give-back need a live connection.
    var canChangeControl: Bool {
        host.connection == .ready || host.connection == .degraded
    }

    /// Stream quality while the screen tab is open and a stream reports one.
    var quality: StreamQuality? {
        guard store.layout.tab == .screen, canChangeControl else { return nil }
        return store.streamQuality(for: host.hostID)
    }

    /// The control action next to the pill: give back while the human holds control or the AI
    /// is paused, take over otherwise.
    var contextualAction: ControlActions.Action {
        owner == .human || control?.aiPaused == true ? .giveBack : .takeOver
    }
}

/// Toolbar items of the workspace window: stream quality, the control pill, the contextual
/// control action, the emergency stop and the inspector toggle.
struct WorkspaceToolbar: ToolbarContent {
    let store: AppStore
    let host: Host

    private var model: WorkspaceToolbarModel { WorkspaceToolbarModel(store: store, host: host) }

    var body: some ToolbarContent {
        if let quality = model.quality {
            ToolbarItem(placement: .primaryAction) {
                StreamQualityLabel(quality: quality, connection: host.connection)
            }
            .sharedBackgroundVisibility(.hidden)
        }
        if model.showsControls {
            ToolbarItem(placement: .primaryAction) {
                WorkspaceControlPill(model: model)
            }
            .sharedBackgroundVisibility(.hidden)
            ToolbarItemGroup(placement: .primaryAction) {
                ContextualControlButton(model: model)
                EmergencyStopButton(store: store, host: host)
            }
        }
        ToolbarSpacer(.fixed, placement: .primaryAction)
        ToolbarItem(placement: .primaryAction) {
            InspectorToggleButton(layout: store.layout)
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
            if model.showsControls {
                WorkspaceControlPill(model: model)
                HStack(spacing: DS.Space.s) {
                    ContextualControlButton(model: model)
                        .buttonStyle(.bordered)
                    EmergencyStopButton(store: model.store, host: model.host)
                }
            }
            InspectorToggleButton(layout: model.store.layout)
                .buttonStyle(.bordered)
                .padding(.leading, DS.Space.s)
        }
        .fixedSize()
    }
}

/// `60 fps · 18 ms` with a gauge symbol; tinted orange while the connection is degraded.
struct StreamQualityLabel: View {
    let quality: StreamQuality
    let connection: ConnectionState

    private var fps: Int { Int(quality.fps.rounded()) }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: connection == .degraded ? "wifi.exclamationmark" : "gauge.with.dots.needle.50percent")
                .foregroundStyle(connection == .degraded ? AnyShapeStyle(DS.Color.warning) : AnyShapeStyle(.secondary))
            Text(String.ui("quality.short", "\(fps) fps · \(quality.latencyMS) ms"))
                .monospacedDigit()
        }
        .font(DS.Font.caption)
        .foregroundStyle(.secondary)
        .fixedSize()
        .help(String.ui("quality.help", "화면 스트림: 초당 \(fps)프레임, 캡처에서 표시까지 \(quality.latencyMS) ms"))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("quality.a11y", "화면 스트림 초당 \(fps)프레임, 지연 \(quality.latencyMS)밀리초"))
    }
}

/// The control pill for the toolbar: 로그인 필요 while the session is locked.
struct WorkspaceControlPill: View {
    let model: WorkspaceToolbarModel

    var body: some View {
        ControlPill(owner: model.owner, clientName: model.control?.ownerClient ?? model.host.ownerClient,
                    sessionLocked: model.host.isSessionLocked)
    }
}

/// 제어권 가져오기 or AI에게 넘기기, with a spinner while the call runs.
struct ContextualControlButton: View {
    let model: WorkspaceToolbarModel

    var body: some View {
        let actions = model.store.controlActions
        let hostID = model.host.hostID
        let action = model.contextualAction
        let running = actions.isRunning(.takeOver, on: hostID) || actions.isRunning(.giveBack, on: hostID)
        Button {
            switch action {
            case .takeOver: actions.takeOver(hostID)
            case .giveBack: actions.giveBack(hostID)
            case .stop: actions.stop(hostID)
            }
        } label: {
            ControlActionLabel(action: action, running: running)
        }
        .disabled(running || !model.canChangeControl)
        .help(action == .takeOver
              ? String.ui("help.takeOver", "AI 대신 내가 이 PC를 조작합니다")
              : String.ui("help.giveBack", "AI가 다시 이 PC를 조작할 수 있게 합니다"))
    }
}

/// Red 긴급 정지 button (⇧⌘. in the menu bar).
struct EmergencyStopButton: View {
    let store: AppStore
    let host: Host

    var body: some View {
        let running = store.controlActions.isRunning(.stop, on: host.hostID)
        Button {
            store.controlActions.stop(host.hostID)
        } label: {
            ControlActionLabel(action: .stop, running: running)
        }
        .buttonStyle(.borderedProminent)
        .tint(DS.Color.danger)
        .disabled(running)
        .help(String.ui("help.stop", "AI 입력을 즉시 멈추고 내가 제어권을 가집니다 (⇧⌘.)"))
    }
}

/// Shows or hides the inspector (⌥⌘0).
struct InspectorToggleButton: View {
    @Bindable var layout: WorkspaceLayout

    var body: some View {
        Button {
            layout.showsInspector.toggle()
        } label: {
            Label(String.ui("inspector.toggle", "인스펙터"), systemImage: "sidebar.right")
                .labelStyle(.iconOnly)
        }
        .help(layout.showsInspector
              ? String.ui("help.hideInspector", "인스펙터 가리기 (⌥⌘0)")
              : String.ui("help.showInspector", "인스펙터 보기 (⌥⌘0)"))
        .accessibilityLabel(layout.showsInspector
                            ? String.ui("menu.hideInspector", "인스펙터 가리기")
                            : String.ui("menu.showInspector", "인스펙터 보기"))
    }
}
