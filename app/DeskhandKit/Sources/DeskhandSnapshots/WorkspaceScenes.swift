import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Workspace scenes at 1440×900: the window drawn as the sidebar beside the toolbar strip,
/// tab bar, control banner, tab content and inspector.
@MainActor
enum WorkspaceScenes {
    static let size = CGSize(width: 1440, height: 900)

    private static let defaults = UserDefaults(suiteName: "io.github.yangtaeyoung.Deskhand.snapshots") ?? .standard

    struct Setup {
        var selected: String
        var tab: WorkspaceTab = .screen
        var inspector: WorkspaceLayout.InspectorPane? = .activity
        /// Control states that replace the fixtures' for these hosts.
        var controls: [String: ControlState] = [:]
        /// Timeline replies that replace the fixtures' for these hosts.
        var timelines: [String: SnapshotBackend.TimelineReply] = [:]
        var controlFailure: OperationError?
        /// Display shown on the selected computer.
        var display: Int?
        var statistics = false
        /// Screen stand-ins that replace the live default for these hosts.
        var screens: [String: ScreenStandIn] = [:]
    }

    /// A store with every fixture computer, `setup.selected` selected and its layout applied.
    static func store(_ setup: Setup) -> AppStore {
        let hosts = PreviewFixtures.hosts()
        var controls = PreviewFixtures.controls(for: hosts)
        controls.merge(setup.controls) { _, new in new }
        var replies: [String: SnapshotBackend.TimelineReply] = [:]
        var timelines: [String: [TimelineEntry]] = [:]
        var errors: [String: OperationError] = [:]
        for host in hosts {
            let reply = setup.timelines[host.hostID] ?? .entries(PreviewFixtures.timeline(hostID: host.hostID))
            replies[host.hostID] = reply
            switch reply {
            case .entries(let entries): timelines[host.hostID] = entries
            case .failure(let error): errors[host.hostID] = error
            case .pending: break
            }
        }
        var qualities: [String: StreamQuality] = [:]
        for host in hosts {
            if let standIn = setup.screens[host.hostID], standIn.session.phase != .live { continue }
            if let quality = PreviewFixtures.streamQuality(hostID: host.hostID) { qualities[host.hostID] = quality }
        }
        let backend = SnapshotBackend(controls: controls, timelines: replies, controlFailure: setup.controlFailure)
        let store = AppStore.seeded(backend: backend, defaults: defaults, hosts: hosts, selectedHostID: setup.selected,
                                    controls: controls, timelines: timelines, timelineErrors: errors,
                                    streamQualities: qualities)
        store.qa.evidence.seed(PreviewFixtures.timelineThumbnails())
        store.layout.tab = setup.tab
        store.layout.showsInspector = setup.inspector != nil
        store.layout.inspectorPane = setup.inspector ?? .activity
        resetScreen(store)
        store.screenPreferences.showsStatistics = setup.statistics
        if let display = setup.display { store.screenPreferences.setDisplay(display, for: setup.selected) }
        if setup.tab == .files { FilesScenes.seed(store, hostID: setup.selected) }
        return store
    }

    /// Default screen preferences: balanced preset, no statistics, scancode keyboard, primary displays.
    static func resetScreen(_ store: AppStore) {
        let preferences = store.screenPreferences
        preferences.preset = .balanced
        preferences.showsStatistics = false
        preferences.keyboardMode = .scancode
        preferences.swapCommandControl = true
        preferences.rightOptionHangul = true
        preferences.rightCommandHanja = false
        for host in store.hosts {
            preferences.setDisplay(host.displays.first(where: \.primary)?.index ?? 0, for: host.hostID)
        }
    }

    static func scene(_ name: String, _ setup: Setup, after: @escaping @MainActor (AppStore) -> Void = { _ in }) -> SnapshotScene {
        SnapshotScene(name: name, size: size) {
            let store = store(setup)
            let _ = after(store)
            SplitStandIn(store: store, screens: ScreenScenes.standIns(for: store.hosts, overrides: setup.screens))
        }
    }

    static var all: [SnapshotScene] {
        let dev = PreviewFixtures.devPCID
        return [
            scene("workspace-observe", Setup(selected: dev, controls: [dev: ControlState(owner: "none", gen: 6, aiPaused: false)])),
            scene("workspace-ai-control", Setup(selected: dev)),
            scene("workspace-human-control", Setup(selected: dev, tab: .terminal,
                                                   controls: [dev: ControlState(owner: "human", gen: 6, aiPaused: false)])),
            scene("workspace-ai-paused", Setup(selected: PreviewFixtures.stagingID)),
            scene("workspace-reconnecting", Setup(selected: PreviewFixtures.buildServerID, tab: .logs)),
            scene("workspace-session-locked", Setup(selected: PreviewFixtures.kioskID)),
            scene("workspace-offline", Setup(selected: PreviewFixtures.designID, tab: .files, inspector: .info)),
            scene("workspace-blocked", Setup(selected: PreviewFixtures.labID, tab: .processes)),
            scene("workspace-repairing", Setup(selected: PreviewFixtures.labID, tab: .processes)) { store in
                store.settings.seed(daemonInfo: nil, hostActions: [PreviewFixtures.labID: .repair])
            },
            scene("workspace-repair-failed", Setup(selected: PreviewFixtures.labID, tab: .processes)) { store in
                store.settings.seed(daemonInfo: nil, hostErrors: [
                    PreviewFixtures.labID: OperationError(code: "AUTH_REQUIRED", message: "lab-pc에 SSH로 접속하지 못했습니다"),
                ], failedActions: [PreviewFixtures.labID: .repair])
            },
            scene("workspace-timeline-error", Setup(selected: PreviewFixtures.qaLaptopID, tab: .files, timelines: [
                PreviewFixtures.qaLaptopID: .failure(OperationError(message: "데몬과 연결이 끊겼습니다")),
            ])),
            scene("workspace-info", Setup(selected: dev, tab: .debugger, inspector: .info)),
            scene("workspace-timeline-loading", Setup(selected: PreviewFixtures.meetingPCID, tab: .qa,
                                                      timelines: [PreviewFixtures.meetingPCID: .pending])),
            scene("workspace-inspector-hidden", Setup(selected: PreviewFixtures.qaLaptopID, inspector: nil)),
            scene("workspace-control-error", Setup(
                selected: dev,
                controlFailure: OperationError(ClientError.timeout(method: "control.stop")))) { store in
                store.controlActions.stop(dev)
            },
            SnapshotScene(name: "workspace-narrow", size: CGSize(width: 960, height: 600)) {
                SplitStandIn(store: store(Setup(selected: dev)))
            },
            SnapshotScene(name: "workspace-takeover-confirm", size: size) {
                let store = store(Setup(selected: dev))
                let timeline = PreviewFixtures.timeline(hostID: dev)
                SheetStandIn(base: SplitStandIn(store: store)) {
                    TakeoverConfirmView(computerName: "개발 PC", client: "Claude Code",
                                        lastAIAction: timeline.first { $0.actor == "ai" },
                                        confirm: {}, cancel: {})
                }
            },
        ]
    }
}
