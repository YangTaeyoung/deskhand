import DeskhandClient
import DeskhandUI
import SwiftUI

/// Quick switch scenes: the palette over the workspace of the development PC, with recent
/// transfers, a paused debug session and QA runs to offer.
@MainActor
enum QuickSwitchScenes {
    static let dev = PreviewFixtures.devPCID

    static func scene(_ name: String, query: String, selection: Int = 0,
                      recents: [String] = ["tab:terminal", "host:\(PreviewFixtures.qaLaptopID)", "command:showTransferQueue"]) -> SnapshotScene {
        SnapshotScene(name: name, size: WorkspaceScenes.size) {
            let store = WorkspaceScenes.store(WorkspaceScenes.Setup(selected: dev))
            let _ = seed(store)
            let _ = store.quickSwitch.seed(items: QuickSwitchCatalog.items(store: store), query: query, selection: selection,
                                           recents: recents)
            ZStack {
                SplitStandIn(store: store)
                QuickSwitchOverlay(store: store, chrome: .standIn)
            }
        }
    }

    /// Transfers in the development PC's queue, a paused debug session and the sample QA runs.
    static func seed(_ store: AppStore) {
        FilesScenes.seed(store, hostID: dev, FilesScenes.Files(transfers: PreviewFixtures.transfers(localHome: "/Users/taeyoung")))
        _ = DebuggerScenes.seed(store, hostID: dev, DebuggerScenes.Tab(sessions: [DebuggerScenes.paused()]))
        QAScenes.seed(store, hostID: dev, QAScenes.Tab())
    }

    static var all: [SnapshotScene] {
        [
            scene("quick-switch", query: ""),
            scene("quick-switch-search", query: "ㅌㅁㄴ"),
            scene("quick-switch-search-english", query: "stop", selection: 0),
            scene("quick-switch-empty", query: "배포 기록"),
        ]
    }
}
