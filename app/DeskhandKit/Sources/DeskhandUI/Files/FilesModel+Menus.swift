import AppKit
import Foundation

extension FilesModel {
    /// Context menu entries for `targets` in `side`; an empty `targets` is the open folder's menu.
    public func menu(for targets: [FileItem], side: FileSide) -> [FileMenuEntry] {
        side == .local ? localMenu(targets) : remoteMenu(targets)
    }

    private func localMenu(_ targets: [FileItem]) -> [FileMenuEntry] {
        let remoteReady = remote.phase == .loaded
        if targets.isEmpty {
            return [
                FileMenuEntry(.ui("files.menu.showInFinder", "Finder에서 보기"), symbol: "folder") { [weak self] in
                    guard let self else { return }
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: self.local.path)])
                },
                FileMenuEntry(.ui("files.menu.copyFolderPath", "이 폴더 경로 복사"), symbol: "doc.on.doc") { [weak self] in
                    guard let self else { return }
                    self.copyPaths([FileItem(name: "", path: self.local.path, isDirectory: true)])
                },
                .separator,
                FileMenuEntry(.ui("files.menu.refresh", "새로 고침"), symbol: "arrow.clockwise", key: "r") { [weak self] in
                    Task { await self?.reload(.local) }
                },
            ]
        }
        var entries: [FileMenuEntry] = []
        if targets.count == 1, let folder = targets.first, folder.isFolder {
            entries.append(FileMenuEntry(.ui("files.menu.open", "열기"), symbol: "folder", key: "↓") { [weak self] in
                Task { await self?.activate(folder, side: .local) }
            })
            entries.append(.separator)
        }
        let onDisk = targets.allSatisfy { !$0.isPreviewOnly }
        entries += [
            FileMenuEntry(.ui("files.menu.send", "원격 PC로 보내기"), symbol: "square.and.arrow.up", isEnabled: remoteReady && onDisk) { [weak self] in
                guard let self else { return }
                Task { await self.requestSend(targets.map { URL(fileURLWithPath: $0.path) }, to: self.remote.path, move: false) }
            },
            FileMenuEntry(.ui("files.menu.sendMove", "원격 PC로 이동…"), symbol: "arrow.right.circle", isEnabled: remoteReady && onDisk) { [weak self] in
                guard let self else { return }
                Task { await self.requestSend(targets.map { URL(fileURLWithPath: $0.path) }, to: self.remote.path, move: true) }
            },
            .separator,
            FileMenuEntry(.ui("files.menu.showInFinder", "Finder에서 보기"), symbol: "folder", isEnabled: onDisk) { [weak self] in
                self?.revealInFinder(targets)
            },
            FileMenuEntry(.ui("files.menu.copyPath", "경로 복사"), symbol: "doc.on.doc", key: "c", modifiers: [.command, .option]) { [weak self] in
                self?.copyPaths(targets)
            },
        ]
        if targets.count == 1, let item = targets.first {
            entries.append(FileMenuEntry(.ui("files.menu.info", "정보"), symbol: "info.circle", key: "i") { [weak self] in
                self?.showInfo(item, side: .local)
            })
        }
        return entries
    }

    private func remoteMenu(_ targets: [FileItem]) -> [FileMenuEntry] {
        let ready = remote.phase == .loaded
        if targets.isEmpty {
            return [
                FileMenuEntry(.ui("files.menu.newFolder", "새 폴더"), symbol: "folder.badge.plus", key: "n",
                              modifiers: [.command, .shift], isEnabled: ready) { [weak self] in
                    Task { await self?.newFolder() }
                },
                .separator,
                FileMenuEntry(.ui("files.menu.copyFolderPath", "이 폴더 경로 복사"), symbol: "doc.on.doc") { [weak self] in
                    guard let self else { return }
                    self.copyPaths([FileItem(name: "", path: self.remote.path, isDirectory: true)])
                },
                FileMenuEntry(.ui("files.menu.refresh", "새로 고침"), symbol: "arrow.clockwise", key: "r") { [weak self] in
                    Task { await self?.reload(.remote) }
                },
            ]
        }
        let single = targets.count == 1 ? targets.first : nil
        var entries: [FileMenuEntry] = []
        if let folder = single, folder.isFolder {
            entries.append(FileMenuEntry(.ui("files.menu.open", "열기"), symbol: "folder", key: "↓") { [weak self] in
                Task { await self?.activate(folder, side: .remote) }
            })
        }
        if let file = single, !file.isDirectory {
            entries.append(FileMenuEntry(.ui("files.menu.launch", "원격에서 열기"), symbol: "arrow.up.forward.app") { [weak self] in
                Task { await self?.launch(file) }
            })
            entries.append(FileMenuEntry(.ui("files.menu.openLog", "로그로 열기"), symbol: "doc.text.magnifyingglass",
                                         isEnabled: file.fileKind.isReadableAsLog) { [weak self] in
                self?.openLog(file)
            })
        }
        if !entries.isEmpty { entries.append(.separator) }
        entries += [
            FileMenuEntry(.ui("files.menu.receive", "이 Mac으로 받기"), symbol: "square.and.arrow.down", isEnabled: local.phase == .loaded) { [weak self] in
                guard let self else { return }
                Task { await self.requestReceive(targets.map { RemoteDragItem(hostID: self.hostID, item: $0) }, to: self.local.path, move: false) }
            },
            FileMenuEntry(.ui("files.menu.receiveMove", "이 Mac으로 이동…"), symbol: "arrow.left.circle", isEnabled: local.phase == .loaded) { [weak self] in
                guard let self else { return }
                Task { await self.requestReceive(targets.map { RemoteDragItem(hostID: self.hostID, item: $0) }, to: self.local.path, move: true) }
            },
            .separator,
            FileMenuEntry(.ui("files.menu.newFolder", "새 폴더"), symbol: "folder.badge.plus", key: "n",
                          modifiers: [.command, .shift], isEnabled: ready) { [weak self] in
                Task { await self?.newFolder() }
            },
        ]
        if let item = single {
            entries.append(FileMenuEntry(.ui("files.menu.rename", "이름 바꾸기"), symbol: "pencil", key: "↩", modifiers: []) { [weak self] in
                self?.remote.renameRequest = item.path
            })
        }
        entries += [
            FileMenuEntry(.ui("files.menu.trash", "휴지통으로 이동"), symbol: "trash", key: "⌫") { [weak self] in
                Task { await self?.trash(targets) }
            },
            FileMenuEntry(.ui("files.menu.delete", "영구 삭제…"), symbol: "trash.slash", key: "⌫", modifiers: [.command, .option]) { [weak self] in
                self?.requestPermanentDelete(targets)
            },
            .separator,
            FileMenuEntry(.ui("files.menu.copyPath", "경로 복사"), symbol: "doc.on.doc", key: "c", modifiers: [.command, .option]) { [weak self] in
                self?.copyPaths(targets)
            },
        ]
        if let item = single {
            entries.append(FileMenuEntry(.ui("files.menu.info", "정보"), symbol: "info.circle", key: "i") { [weak self] in
                self?.showInfo(item, side: .remote)
            })
        }
        return entries
    }
}
