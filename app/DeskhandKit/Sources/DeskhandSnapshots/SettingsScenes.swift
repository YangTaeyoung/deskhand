import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Settings window scenes at the window's size: the title and pane tabs drawn as a strip over
/// each pane. Every scene starts from empty settings.
@MainActor
enum SettingsScenes {
    static let suite = "io.github.yangtaeyoung.Deskhand.snapshots.settings"
    static let dev = PreviewFixtures.devPCID

    /// A store with the fixture computers and the development PC selected, on fresh settings.
    static func store(hosts: [Host] = PreviewFixtures.hosts(), phase: AppStore.Phase = .ready) -> AppStore {
        UserDefaults.standard.removePersistentDomain(forName: suite)
        let defaults = UserDefaults(suiteName: suite) ?? .standard
        let backend = SnapshotBackend(controls: PreviewFixtures.controls(for: hosts), timelines: [:],
                                      daemonVersion: phase == .ready ? "0.1.0" : nil)
        let store = AppStore.seeded(backend: backend, defaults: defaults, phase: phase, hosts: hosts,
                                    selectedHostID: hosts.first?.hostID)
        store.settings.seed(daemonInfo: phase == .ready ? DaemonInfo(version: "0.1.0", apiVersion: 1) : DaemonInfo(version: nil, apiVersion: nil))
        return store
    }

    static func scene(_ name: String, _ pane: SettingsPane, setup: @escaping @MainActor (AppStore) -> Void = { _ in },
                      store makeStore: @escaping @MainActor () -> AppStore = { store() }) -> SnapshotScene {
        SnapshotScene(name: name, size: SettingsWindowStandIn.size(pane)) {
            let store = makeStore()
            let _ = setup(store)
            SettingsWindowStandIn(store: store, pane: pane)
        }
    }

    static var all: [SnapshotScene] {
        [
            scene("settings-general", .general),
            scene("settings-general-unavailable", .general, store: {
                store(phase: .daemonUnavailable(AppStore.DaemonFailure(message: "5초 안에 데몬이 응답하지 않았습니다", logPath: Paths.daemonLog)))
            }),
            scene("settings-connection", .connection) { store in
                store.settings.connectionHostID = dev
            },
            scene("settings-connection-blocked", .connection) { store in
                store.settings.connectionHostID = PreviewFixtures.labID
                store.settings.seed(daemonInfo: DaemonInfo(version: "0.1.0", apiVersion: 1), hostErrors: [
                    PreviewFixtures.labID: OperationError(code: "AUTH_REQUIRED", message: "lab-pc에 SSH로 접속하지 못했습니다",
                                                          nextAction: "host_doctor"),
                ])
            },
            scene("settings-connection-empty", .connection, store: { store(hosts: []) }),
            scene("settings-streaming", .streaming) { store in
                store.screenPreferences.showsStatistics = true
            },
            scene("settings-keyboard", .keyboard),
            scene("settings-files", .files) { store in
                store.settings.scopeHostID = dev
                store.filesPreferences.addWorkRoot("C:\\Work\\Shop", for: dev, style: .windows)
                store.filesPreferences.addWorkRoot("D:\\Builds\\nightly", for: dev, style: .windows)
            },
            scene("settings-privacy", .privacy) { store in
                store.settings.scopeHostID = dev
                store.filesPreferences.addWorkRoot("C:\\Work\\Shop", for: dev, style: .windows)
                store.privacyPreferences.setScope(AIScope(allowsScreen: true, files: .workRoots, allowsClipboard: false), for: dev)
            },
            scene("settings-privacy-no-roots", .privacy) { store in
                store.settings.scopeHostID = PreviewFixtures.qaLaptopID
                store.privacyPreferences.setScope(AIScope(allowsScreen: false, files: .workRoots, allowsClipboard: true),
                                                  for: PreviewFixtures.qaLaptopID)
            },
        ]
    }
}
