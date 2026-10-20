import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Files tab scenes at 1440×900. Both panes and the queue are seeded from fixtures; nothing is
/// read from this Mac's disk or the backend.
@MainActor
enum FilesScenes {
    typealias Setup = WorkspaceScenes.Setup

    struct Files {
        var localPath = PreviewFixtures.localBuildFolder
        var localItems = PreviewFixtures.localItems()
        var localSelection: Set<String> = []
        var remotePath = PreviewFixtures.remoteHome(user: "taeyoung") + "\\Downloads"
        var remoteItems: [FileItem]?
        var remoteSelection: Set<String> = []
        var remotePhase: FileBrowser.Phase = .loaded
        var remoteBack: [String] = []
        var active: FileSide = .remote
        var transfers: [Transfer] = []
        var expanded: Set<String> = []
        var queueExpanded = false
        var queueHeight: Double = 232
        var hidden: [FileSide: Set<FileColumn>] = [:]
    }

    static func remoteItems(_ path: String, user: String = "taeyoung") -> [FileItem] {
        let folders = PreviewFixtures.remoteFolders(user: user)
        return (folders[path] ?? []).map { FileItem(entry: $0, style: .windows) }
    }

    /// Seeds the files tab of `hostID` with `files` and resets the files preferences.
    static func seed(_ store: AppStore, hostID: String, _ files: Files = Files()) {
        guard let host = store.host(hostID) else { return }
        let preferences = store.filesPreferences
        preferences.conflictPolicy = .fail
        preferences.queueExpanded = files.queueExpanded
        preferences.queueHeight = files.queueHeight
        preferences.splitFraction = 0.5
        for side in FileSide.allCases {
            preferences.setSort(.standard, for: side)
            preferences.setShowsHidden(false, for: side)
            for column in FileColumn.allCases {
                preferences.setColumn(column, hidden: files.hidden[side]?.contains(column) ?? false, side: side)
            }
        }
        let model = store.files(for: host)
        model.local.seed(path: files.localPath, items: files.localItems,
                         selection: Set(files.localSelection.map { files.localPath + "/" + $0 }))
        let remoteItems = files.remoteItems ?? remoteItems(files.remotePath)
        model.remote.seed(path: files.remotePath, items: remoteItems,
                          selection: Set(files.remoteSelection.map { files.remotePath + "\\" + $0 }),
                          phase: files.remotePhase, backStack: files.remoteBack)
        model.transfers.seed(files.transfers, expanded: files.expanded)
        model.activeSide = files.active
        model.markSeeded()
    }

    static func scene(_ name: String, _ setup: Setup, _ files: Files, sheet: ((AppStore) -> AnyView)? = nil) -> SnapshotScene {
        SnapshotScene(name: name, size: WorkspaceScenes.size) {
            let store = WorkspaceScenes.store(setup)
            let _ = seed(store, hostID: setup.selected, files)
            if let sheet {
                SheetStandIn(base: SplitStandIn(store: store)) { sheet(store) }
            } else {
                SplitStandIn(store: store)
            }
        }
    }

    static var all: [SnapshotScene] {
        let dev = PreviewFixtures.devPCID
        let user = "taeyoung"
        let home = PreviewFixtures.remoteHome(user: user)
        let fixtures = PreviewFixtures.transfers(localHome: "/Users/taeyoung")
        let running = fixtures.filter { $0.transferID == "t-preview-running" || $0.transferID == "t-preview-done" }
        let longFolder = PreviewFixtures.longNamesFolder(user: user)
        return [
            scene("files", Setup(selected: dev, tab: .files, inspector: nil), Files(
                localSelection: ["Shop-Setup-1188-x64.msi"],
                remoteSelection: ["Deskhand-Setup-0.4.2.msi", "report.html"],
                remoteBack: [home], transfers: running)),
            scene("files-long-names", Setup(selected: dev, tab: .files), Files(
                localPath: "/Users/taeyoung/Documents/고객사 전달 자료/2026년 하반기 결제 시스템 개편 — 최종 산출물 (검토 완료본)",
                localItems: PreviewFixtures.localLongItems(),
                remotePath: longFolder,
                remoteSelection: [PreviewFixtures.longFileName],
                remoteBack: [home + "\\Documents"])),
            scene("files-transfers", Setup(selected: dev, tab: .files, inspector: nil), Files(
                transfers: [fixtures[0], PreviewFixtures.verifyingTransfer()] + fixtures.dropFirst(),
                queueExpanded: true, queueHeight: 600)),
            scene("files-transfer-files", Setup(selected: dev, tab: .files, inspector: nil), Files(
                transfers: fixtures.filter { $0.transferID == "t-preview-partial" } + fixtures.filter { $0.transferID != "t-preview-partial" },
                expanded: ["t-preview-partial"], queueExpanded: true, queueHeight: 600)),
            scene("files-conflict", Setup(selected: dev, tab: .files, inspector: nil), Files(transfers: running), sheet: { store in
                AnyView(ConflictSheet(prompt: conflictPrompt(store: store, dev: dev, home: home)) { _, _ in })
            }),
            scene("files-empty", Setup(selected: dev, tab: .files, inspector: nil), Files(
                localPath: "/Users/taeyoung/Desktop/새 빌드", localItems: [],
                remotePath: home + "\\Downloads\\빈 폴더", remoteItems: [], remoteBack: [home + "\\Downloads"],
                active: .remote, transfers: [], queueExpanded: true, queueHeight: 180)),
            scene("files-error", Setup(selected: dev, tab: .files, inspector: nil), Files(
                localSelection: ["release-notes.md"],
                remotePath: PreviewFixtures.deniedFolder, remoteItems: [],
                remotePhase: .failed(OperationError(code: "PERMISSION_DENIED",
                                                    message: PreviewFixtures.deniedFolders[PreviewFixtures.deniedFolder] ?? "")),
                remoteBack: ["C:\\Windows\\System32"],
                transfers: fixtures.filter { $0.transferID == "t-preview-failed" || $0.transferID == "t-preview-running" })),
        ]
    }

    private static func conflictPrompt(store: AppStore, dev: String, home: String) -> ConflictPrompt {
        let downloads = home + "\\Downloads"
        let existing = remoteItems(downloads)
        func item(_ name: String) -> FileItem {
            existing.first { $0.name == name } ?? FileItem(name: name, path: downloads + "\\" + name, isDirectory: false)
        }
        let now = Date.now
        let local = PreviewFixtures.localBuildFolder
        return ConflictPrompt(
            direction: .push, sourceName: "이 Mac", destinationName: store.host(dev)?.name ?? "개발 PC", folder: downloads,
            items: [
                ConflictItem(candidate: TransferCandidate(name: "report.html", source: local + "/report.html",
                                                          destination: downloads + "\\report.html", isDirectory: false,
                                                          size: 52_118, modified: now.addingTimeInterval(-600)),
                             existing: item("report.html")),
                ConflictItem(candidate: TransferCandidate(name: "Deskhand-Setup-0.4.2.msi", source: local + "/Deskhand-Setup-0.4.2.msi",
                                                          destination: downloads + "\\Deskhand-Setup-0.4.2.msi", isDirectory: false,
                                                          size: 84_301_568, modified: now.addingTimeInterval(-7200)),
                             existing: item("Deskhand-Setup-0.4.2.msi")),
                ConflictItem(candidate: TransferCandidate(name: "로그 모음", source: local + "/로그 모음",
                                                          destination: downloads + "\\로그 모음", isDirectory: true,
                                                          modified: now.addingTimeInterval(-3600)),
                             existing: item("로그 모음")),
            ])
    }
}
