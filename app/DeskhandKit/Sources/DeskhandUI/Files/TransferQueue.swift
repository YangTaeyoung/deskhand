import DeskhandClient
import Foundation
import Observation
import os

/// Transfers of one computer, newest first, kept current by `transfer.updated` events. The list
/// and the events carry each transfer without its files (counts only); the files of a transfer
/// are read with `transfer.get` when the person opens them and then kept current with the
/// `changed_files` of the events, matched by `rel` (local-api.md §8). An event, list entry or
/// reply is applied only when its `rev` is larger than the last applied one of that transfer.
/// Tracks the pause, resume and cancel calls in flight and their failures.
@MainActor
@Observable
public final class TransferQueue {
    public let hostID: String
    /// Every known transfer of the host, newest first.
    public private(set) var transfers: [Transfer] = []
    /// True once `transfer.list` has answered.
    public private(set) var loaded = false
    public private(set) var loadError: OperationError?
    /// Transfers whose per-file list is expanded.
    public private(set) var expanded: Set<String> = []
    /// Finished transfers the person removed from the list.
    public private(set) var dismissed: Set<String> = []
    public private(set) var running: [String: TransferAction] = [:]
    public private(set) var actionErrors: [String: OperationError] = [:]
    /// `transfer.updated` events whose transfer could not be read.
    public private(set) var undecodedEvents = 0
    /// The files of the transfers read with `transfer.get`, by transfer ID.
    public private(set) var files: [String: [TransferFile]] = [:]
    /// Transfers whose files are being read.
    public private(set) var loadingFiles: Set<String> = []
    /// Why the files of a transfer could not be read.
    public private(set) var fileErrors: [String: OperationError] = [:]

    /// Called once for each transfer that reaches a finished state while the queue watches it.
    @ObservationIgnored var onFinish: ((Transfer) -> Void)?
    /// How long a finished transfer stays in the list after it last changed.
    @ObservationIgnored var retention: () -> TransferRetention = { .month }
    @ObservationIgnored private let backend: any Backend
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var reconnectsTask: Task<Void, Never>?
    /// The last applied `rev` per transfer ID.
    @ObservationIgnored private var revs: [String: Int] = [:]
    /// The `rev` of the transfer when its files were read or last updated from an event.
    @ObservationIgnored private var fileRevs: [String: Int] = [:]
    /// Transfers whose files are read again once the read in flight answers.
    @ObservationIgnored private var staleFiles: Set<String> = []
    nonisolated static let log = Logger(subsystem: "io.github.yangtaeyoung.Deskhand", category: "files")

    init(hostID: String, backend: any Backend) {
        self.hostID = hostID
        self.backend = backend
    }

    /// Transfers shown in the drawer: every one the person did not remove, except finished ones
    /// that last changed longer ago than the retention.
    public var visible: [Transfer] {
        let cutoff = Date(timeIntervalSinceNow: -retention().interval)
        return transfers.filter { transfer in
            guard !dismissed.contains(transfer.transferID) else { return false }
            return !transfer.transferState.isFinished || transfer.updatedAt.date >= cutoff
        }
    }

    public var summary: TransferSummary { TransferSummary(visible) }

    public func transfer(_ id: String) -> Transfer? {
        transfers.first { $0.transferID == id }
    }

    /// Where a finished pull or push wrote its single file: the path the daemon reported for the
    /// file once its files are read, else the requested path.
    public func writtenPath(of transfer: Transfer) -> String {
        let direction = transfer.transferDirection
        let requested = direction.isUpload ? transfer.remote : transfer.local
        guard transfer.filesTotal <= 1, let path = files[transfer.transferID]?.first?.path, !path.isEmpty else { return requested }
        return path
    }

    // MARK: Lifecycle

