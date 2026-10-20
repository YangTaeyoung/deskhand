import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Scenes of the main window at 1280×800. The split view is drawn as its sidebar and detail
/// columns side by side, and sheets as a panel centered over the dimmed window.
@MainActor
enum RootScenes {
    static let size = CGSize(width: 1280, height: 800)
    static let sidebarWidth: CGFloat = 260

    private static let defaults = UserDefaults(suiteName: "io.github.yangtaeyoung.Deskhand.snapshots") ?? .standard
    private static let backend = PreviewBackend(scenario: .standard, stepDelay: .zero)

    static func store(hosts: [Host], selected: String?) -> AppStore {
        let store = AppStore.seeded(backend: backend, defaults: defaults, hosts: hosts, selectedHostID: selected,
                                    timelines: Dictionary(uniqueKeysWithValues: hosts.map { ($0.hostID, PreviewFixtures.timeline(hostID: $0.hostID)) }))
        resetLayout(store)
        return store
    }

    /// The default workspace layout: the screen tab with the activity inspector.
    static func resetLayout(_ store: AppStore) {
        store.layout.tab = .screen
        store.layout.showsInspector = true
        store.layout.inspectorPane = .activity
    }

    static var withHosts: AppStore { store(hosts: PreviewFixtures.hosts(), selected: PreviewFixtures.devPCID) }
    static var empty: AppStore { store(hosts: [], selected: nil) }

    static func steps(doneThrough last: InstallStep?, then status: StepStatus, at step: InstallStep?, alias: String) -> [StepItem] {
        InstallStep.allCases.map { s in
            let index = InstallStep.allCases.firstIndex(of: s)!
            if let last, index <= InstallStep.allCases.firstIndex(of: last)! {
                return StepItem(id: s.rawValue, title: s.title, detail: PreviewFixtures.installDetail(s, alias: alias, done: true), status: .done)
            }
            if s == step {
                let detail = status == .failed
                    ? PreviewFixtures.installFailure(alias: alias).message
                    : PreviewFixtures.installDetail(s, alias: alias, done: false)
                return StepItem(id: s.rawValue, title: s.title, detail: detail, status: status)
            }
            return StepItem(id: s.rawValue, title: s.title, detail: s.activeDescription, status: .pending)
        }
    }

