import DeskhandClient
import DeskhandUI
import SwiftUI

@MainActor
enum RDPScenes {
    static var all: [SnapshotScene] {
        [SnapshotScene(name: "rdp-login", size: CGSize(width: 960, height: 640)) {
            let store = WorkspaceScenes.store(.init(selected: PreviewFixtures.devPCID))
            let host = store.hosts.first { $0.hostID == PreviewFixtures.devPCID }!
            RDPWorkspaceView(store: store, host: host, standIn: RDPEndpoint(
                enabled: true, port: 3389, certificateSHA256: String(repeating: "A", count: 64),
                sessionID: 1, user: "DEVBOX\\developer", sid: "S-1-preview", protocolKind: "console",
                connectState: "disconnected", locked: true))
        }]
    }
}
