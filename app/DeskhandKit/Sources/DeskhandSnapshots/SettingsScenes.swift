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
        store.settings.appVersion = "0.1.0"
        return store
    }

    static func scene(_ name: String, _ pane: SettingsPane, setup: @escaping @MainActor (AppStore) -> Void = { _ in },
                      store makeStore: @escaping @MainActor () -> AppStore = { store() }) -> SnapshotScene {
        let sized = makeStore()
        setup(sized)
        let size = SettingsWindowStandIn.size(pane, store: sized)
        return SnapshotScene(name: name, size: size) {
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
            scene("settings-files-no-roots", .files) { store in
                store.settings.scopeHostID = PreviewFixtures.qaLaptopID
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

/// The settings window with its title and toolbar tabs drawn as a strip over the pane content.
struct SettingsWindowStandIn: View {
    let store: AppStore
    let pane: SettingsPane

    var body: some View {
        VStack(spacing: 0) {
            SettingsToolbarStrip(selection: pane)
            Divider()
            SettingsPaneContent(store: store, pane: pane)
        }
        .frame(width: SettingsPane.width)
    }

    /// Window size of the stand-in showing `pane` for `store`.
    static func size(_ pane: SettingsPane, store: AppStore) -> CGSize {
        let content = pane.size(for: store)
        return CGSize(width: content.width, height: content.height + SettingsToolbarStrip.height + 1)
    }
}

/// The settings window's title and toolbar tabs: the pane title centered over a row of
/// symbol-and-title tabs, the selected one highlighted.
struct SettingsToolbarStrip: View {
    static let height: CGFloat = 84

    let selection: SettingsPane

    var body: some View {
        VStack(spacing: DS.Space.xs) {
            Text(selection.title)
                .font(DS.Font.headline)
                .frame(height: 24)
            HStack(spacing: DS.Space.xs) {
                ForEach(SettingsPane.allCases) { pane in
                    tab(pane)
                }
            }
        }
        .frame(maxWidth: .infinity)
        .frame(height: Self.height, alignment: .top)
        .padding(.top, DS.Space.xs)
        .background(DS.Color.surfaceRaised.opacity(0.5))
    }

    private func tab(_ pane: SettingsPane) -> some View {
        let selected = pane == selection
        return VStack(spacing: DS.Space.xs) {
            Image(systemName: pane.symbol)
                .font(.system(size: 17, weight: .regular))
                .frame(height: 20)
            Text(pane.title)
                .font(DS.Font.caption)
                .lineLimit(1)
        }
        .foregroundStyle(selected ? AnyShapeStyle(DS.Status.accentText) : AnyShapeStyle(DS.Status.neutralText))
        .frame(minWidth: 64)
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .background(selected ? DS.Color.neutral.opacity(0.16) : .clear,
                    in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
    }
}