    /// Subscribes to `transfer.updated`, then loads the host's transfers, and follows the events
    /// until `stop()`. After the stream ends it subscribes and loads again; whenever the daemon
    /// connection comes back or reports `events.dropped` it loads again.
    func start() {
        guard eventsTask == nil else { return }
        let backend = self.backend
        let hostID = self.hostID
        let reconnects = backend.reconnects()
        reconnectsTask = Task { [weak self] in
            for await _ in reconnects {
                guard !Task.isCancelled else { return }
                await self?.reload()
            }
        }
        eventsTask = Task { [weak self] in
            while !Task.isCancelled {
                let stream = backend.events(kinds: EventKind.transferKinds, hostID: hostID)
                await self?.reload()
                do {
                    for try await event in stream {
                        self?.handle(event)
                    }
                } catch {}
                guard !Task.isCancelled else { return }
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    /// Applies a `transfer.updated` event; one whose `transfer` does not decode is logged and
    /// counted. `events.dropped` loads the list and the read files again.
    func handle(_ event: DaemonEvent) {
        if event.kind == EventKind.eventsDropped {
            Task { await reload() }
            return
        }
        guard event.kind == EventKind.transferUpdated else { return }
        do {
            let update = try LiveBackend.decode(TransferUpdate.self, from: .object(event.payload))
            apply(update.transfer, changedFiles: update.changedFiles)
        } catch {
            undecodedEvents += 1
            Self.log.error("transfer.updated seq \(event.seq, privacy: .public) not applied: \(String(describing: error), privacy: .public)")
        }
    }

    func stop() {
        eventsTask?.cancel()
        eventsTask = nil
        reconnectsTask?.cancel()
        reconnectsTask = nil
    }

    /// Reads the list. Finished transfers the daemon no longer lists (it prunes them 30 days
    /// after they finished) leave the queue; files already read are read again.
    public func reload() async {
        do {
            let list = try await backend.transferList(hostID: hostID)
            for transfer in list.reversed() { apply(transfer, notify: loaded) }
            let listed = Set(list.map(\.transferID))
            for transfer in transfers where transfer.transferState.isFinished && !listed.contains(transfer.transferID) {
                forget(transfer.transferID)
            }
            loaded = true
            loadError = nil
            for id in Array(files.keys) where listed.contains(id) { await loadFiles(id) }
        } catch is CancellationError {
        } catch {
            loadError = OperationError(error)
            loaded = true
        }
    }

    /// Inserts or replaces a transfer of this host when its `rev` is larger than the last
    /// applied one; newly seen transfers go to the top. `changedFiles` replace the read files of
    /// the same `rel`. When the files are read and a `rev` was skipped, they are read again.
    func apply(_ transfer: Transfer, changedFiles: [TransferFile] = [], notify: Bool = true) {
        guard transfer.hostID == hostID else { return }
        let id = transfer.transferID
        let last = revs[id]
        if let last, transfer.rev <= last { return }
        revs[id] = transfer.rev
        if let i = transfers.firstIndex(where: { $0.transferID == id }) {
            let wasFinished = transfers[i].transferState.isFinished
            transfers[i] = transfer
            if notify, !wasFinished, transfer.transferState.isFinished { onFinish?(transfer) }
        } else {
            transfers.insert(transfer, at: 0)
            dismissed.remove(id)
        }
        guard var held = files[id] else {
            if transfer.transferState.isFinished, transfer.transferOutcome == .renamed, transfer.filesTotal == 1 {
                Task { await loadFiles(id) }
            }
            return
        }
        for file in changedFiles {
            if let j = held.firstIndex(where: { $0.rel == file.rel }) {
                held[j] = file
            } else {
                held.append(file)
            }
        }
        files[id] = held
        if let known = fileRevs[id], transfer.rev > known + 1 {
            Task { await loadFiles(id) }
        } else {
            fileRevs[id] = transfer.rev
        }
    }

    /// Applies a transfer with its files from `transfer.get` or a reply: the files when they are
    /// at least as new as the applied transfer, then the transfer by its `rev`.
    func apply(_ detail: TransferDetail, notify: Bool = true) {
        guard detail.hostID == hostID else { return }
        let id = detail.transferID
        if detail.rev >= (revs[id] ?? 0) {
            files[id] = detail.files
            fileRevs[id] = detail.rev
            fileErrors[id] = nil
        } else {
            staleFiles.insert(id)
        }
        apply(detail.transfer, notify: notify)
    }

    /// Reads a transfer the app just started so it shows before its first event.
    func track(_ transferID: String) async {
        guard let detail = try? await backend.transferGet(transferID: transferID) else { return }
        apply(detail.transfer)
    }

    /// Reads the files of a transfer; a reply older than the applied transfer is read again once.
    public func loadFiles(_ id: String) async {
        guard !loadingFiles.contains(id) else {
            staleFiles.insert(id)
            return
        }
        loadingFiles.insert(id)
        defer { loadingFiles.remove(id) }
        for _ in 0..<2 {
            staleFiles.remove(id)
            do {
                let detail = try await backend.transferGet(transferID: id)
                apply(detail)
                if !staleFiles.contains(id) { return }
            } catch is CancellationError {
                return
            } catch {
                fileErrors[id] = OperationError(error)
                return
            }
        }
    }

    private func forget(_ id: String) {
        transfers.removeAll { $0.transferID == id }
        revs[id] = nil
        files[id] = nil
        fileRevs[id] = nil
        fileErrors[id] = nil
        expanded.remove(id)
        dismissed.remove(id)
        actionErrors[id] = nil
    }

    // MARK: Files

    /// Opens or closes the file list of a transfer, reading its files the first time it opens.
    public func toggleFiles(_ id: String) {
        if expanded.contains(id) {
            expanded.remove(id)
        } else {
            expand(id)
        }
    }

    /// Opens the file list of a transfer, reading its files when they were not read.
    public func expand(_ id: String) {
        expanded.insert(id)
        if files[id] == nil { Task { await loadFiles(id) } }
    }

    // MARK: Actions

    public func perform(_ action: TransferAction, on id: String) {
        switch action {
        case .pause: call(action, id) { try await $0.transferPause(transferID: id) }
        case .resume, .retry: call(action, id) { try await $0.transferResume(transferID: id) }
        case .cancel: call(action, id) { try await $0.transferCancel(transferID: id) }
        case .dismiss:
            dismissed.insert(id)
            expanded.remove(id)
        case .showDestination, .showSource:
            break
        }
    }

    /// Removes every finished transfer from the list.
    public func clearFinished() {
        for transfer in transfers where transfer.transferState.isFinished {
            dismissed.insert(transfer.transferID)
            expanded.remove(transfer.transferID)
        }
    }

    public var hasFinished: Bool { visible.contains { $0.transferState.isFinished } }

    public func isRunning(_ id: String) -> Bool { running[id] != nil }

    /// Runs a pause, resume or cancel. A refusal (`INVALID_ARGUMENT`: the transfer finished or a
    /// move is removing its source) reads the transfer again so its row shows what it is doing.
    private func call(_ action: TransferAction, _ id: String,
                      _ body: @escaping @MainActor (any Backend) async throws -> TransferDetail) {
        guard running[id] == nil else { return }
        running[id] = action
        actionErrors[id] = nil
        let backend = self.backend
        Task { [weak self] in
            do {
                let detail = try await body(backend)
                self?.apply(detail)
            } catch {
                let failure = OperationError(error)
                self?.actionErrors[id] = TransferActionFailure.explained(failure, action: action)
                if failure.code == "INVALID_ARGUMENT" { await self?.track(id) }
            }
            self?.running[id] = nil
        }
    }

    // MARK: Seeding

    /// Replaces the list without asking the backend. Snapshot scenes use it.
    public func seed(_ transfers: [Transfer], files: [String: [TransferFile]] = [:], expanded: Set<String> = [],
                     actionErrors: [String: OperationError] = [:]) {
        self.transfers = transfers
        revs = Dictionary(transfers.map { ($0.transferID, $0.rev) }, uniquingKeysWith: max)
        self.files = files
        fileRevs = revs.filter { files[$0.key] != nil }
        self.expanded = expanded
        self.actionErrors = actionErrors
        loaded = true
    }

    /// Replaces the list with transfers and their files without asking the backend.
    public func seed(_ details: [TransferDetail], expanded: Set<String> = [], actionErrors: [String: OperationError] = [:]) {
        seed(details.map(\.transfer), files: Dictionary(details.map { ($0.transferID, $0.files) }, uniquingKeysWith: { a, _ in a }),
             expanded: expanded, actionErrors: actionErrors)
    }
}

/// What a refused pause or cancel tells the person.
public enum TransferActionFailure {
    /// The daemon's refusal of a pause or cancel while a move removes its source
    /// (`INVALID_ARGUMENT` "원본 정리 중") in the app's words; other failures as they are.
    public static func explained(_ error: OperationError, action: TransferAction) -> OperationError {
        guard error.code == "INVALID_ARGUMENT", error.message.contains("원본 정리 중") else { return error }
        let message: String = action == .pause
            ? .ui("transfer.refused.pauseRemoving", "원본을 정리하는 중이라 일시정지할 수 없습니다. 정리가 끝나면 이동이 끝납니다.")
            : .ui("transfer.refused.cancelRemoving", "원본을 정리하는 중이라 취소할 수 없습니다. 정리가 끝나면 이동이 끝납니다.")
        return OperationError(code: error.code, message: message)
    }
}