    static var all: [SnapshotScene] {
        [
            SnapshotScene(name: "root-empty", size: size) {
                SplitStandIn(store: empty)
            },
            SnapshotScene(name: "root-hosts", size: size) {
                SplitStandIn(store: withHosts)
            },
            SnapshotScene(name: "add-host-progress", size: size) {
                SheetStandIn(base: SplitStandIn(store: empty)) {
                    AddHostSheet(flow: AddHostFlow(
                        backend: backend, alias: "windows", name: "개발 PC", aliases: PreviewFixtures.sshAliases,
                        stage: .running, steps: steps(doneThrough: .copy, then: .active, at: .initialize, alias: "windows")))
                }
            },
            SnapshotScene(name: "add-host-error", size: size) {
                let alias = PreviewFixtures.failingAlias
                SheetStandIn(base: SplitStandIn(store: withHosts)) {
                    AddHostSheet(flow: AddHostFlow(
                        backend: backend, alias: alias, name: "미디어 PC", aliases: PreviewFixtures.sshAliases,
                        stage: .failed(PreviewFixtures.installFailure(alias: alias)),
                        steps: steps(doneThrough: .initialize, then: .failed, at: .install, alias: alias)))
                }
            },
            SnapshotScene(name: "add-host-form", size: size) {
                SheetStandIn(base: SplitStandIn(store: empty)) {
                    AddHostSheet(flow: AddHostFlow(
                        backend: backend, alias: "windows", name: "", aliases: PreviewFixtures.sshAliases,
                        stage: .form, steps: []))
                }
            },
            SnapshotScene(name: "add-host-connected", size: size) {
                let host = PreviewFixtures.newHost(id: "host-new", name: "미디어 PC", alias: "media-pc")
                SheetStandIn(base: SplitStandIn(store: empty)) {
                    AddHostSheet(flow: AddHostFlow(
                        backend: backend, alias: "media-pc", name: "미디어 PC", aliases: PreviewFixtures.sshAliases,
                        stage: .succeeded(host), steps: steps(doneThrough: .verify, then: .done, at: nil, alias: "media-pc")))
                }
            },
            SnapshotScene(name: "root-reconnecting", size: size) {
                let store = AppStore.seeded(
                    backend: backend, defaults: defaults, phase: .reconnecting, hosts: PreviewFixtures.hosts(),
                    selectedHostID: PreviewFixtures.devPCID,
                    timelines: [PreviewFixtures.devPCID: PreviewFixtures.timeline(hostID: PreviewFixtures.devPCID)])
                let _ = resetLayout(store)
                SplitStandIn(store: store)
            },
            SnapshotScene(name: "root-host-blocked", size: size) {
                SplitStandIn(store: store(hosts: PreviewFixtures.hosts(), selected: PreviewFixtures.labID))
            },
            SnapshotScene(name: "root-host-reconnecting", size: size) {
                SplitStandIn(store: store(hosts: PreviewFixtures.hosts(), selected: PreviewFixtures.buildServerID))
            },
            SnapshotScene(name: "doctor", size: size) {
                let host = PreviewFixtures.hosts()[0]
                SheetStandIn(base: SplitStandIn(store: withHosts)) {
                    DoctorView(model: DoctorModel(backend: backend, host: host,
                                                  status: .loaded(PreviewFixtures.doctor(hostID: host.hostID)),
                                                  finishedAt: .now)) {}
                }
            },
            SnapshotScene(name: "doctor-overflow", size: size) {
                let host = PreviewFixtures.hosts()[0]
                let checks = PreviewFixtures.doctor(hostID: PreviewFixtures.devPCID)
                    + PreviewFixtures.doctor(hostID: PreviewFixtures.labID)
                SheetStandIn(base: SplitStandIn(store: withHosts)) {
                    DoctorView(model: DoctorModel(backend: backend, host: host, status: .loaded(checks), finishedAt: .now)) {}
                }
            },
            SnapshotScene(name: "daemon-unavailable", size: size) {
                DaemonUnavailableView(failure: AppStore.DaemonFailure(message: "5초 안에 데몬이 응답하지 않았습니다",
                                                                      logPath: Paths.daemonLog)) {}
            },
        ]
    }
}

/// Sidebar and detail columns of the computers split view, side by side, with the window
/// toolbar drawn as a strip over the detail column and the sidebar list starting below it.
struct SplitStandIn: View {
    let store: AppStore

    var body: some View {
        HStack(spacing: 0) {
            SidebarView(store: store, selectionStyle: .drawn)
                .padding(.top, WorkspaceToolbarStrip.height)
                .frame(width: RootScenes.sidebarWidth)
            Divider()
            VStack(spacing: 0) {
                WorkspaceToolbarStrip(store: store)
                RootDetailView(store: store, chrome: .standIn)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }
}

/// A sheet panel centered over the dimmed window content.
struct SheetStandIn<Base: View, Sheet: View>: View {
    let base: Base
    let sheet: Sheet
    @Environment(\.colorScheme) private var colorScheme

    init(base: Base, @ViewBuilder sheet: () -> Sheet) {
        self.base = base
        self.sheet = sheet()
    }

    var body: some View {
        ZStack {
            base
                .allowsHitTesting(false)
            Color.black.opacity(colorScheme == .dark ? 0.32 : 0.16)
            sheet
                .fixedSize()
                .background(DS.Color.surface)
                .clipShape(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous)
                        .strokeBorder(DS.Color.separator, lineWidth: 1)
                )
                .shadow(color: .black.opacity(colorScheme == .dark ? 0.5 : 0.22), radius: 24, y: 12)
        }
    }
}
