import AppKit
import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

/// An item about to be transferred: its name, where it comes from and the full path it goes to.
public struct TransferCandidate: Sendable, Equatable, Hashable {
    public var name: String
    public var source: String
    public var destination: String
    public var isDirectory: Bool
    public var size: Int?
    public var modified: Date?

    public init(name: String, source: String, destination: String, isDirectory: Bool, size: Int? = nil, modified: Date? = nil) {
        self.name = name
        self.source = source
        self.destination = destination
        self.isDirectory = isDirectory
        self.size = size
        self.modified = modified
    }
}

/// A transfer whose destination already has an item of the same name.
public struct ConflictItem: Sendable, Equatable, Identifiable {
    public var candidate: TransferCandidate
    public var existing: FileItem

    public var id: String { candidate.destination }

    public init(candidate: TransferCandidate, existing: FileItem) {
        self.candidate = candidate
        self.existing = existing
    }
}

/// Conflicts waiting for the person's decision, the first one shown.
public struct ConflictPrompt: Sendable, Equatable, Identifiable {
    public let id: UUID
    public var direction: TransferDirection
    /// `이 Mac` or the computer's name: where the items come from.
    public var sourceName: String
    /// `개발 PC` or `이 Mac`: where the destination folder is.
    public var destinationName: String
    public var folder: String
    public var items: [ConflictItem]

    public init(id: UUID = UUID(), direction: TransferDirection, sourceName: String, destinationName: String, folder: String,
                items: [ConflictItem]) {
        self.id = id
        self.direction = direction
        self.sourceName = sourceName
        self.destinationName = destinationName
        self.folder = folder
        self.items = items
    }

    public var current: ConflictItem? { items.first }
}

/// Items awaiting confirmation of a permanent delete.
public struct DeletionRequest: Sendable, Equatable, Identifiable {
    public let id = UUID()
    public var items: [FileItem]
}

/// The info sheet: the item, refreshed with `files.stat` for remote items.
public struct FileInfoState: Sendable, Equatable, Identifiable {
    public var item: FileItem
    public var side: FileSide
    public var isLoading: Bool
    public var error: OperationError?

    public var id: String { item.path }
}

/// A short message at the bottom of the files tab.
public struct FilesNotice: Sendable, Equatable, Identifiable {
    public enum Tone: Sendable, Equatable {
        case info
        case success
        case error
    }

    public let id = UUID()
    public var tone: Tone
    public var message: String
    public var detail: String?
    public var code: String?
}

/// What a drag over a pane would do, for the pane's drop banner.
public struct FileDropHover: Sendable, Equatable {
    /// Folder row under the pointer; nil for the open folder.
    public var folder: FileItem?
    public var move: Bool
    public var count: Int
}

/// A menu item of a pane's context menu.
public struct FileMenuEntry {
    public var title: String
    public var symbol: String?
    /// Shown next to the title; context menus do not trigger it.
    public var key: String
    public var modifiers: NSEvent.ModifierFlags
    public var isEnabled: Bool
    public var isSeparator: Bool
    public var action: @MainActor () -> Void

    public init(_ title: String, symbol: String? = nil, key: String = "", modifiers: NSEvent.ModifierFlags = [.command],
                isEnabled: Bool = true, action: @escaping @MainActor () -> Void) {
        self.title = title
        self.symbol = symbol
        self.key = key
        self.modifiers = modifiers
        self.isEnabled = isEnabled
        self.isSeparator = false
        self.action = action
    }

    public static var separator: FileMenuEntry {
        var entry = FileMenuEntry("") {}
        entry.isSeparator = true
        return entry
    }
}

/// Whether the remote pane can list the computer's files.
public enum RemoteFilesAvailability: Equatable, Sendable {
    case ready
    case connecting
    case reconnecting
    case offline
    case blocked
    /// The agent reports the `files` capability as not ready.
    case unavailable(CapabilityState, reason: String?)

