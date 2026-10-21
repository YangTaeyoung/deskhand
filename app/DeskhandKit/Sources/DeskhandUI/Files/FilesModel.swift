import AppKit
import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

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
    /// Conflicts waiting for a decision, one prompt per transfer request, oldest first.
    public private(set) var conflicts: [ConflictPrompt] = []
    public var pendingMove: MoveRequest?
    public var pendingDeletion: DeletionRequest?
    public var info: FileInfoState?
    public private(set) var notice: FilesNotice?
    /// Failures shown inline at the top of a pane until dismissed or the next success there.
    public private(set) var paneErrors: [FileSide: FilesNotice] = [:]
    public var dropHover: [FileSide: FileDropHover] = [:]
    /// Remote actions in flight, for the pane's status line.
    public private(set) var busyMessage: String?
    /// Increments to move keyboard focus to a pane's search field.
    public private(set) var searchFocusRequest: [FileSide: Int] = [:]

    @ObservationIgnored weak var store: AppStore?
    @ObservationIgnored let backend: any Backend
    @ObservationIgnored let preferences: FilesPreferences
    /// Items the preview daemon pulled to this Mac, when the backend keeps them in memory.
    @ObservationIgnored let overlay: LocalOverlay?
    @ObservationIgnored private(set) var hostName: String
    @ObservationIgnored private var started = false
    @ObservationIgnored private var wasReady: Bool?
    @ObservationIgnored private var noticeTask: Task<Void, Never>?

    init(host: Host, backend: any Backend, preferences: FilesPreferences) {
        hostID = host.hostID
        hostName = host.name
        self.backend = backend
        self.preferences = preferences
        let remoteStyle = PathStyle(os: host.os)
        let localPath = preferences.localPath.flatMap { LocalFileSource.exists($0) ? $0 : nil } ?? LocalFileSource.home
        overlay = (backend as? any LocalOverlayProviding)?.localOverlay
        local = FileBrowser(side: .local, source: LocalFileSource(overlay: overlay), path: localPath,
                            sort: preferences.sort(for: .local), showsHidden: preferences.showsHidden(.local))
        remote = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: host.hostID, style: remoteStyle),
                             path: preferences.remotePath(for: host.hostID) ?? RemoteFileSource.startPath(for: host),
                             sort: preferences.sort(for: .remote), showsHidden: preferences.showsHidden(.remote))
        transfers = TransferQueue(hostID: host.hostID, backend: backend)
        transfers.retention = { [weak preferences] in preferences?.finishedTransferRetention ?? .month }
        transfers.onFinish = { [weak self] transfer in self?.transferFinished(transfer) }
    }

    public func browser(_ side: FileSide) -> FileBrowser { side == .local ? local : remote }

    /// The shown conflict prompt.
    public var conflict: ConflictPrompt? { conflicts.first }

    /// Puts conflict prompts in place without a transfer request. Snapshot scenes use it.
    public func seedConflicts(_ prompts: [ConflictPrompt]) {
        conflicts = prompts
    }

    /// The item at `path` on this Mac's disk or among the preview's received items.
    public func localItem(at path: String) -> FileItem? {
        (local.source as? LocalFileSource)?.item(at: path)
    }

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
        let returned = available && wasReady == false
        wasReady = available
        guard !started else {
            if returned, remote.phase == .loaded {
                await remote.reload()
            } else if available {
                await loadRemoteIfNeeded()
            }
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
        await begin(uploads(urls, to: folder), direction: .upload(move: move), folder: folder)
    }

    /// Pulls remote items into a folder of this Mac.
    public func receive(_ items: [RemoteDragItem], to folder: String, move: Bool) async {
        await begin(downloads(items, to: folder), direction: .download(move: move), folder: folder)
    }

    /// Sends from a menu or the toolbar: a move waits for confirmation unless the person turned
    /// the confirmation off.
    public func requestSend(_ urls: [URL], to folder: String, move: Bool) async {
        guard move, preferences.confirmMoves else { return await send(urls, to: folder, move: move) }
        pendingMove = MoveRequest(direction: .movePush, candidates: uploads(urls, to: folder), folder: folder,
                                  sourceName: sideName(.local), destinationName: sideName(.remote))
    }

    /// Receives from a menu or the toolbar, confirming a move as `requestSend` does.
    public func requestReceive(_ items: [RemoteDragItem], to folder: String, move: Bool) async {
        guard move, preferences.confirmMoves else { return await receive(items, to: folder, move: move) }
        pendingMove = MoveRequest(direction: .movePull, candidates: downloads(items, to: folder), folder: folder,
                                  sourceName: sideName(.remote), destinationName: sideName(.local))
    }

    /// Starts a confirmed move; with `dontAskAgain`, later moves start without asking.
    public func confirmMove(_ request: MoveRequest, dontAskAgain: Bool) async {
        if pendingMove?.id == request.id { pendingMove = nil }
        if dontAskAgain { preferences.confirmMoves = false }
        await begin(request.candidates, direction: request.direction, folder: request.folder)
    }

    private func uploads(_ urls: [URL], to folder: String) -> [TransferCandidate] {
        urls.map { url in
            let item = LocalFileSource.item(for: url)
            return TransferCandidate(name: item.name, source: url.path,
                                     destination: FilePath.join(folder, item.name, style: remote.style),
                                     isDirectory: item.isDirectory, size: item.size, modified: item.modified)
        }
    }

    private func downloads(_ items: [RemoteDragItem], to folder: String) -> [TransferCandidate] {
        items.map { item in
            TransferCandidate(name: item.name, source: item.path, destination: FilePath.join(folder, item.name, style: .posix),
                              isDirectory: item.dir, size: item.size, modified: item.modified)
        }
    }

    /// Sends the local selection to the open remote folder; ⌘ on the toolbar button moves.
    public func sendSelection(move: Bool) async {
        let urls = local.selectedItems.filter { !$0.isPreviewOnly }.map { URL(fileURLWithPath: $0.path) }
        guard !urls.isEmpty, remote.phase == .loaded else { return }
        await requestSend(urls, to: remote.path, move: move)
    }

    /// Receives the remote selection into the open local folder; ⌘ on the toolbar button moves.
    public func receiveSelection(move: Bool) async {
        let items = remote.selectedItems.map { RemoteDragItem(hostID: hostID, item: $0) }
        guard !items.isEmpty else { return }
        await requestReceive(items, to: local.path, move: move)
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
        var found: [ConflictItem] = []
        for candidate in candidates {
            if let item = existing[candidate.destination] {
                found.append(ConflictItem(candidate: candidate, existing: item))
            } else {
                await start(candidate, direction: direction, policy: .fail)
            }
        }
        guard !found.isEmpty else { return }
        self.conflicts.append(ConflictPrompt(direction: direction, sourceName: sideName(direction.isUpload ? .local : .remote),
                                             destinationName: sideName(direction.isUpload ? .remote : .local),
                                             folder: folder, items: found))
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
            for candidate in candidates {
                if let item = localItem(at: candidate.destination) { found[candidate.destination] = item }
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
            let started = try await backend.transferStart(request)
            preferences.queueExpanded = true
            await transfers.track(started.transferID)
        } catch {
            let name = candidate.name
            show(.error, .ui("files.error.transferStart", "‘\(name)’ 전송을 시작하지 못했습니다"), error: OperationError(error),
                 side: direction.isUpload ? .remote : .local)
        }
    }

    /// Applies the person's decision to the shown conflict, or with `applyToAll` to every
    /// remaining one of the same request. `fail` stops that request: none of its remaining
    /// conflicting items is transferred. Prompts of later requests stay queued.
    public func resolveConflict(_ policy: ConflictPolicy, applyToAll: Bool) async {
        guard var prompt = conflicts.first, !prompt.items.isEmpty else { return }
        if policy == .fail {
            let count = DS.Format.count(prompt.items.count)
            conflicts.removeFirst()
            show(.info, .ui("files.conflict.stopped", "같은 이름이 있는 \(count)개 항목을 보내지 않았습니다"))
            return
        }
        let targets = applyToAll ? prompt.items : [prompt.items[0]]
        prompt.items.removeFirst(targets.count)
        if prompt.items.isEmpty {
            conflicts.removeFirst()
        } else {
            conflicts[0] = prompt
        }
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
        } else if overlay?.item(at: path) != nil {
            await open(.local, path: folder)
            local.selection = [path]
            activeSide = .local
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
            show(.error, .ui("files.error.mkdir", "‘\(name)’ 폴더를 만들지 못했습니다"), error: OperationError(error), side: .remote)
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
            show(.error, problem, side: .remote)
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
            show(.error, .ui("files.error.rename", "‘\(old)’의 이름을 바꾸지 못했습니다"), error: OperationError(error), side: .remote)
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
               failure: { name in .ui("files.error.trash", "‘\(name)’ 항목을 휴지통으로 옮기지 못했습니다") })
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
               failure: { name in .ui("files.error.delete", "‘\(name)’ 항목을 삭제하지 못했습니다") })
    }

    private func report(items: [FileItem], failures: [(FileItem, OperationError)],
                        success: (String) -> String, failure: (String) -> String) {
        if let (item, error) = failures.first {
            var message = failure(item.name)
            if failures.count > 1 {
                let more = DS.Format.count(failures.count - 1)
                message = .ui("files.error.more", "\(message) 외 \(more)개")
            }
            show(.error, message, error: error, side: .remote)
        } else {
            show(.success, success(DS.Format.count(items.count)))
        }
    }

    /// Starts a remote file with `app.launch`, with progress in the remote status line and a
    /// failure inline in the remote pane.
    public func launch(_ item: FileItem) async {
        let name = item.name
        busyMessage = .ui("files.busy.launch", "‘\(name)’ 파일을 원격 PC에서 여는 중…")
        defer { busyMessage = nil }
        do {
            let pid = try await backend.appLaunch(hostID: hostID, path: item.path, args: nil, cwd: nil)
            let host = hostName
            if let pid {
                let number = String(pid)
                show(.success, .ui("files.launchedPID", "\(host)에서 ‘\(name)’ 파일을 열었습니다 · PID \(number)"))
            } else {
                show(.success, .ui("files.launched", "\(host)에서 ‘\(name)’ 파일을 열었습니다"))
            }
        } catch {
            show(.error, .ui("files.error.launch", "‘\(name)’ 파일을 열지 못했습니다"), error: OperationError(error), side: .remote)
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
            let count = DS.Format.count(items.count)
            show(.success, .ui("files.copiedPaths", "경로 \(count)개를 복사했습니다"))
        }
    }

    public func revealInFinder(_ items: [FileItem]) {
        let urls = items.map { URL(fileURLWithPath: $0.path) }
        guard !urls.isEmpty else { return }
        NSWorkspace.shared.activateFileViewerSelecting(urls)
    }

    // MARK: Notices

    /// Shows a toast, or for an error with a pane, an inline message in that pane. A success or
    /// information toast clears the inline errors.
    public func show(_ tone: FilesNotice.Tone, _ message: String, error: OperationError? = nil, side: FileSide? = nil) {
        let notice = FilesNotice(tone: tone, message: message, detail: error.map(\.sentences), code: error?.code, side: side)
        if tone == .error, let side {
            paneErrors[side] = notice
            return
        }
        if tone != .error { paneErrors = [:] }
        noticeTask?.cancel()
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

    public func dismissError(_ side: FileSide) {
        paneErrors[side] = nil
    }
}