    public init(host: Host) {
        switch host.connection {
        case .offline: self = .offline
        case .blocked: self = .blocked
        case .connecting: self = .connecting
        case .reconnecting: self = .reconnecting
        case .ready, .degraded:
            if let files = host.caps.files, files.capabilityState != .ready {
                self = .unavailable(files.capabilityState, reason: files.reason)
            } else {
                self = .ready
            }
        }
    }
}

/// The files tab of one computer: the local and remote panes, the transfer queue, and every
/// file action — transfers with their conflict decisions, new folder, rename, trash, permanent
/// delete, open on the remote computer, open in the logs tab, info and copy path.
@MainActor
@Observable
public final class FilesModel {
    public let hostID: String
    public let local: FileBrowser
    public let remote: FileBrowser
    public let transfers: TransferQueue
    /// The pane the keyboard and toolbar act on.
    public var activeSide: FileSide = .remote
    public var conflict: ConflictPrompt?
    public var pendingDeletion: DeletionRequest?
    public var info: FileInfoState?
    public private(set) var notice: FilesNotice?
    public var dropHover: [FileSide: FileDropHover] = [:]
    /// Remote actions in flight, for the pane's status line.
    public private(set) var busyMessage: String?
    /// Increments to move keyboard focus to a pane's search field.
    public private(set) var searchFocusRequest: [FileSide: Int] = [:]

    @ObservationIgnored weak var store: AppStore?
    @ObservationIgnored let backend: any Backend
    @ObservationIgnored let preferences: FilesPreferences
    @ObservationIgnored private(set) var hostName: String
    @ObservationIgnored private var started = false
    @ObservationIgnored private var noticeTask: Task<Void, Never>?

    init(host: Host, backend: any Backend, preferences: FilesPreferences) {
        hostID = host.hostID
        hostName = host.name
        self.backend = backend
        self.preferences = preferences
        let remoteStyle = PathStyle(os: host.os)
        let localPath = preferences.localPath.flatMap { LocalFileSource.exists($0) ? $0 : nil } ?? LocalFileSource.home
        local = FileBrowser(side: .local, source: LocalFileSource(), path: localPath,
                            sort: preferences.sort(for: .local), showsHidden: preferences.showsHidden(.local))
        remote = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: host.hostID, style: remoteStyle),
                             path: preferences.remotePath(for: host.hostID) ?? RemoteFileSource.startPath(for: host),
                             sort: preferences.sort(for: .remote), showsHidden: preferences.showsHidden(.remote))
        transfers = TransferQueue(hostID: host.hostID, backend: backend)
        transfers.onFinish = { [weak self] transfer in self?.transferFinished(transfer) }
    }

    public func browser(_ side: FileSide) -> FileBrowser { side == .local ? local : remote }

    /// `이 Mac` or the computer's name.
    public func sideName(_ side: FileSide) -> String {
        side == .local ? .ui("files.side.local", "이 Mac") : hostName
    }

    // MARK: Lifecycle

    /// Opens both panes and follows the host's transfers. Later calls only load the remote pane
    /// when it has not loaded yet and the computer can list files.
    public func start(host: Host) async {
        hostName = host.name
        let available = RemoteFilesAvailability(host: host) == .ready
        guard !started else {
            if available { await loadRemoteIfNeeded() }
            return
        }
        started = true
        transfers.start()
        async let localLoad: Void = local.open(local.path)
        if available { await remote.open(remote.path) }
        await localLoad
    }

    /// Lists the remote folder when it has not loaded or its last load failed.
    public func loadRemoteIfNeeded() async {
        switch remote.phase {
        case .idle, .failed: await remote.open(remote.path)
        case .loading, .loaded: break
        }
    }

    func stop() {
        transfers.stop()
        noticeTask?.cancel()
    }

    /// Marks the model as started so the view does not load anything. Snapshot scenes call it
    /// after seeding the browsers and queue.
    public func markSeeded() {
        started = true
    }

    // MARK: Navigation

    public func open(_ side: FileSide, path: String) async {
        let browser = browser(side)
        await browser.open(path)
        remember(side)
    }

    public func goUp(_ side: FileSide) async {
        await browser(side).goUp()
        remember(side)
    }

    public func goBack(_ side: FileSide) async {
        await browser(side).goBack()
        remember(side)
    }

    public func goForward(_ side: FileSide) async {
        await browser(side).goForward()
        remember(side)
    }

    public func reload(_ side: FileSide) async {
        await browser(side).reload()
    }

    /// Opens a folder row; other rows do nothing.
    public func activate(_ item: FileItem, side: FileSide) async {
        guard item.isFolder else { return }
        await open(side, path: item.path)
    }

    public func setSort(_ sort: FileSort, for side: FileSide) {
        browser(side).sort = sort
        preferences.setSort(sort, for: side)
    }

    public func setShowsHidden(_ shows: Bool, for side: FileSide) {
        browser(side).showsHidden = shows
        preferences.setShowsHidden(shows, for: side)
    }

    public func focusSearch(_ side: FileSide) {
        searchFocusRequest[side, default: 0] += 1
    }

    private func remember(_ side: FileSide) {
        let browser = browser(side)
        guard browser.phase == .loaded else { return }
        switch side {
        case .local: preferences.localPath = browser.path
        case .remote: preferences.setRemotePath(browser.path, for: hostID)
        }
    }

    // MARK: Drops and transfers

    /// Whether `payload` can drop on `side`, into `folder` or the open folder: files of this Mac
    /// go to the remote pane, remote rows go to the local pane.
    public func canAccept(_ payload: FileDropPayload, on side: FileSide) -> Bool {
        switch (payload, side) {
        case (.local(let urls), .remote):
            return !urls.isEmpty && remote.phase == .loaded
        case (.remote(let items), .local):
            return !items.isEmpty && items.allSatisfy { $0.hostID == hostID } && local.phase == .loaded
        default:
            return false
        }
    }

    /// Starts the transfers a drop asks for. Returns false when the pane does not take the payload.
    @discardableResult
    public func handleDrop(_ payload: FileDropPayload, on side: FileSide, into folder: FileItem?, move: Bool) async -> Bool {
        guard canAccept(payload, on: side) else { return false }
        dropHover[side] = nil
        switch payload {
        case .local(let urls):
            await send(urls, to: folder?.path ?? remote.path, move: move)
        case .remote(let items):
            await receive(items, to: folder?.path ?? local.path, move: move)
        }
        return true
    }

    /// Pushes files of this Mac into a remote folder.
    public func send(_ urls: [URL], to folder: String, move: Bool) async {
        let candidates = urls.map { url -> TransferCandidate in
            let item = LocalFileSource.item(for: url)
            return TransferCandidate(name: item.name, source: url.path,
                                     destination: FilePath.join(folder, item.name, style: remote.style),
                                     isDirectory: item.isDirectory, size: item.size, modified: item.modified)
        }
        await begin(candidates, direction: .upload(move: move), folder: folder)
    }

    /// Pulls remote items into a folder of this Mac.
    public func receive(_ items: [RemoteDragItem], to folder: String, move: Bool) async {
        let candidates = items.map { item in
            TransferCandidate(name: item.name, source: item.path, destination: FilePath.join(folder, item.name, style: .posix),
                              isDirectory: item.dir, size: item.size, modified: item.modified)
        }
        await begin(candidates, direction: .download(move: move), folder: folder)
    }

    /// Sends the local selection to the open remote folder.
    public func sendSelection(move: Bool) async {
        let urls = local.selectedItems.map { URL(fileURLWithPath: $0.path) }
        guard !urls.isEmpty, remote.phase == .loaded else { return }
        await send(urls, to: remote.path, move: move)
    }

    /// Receives the remote selection into the open local folder.
    public func receiveSelection(move: Bool) async {
        let items = remote.selectedItems.map { RemoteDragItem(hostID: hostID, item: $0) }
        guard !items.isEmpty else { return }
        await receive(items, to: local.path, move: move)
    }

    /// Starts transfers whose destinations are free with the `fail` policy and asks about the
    /// others, or starts every transfer with the preferred policy when it is not `fail`.
    private func begin(_ candidates: [TransferCandidate], direction: TransferDirection, folder: String) async {
        guard !candidates.isEmpty else { return }
        let policy = preferences.conflictPolicy
        if policy != .fail {
            for candidate in candidates { await start(candidate, direction: direction, policy: policy) }
            return
        }
        let existing = await existingItems(for: candidates, direction: direction, folder: folder)
        var conflicts: [ConflictItem] = []
        for candidate in candidates {
            if let item = existing[candidate.destination] {
                conflicts.append(ConflictItem(candidate: candidate, existing: item))
            } else {
                await start(candidate, direction: direction, policy: .fail)
            }
        }
        guard !conflicts.isEmpty else { return }
        if var prompt = conflict, prompt.direction == direction, prompt.folder == folder {
            prompt.items.append(contentsOf: conflicts)
            conflict = prompt
        } else {
            conflict = ConflictPrompt(direction: direction, sourceName: sideName(direction.isUpload ? .local : .remote),
                                      destinationName: sideName(direction.isUpload ? .remote : .local),
                                      folder: folder, items: conflicts)
        }
    }

    /// Items already at the candidates' destinations, by destination path.
    private func existingItems(for candidates: [TransferCandidate], direction: TransferDirection, folder: String) async -> [String: FileItem] {
        var found: [String: FileItem] = [:]
        if direction.isUpload {
            let listed = remote.isComplete && remote.style.sameName(FilePath.normalized(folder, style: remote.style), remote.path)
            for candidate in candidates {
                if listed {
                    if let item = remote.item(named: candidate.name) { found[candidate.destination] = item }
                } else if let entry = try? await backend.filesStat(hostID: hostID, path: candidate.destination) {
                    found[candidate.destination] = FileItem(entry: entry, style: remote.style)
                }
            }
        } else {
            for candidate in candidates where LocalFileSource.exists(candidate.destination) {
                found[candidate.destination] = LocalFileSource.item(for: URL(fileURLWithPath: candidate.destination))
            }
        }
        return found
    }

    private func start(_ candidate: TransferCandidate, direction: TransferDirection, policy: ConflictPolicy) async {
        let request = TransferRequest(
            hostID: hostID, direction: direction,
            local: direction.isUpload ? candidate.source : candidate.destination,
            remote: direction.isUpload ? candidate.destination : candidate.source,
            onConflict: policy)
        do {
            let id = try await backend.transferStart(request)
            preferences.queueExpanded = true
            await transfers.track(id)
        } catch {
            let name = candidate.name
            show(.error, .ui("files.error.transferStart", "‘\(name)’ 전송을 시작하지 못했습니다"), error: OperationError(error))
        }
    }

    /// Applies the person's decision to the shown conflict, or with `applyToAll` to every
    /// remaining one. `fail` stops: no remaining conflicting item is transferred.
    public func resolveConflict(_ policy: ConflictPolicy, applyToAll: Bool) async {
        guard var prompt = conflict, !prompt.items.isEmpty else { return }
        if policy == .fail {
            let count = FileFormat.count(prompt.items.count)
            conflict = nil
            show(.info, .ui("files.conflict.stopped", "같은 이름이 있는 \(count)개 항목을 보내지 않았습니다"))
            return
        }
        let targets = applyToAll ? prompt.items : [prompt.items[0]]
        prompt.items.removeFirst(targets.count)
        conflict = prompt.items.isEmpty ? nil : prompt
        for item in targets {
            await start(item.candidate, direction: prompt.direction, policy: policy)
        }
    }

    /// Reloads the panes a finished transfer changed.
    private func transferFinished(_ transfer: Transfer) {
        let presentation = TransferPresentation(transfer, hostName: hostName)
        let direction = presentation.direction
        let remoteFolder = direction.isUpload ? presentation.destinationFolder : presentation.sourceFolder
        let localFolder = direction.isUpload ? presentation.sourceFolder : presentation.destinationFolder
        let touchesRemote = direction.isUpload || direction == .movePull
        let touchesLocal = !direction.isUpload || direction == .movePush
        Task {
            if touchesRemote, remote.phase == .loaded, remote.style.sameName(FilePath.normalized(remoteFolder, style: remote.style), remote.path) {
                await remote.reload()
            }
            if touchesLocal, local.phase == .loaded, FilePath.normalized(localFolder, style: .posix) == local.path {
                await local.reload()
            }
        }
    }

    /// Shows a transfer's destination or source.
    public func showLocation(_ action: TransferAction, for transfer: Transfer) async {
        let presentation = TransferPresentation(transfer, hostName: hostName)
        let wantsDestination = action == .showDestination
        let onRemote = presentation.direction.isUpload == wantsDestination
        let path = wantsDestination ? presentation.destinationPath : presentation.sourcePath
        let folder = wantsDestination ? presentation.destinationFolder : presentation.sourceFolder
        if onRemote {
            await open(.remote, path: folder)
            remote.selection = [path]
            activeSide = .remote
        } else if LocalFileSource.exists(path) {
            NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
        } else {
            await open(.local, path: folder)
            activeSide = .local
        }
    }

    // MARK: Remote actions

    /// Creates `새 폴더` (or the first free variant) in the open remote folder and starts renaming it.
    public func newFolder() async {
        guard remote.phase == .loaded else { return }
        let name = FileNameRule.freeName(.ui("files.newFolderName", "새 폴더"), in: remote.items.map(\.name), style: remote.style)
        let path = FilePath.join(remote.path, name, style: remote.style)
        busyMessage = .ui("files.busy.mkdir", "폴더를 만드는 중…")
        defer { busyMessage = nil }
        do {
            try await backend.filesMkdir(hostID: hostID, path: path)
            await remote.reload()
            if let created = remote.item(named: name) {
                remote.selection = [created.path]
                remote.renameRequest = created.path
            }
        } catch {
            show(.error, .ui("files.error.mkdir", "‘\(name)’ 폴더를 만들지 못했습니다"), error: OperationError(error))
        }
    }

    /// Why `name` cannot replace `item`'s name, or nil.
    public func renameProblem(_ item: FileItem, to name: String) -> String? {
        if let problem = FileNameRule.problem(name, style: remote.style) { return problem }
        if name != item.name, let other = remote.item(named: name), other.path != item.path {
            let existing = other.name
            return .ui("name.taken", "‘\(existing)’ 이름의 항목이 이미 있습니다.")
        }
        return nil
    }

    public func rename(_ item: FileItem, to name: String) async {
        guard name != item.name else { return }
        if let problem = renameProblem(item, to: name) {
            show(.error, problem)
            return
        }
        let target = FilePath.join(FilePath.parent(item.path, style: remote.style) ?? remote.path, name, style: remote.style)
        busyMessage = .ui("files.busy.rename", "이름을 바꾸는 중…")
        defer { busyMessage = nil }
        do {
            try await backend.filesRename(hostID: hostID, from: item.path, to: target)
            await remote.reload()
            if let renamed = remote.item(named: name) { remote.selection = [renamed.path] }
        } catch {
            let old = item.name
            show(.error, .ui("files.error.rename", "‘\(old)’의 이름을 바꾸지 못했습니다"), error: OperationError(error))
        }
    }

    /// Moves remote items to the remote computer's recycle bin.
    public func trash(_ items: [FileItem]) async {
        guard !items.isEmpty else { return }
        busyMessage = .ui("files.busy.trash", "휴지통으로 옮기는 중…")
        defer { busyMessage = nil }
        var failures: [(FileItem, OperationError)] = []
        for item in items {
            do {
                try await backend.filesTrash(hostID: hostID, path: item.path)
            } catch {
                failures.append((item, OperationError(error)))
            }
        }
        await remote.reload()
        report(items: items, failures: failures,
               success: { count in .ui("files.trashed", "\(count)개 항목을 휴지통으로 옮겼습니다") },
               failure: { name in .ui("files.error.trash", "‘\(name)’을(를) 휴지통으로 옮기지 못했습니다") })
    }

    /// Asks to confirm a permanent delete of remote items.
    public func requestPermanentDelete(_ items: [FileItem]) {
        guard !items.isEmpty else { return }
        pendingDeletion = DeletionRequest(items: items)
    }

    /// Deletes the items of a confirmed request permanently.
    public func confirmPermanentDelete(_ request: DeletionRequest) async {
        if pendingDeletion?.id == request.id { pendingDeletion = nil }
        busyMessage = .ui("files.busy.delete", "영구 삭제하는 중…")
        defer { busyMessage = nil }
        var failures: [(FileItem, OperationError)] = []
        for item in request.items {
            do {
                try await backend.filesDelete(hostID: hostID, path: item.path)
            } catch {
                failures.append((item, OperationError(error)))
            }
        }
        await remote.reload()
        report(items: request.items, failures: failures,
               success: { count in .ui("files.deleted", "\(count)개 항목을 영구 삭제했습니다") },
               failure: { name in .ui("files.error.delete", "‘\(name)’을(를) 삭제하지 못했습니다") })
    }

    private func report(items: [FileItem], failures: [(FileItem, OperationError)],
                        success: (String) -> String, failure: (String) -> String) {
        if let (item, error) = failures.first {
            var message = failure(item.name)
            if failures.count > 1 {
                let more = FileFormat.count(failures.count - 1)
                message = .ui("files.error.more", "\(message) 외 \(more)개")
            }
            show(.error, message, error: error)
        } else {
            show(.success, success(FileFormat.count(items.count)))
        }
    }

    /// Starts a remote file with `app.launch`.
    public func launch(_ item: FileItem) async {
        let name = item.name
        do {
            try await backend.appLaunch(hostID: hostID, path: item.path)
            let host = hostName
            show(.success, .ui("files.launched", "\(host)에서 ‘\(name)’을(를) 열었습니다"))
        } catch {
            show(.error, .ui("files.error.launch", "‘\(name)’을(를) 열지 못했습니다"), error: OperationError(error))
        }
    }

    /// Switches to the logs tab with the remote file selected.
    public func openLog(_ item: FileItem) {
        store?.openLog(hostID: hostID, path: item.path)
    }

    public func showInfo(_ item: FileItem, side: FileSide) {
        info = FileInfoState(item: item, side: side, isLoading: side == .remote)
        guard side == .remote else { return }
        Task {
            do {
                let entry = try await backend.filesStat(hostID: hostID, path: item.path)
                guard info?.item.path == item.path else { return }
                var fresh = FileItem(entry: entry, style: remote.style)
                fresh.name = item.name
                info = FileInfoState(item: fresh, side: side, isLoading: false)
            } catch {
                guard info?.item.path == item.path else { return }
                info?.isLoading = false
                info?.error = OperationError(error)
            }
        }
    }

    public func copyPaths(_ items: [FileItem]) {
        guard !items.isEmpty else { return }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(items.map(\.path).joined(separator: "\n"), forType: .string)
        if items.count == 1 {
            show(.success, .ui("files.copiedPath", "경로를 복사했습니다"))
        } else {
            let count = FileFormat.count(items.count)
            show(.success, .ui("files.copiedPaths", "경로 \(count)개를 복사했습니다"))
        }
    }

    public func revealInFinder(_ items: [FileItem]) {
        let urls = items.map { URL(fileURLWithPath: $0.path) }
        guard !urls.isEmpty else { return }
        NSWorkspace.shared.activateFileViewerSelecting(urls)
    }

    // MARK: Notices

    public func show(_ tone: FilesNotice.Tone, _ message: String, error: OperationError? = nil) {
        noticeTask?.cancel()
        let notice = FilesNotice(tone: tone, message: message, detail: error.map(\.detail), code: error?.code)
        self.notice = notice
        let seconds: Double = tone == .error ? 8 : 3.5
        noticeTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(seconds))
            guard !Task.isCancelled, self?.notice?.id == notice.id else { return }
            self?.notice = nil
        }
    }

    public func dismissNotice() {
        noticeTask?.cancel()
        notice = nil
    }

    // MARK: Menus

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
        entries += [
            FileMenuEntry(.ui("files.menu.send", "원격 PC로 보내기"), symbol: "square.and.arrow.up", isEnabled: remoteReady) { [weak self] in
                Task { await self?.send(targets.map { URL(fileURLWithPath: $0.path) }, to: self?.remote.path ?? "", move: false) }
            },
            FileMenuEntry(.ui("files.menu.sendMove", "원격 PC로 이동…"), symbol: "arrow.right.circle", isEnabled: remoteReady) { [weak self] in
                Task { await self?.send(targets.map { URL(fileURLWithPath: $0.path) }, to: self?.remote.path ?? "", move: true) }
            },
            .separator,
            FileMenuEntry(.ui("files.menu.showInFinder", "Finder에서 보기"), symbol: "folder") { [weak self] in
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
                Task { await self.receive(targets.map { RemoteDragItem(hostID: self.hostID, item: $0) }, to: self.local.path, move: false) }
            },
            FileMenuEntry(.ui("files.menu.receiveMove", "이 Mac으로 이동…"), symbol: "arrow.left.circle", isEnabled: local.phase == .loaded) { [weak self] in
                guard let self else { return }
                Task { await self.receive(targets.map { RemoteDragItem(hostID: self.hostID, item: $0) }, to: self.local.path, move: true) }
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

/// A remote row being dragged, as written to the pasteboard.
public struct RemoteDragItem: Codable, Sendable, Equatable {
    public var hostID: String
    public var path: String
    public var name: String
    public var dir: Bool
    public var size: Int?
    public var modified: Date?

    public init(hostID: String, path: String, name: String, dir: Bool, size: Int? = nil, modified: Date? = nil) {
        self.hostID = hostID
        self.path = path
        self.name = name
        self.dir = dir
        self.size = size
        self.modified = modified
    }

    public init(hostID: String, item: FileItem) {
        self.init(hostID: hostID, path: item.path, name: item.name, dir: item.isDirectory, size: item.size, modified: item.modified)
    }
}

extension NSPasteboard.PasteboardType {
    /// A remote file or folder row of the files tab (`RemoteDragItem` JSON).
    public static let deskhandRemoteItem = NSPasteboard.PasteboardType("io.github.yangtaeyoung.Deskhand.remote-item")
}

/// What a drag carries into a pane.
public enum FileDropPayload: Sendable, Equatable {
    /// Files and folders of this Mac, from the local pane or Finder.
    case local([URL])
    /// Rows of a remote pane.
    case remote([RemoteDragItem])

    public init?(pasteboard: NSPasteboard) {
        let decoder = JSONDecoder()
        let remote = (pasteboard.pasteboardItems ?? []).compactMap { item -> RemoteDragItem? in
            guard let data = item.data(forType: .deskhandRemoteItem) else { return nil }
            return try? decoder.decode(RemoteDragItem.self, from: data)
        }
        if !remote.isEmpty {
            self = .remote(remote)
            return
        }
        let urls = pasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
        guard !urls.isEmpty else { return nil }
        self = .local(urls)
    }

    public var count: Int {
        switch self {
        case .local(let urls): urls.count
        case .remote(let items): items.count
        }
    }
}
