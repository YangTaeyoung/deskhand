import DeskhandClient
import Foundation
import Observation
import os

/// Transfers of one computer, newest first, kept current by `transfer.updated` events. Tracks
/// the pause, resume and cancel calls in flight and their failures. An event, list entry or
/// call reply is applied only when its `rev` is larger than the last applied one of that
/// transfer (local-api.md §5).
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
    public var expanded: Set<String> = []
    /// Finished transfers the person removed from the list.
    public private(set) var dismissed: Set<String> = []
    public private(set) var running: [String: TransferAction] = [:]
    public private(set) var actionErrors: [String: OperationError] = [:]
    /// `transfer.updated` events whose transfer could not be read.
    public private(set) var undecodedEvents = 0

    /// Called once for each transfer that reaches a finished state while the queue watches it.
    @ObservationIgnored var onFinish: ((Transfer) -> Void)?
    /// How long a finished transfer stays in the list after it last changed.
    @ObservationIgnored var retention: () -> TransferRetention = { .month }
    @ObservationIgnored private let backend: any Backend
    @ObservationIgnored private var eventsTask: Task<Void, Never>?
    @ObservationIgnored private var reconnectsTask: Task<Void, Never>?
    /// The last applied `rev` per transfer ID.
    @ObservationIgnored private var revs: [String: Int] = [:]
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
    /// counted. `events.dropped` loads the list again.
    func handle(_ event: DaemonEvent) {
        if event.kind == EventKind.eventsDropped {
            Task { await reload() }
            return
        }
        guard event.kind == EventKind.transferUpdated else { return }
        do {
            guard let value = event.payload["transfer"] else { throw DecodingError.valueNotFound(
                Transfer.self, .init(codingPath: [], debugDescription: "transfer.updated without transfer")) }
            apply(try LiveBackend.decode(Transfer.self, from: value))
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

    public func reload() async {
        do {
            let list = try await backend.transferList(hostID: hostID)
            for transfer in list.reversed() { apply(transfer, notify: loaded) }
            loaded = true
            loadError = nil
        } catch is CancellationError {
        } catch {
            loadError = OperationError(error)
            loaded = true
        }
    }

    /// Inserts or replaces a transfer of this host when its `rev` is larger than the last
    /// applied one. Newly seen transfers go to the top.
    func apply(_ transfer: Transfer, notify: Bool = true) {
        guard transfer.hostID == hostID else { return }
        let id = transfer.transferID
        if let last = revs[id], transfer.rev <= last { return }
        revs[id] = transfer.rev
        if let i = transfers.firstIndex(where: { $0.transferID == id }) {
            let wasFinished = transfers[i].transferState.isFinished
            transfers[i] = transfer
            if notify, !wasFinished, transfer.transferState.isFinished { onFinish?(transfer) }
        } else {
            transfers.insert(transfer, at: 0)
            dismissed.remove(id)
        }
    }

    /// Reads a transfer the app just started so it shows before its first event.
    func track(_ transferID: String) async {
        guard let transfer = try? await backend.transferGet(transferID: transferID) else { return }
        apply(transfer)
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

    private func call(_ action: TransferAction, _ id: String, _ body: @escaping @MainActor (any Backend) async throws -> Transfer) {
        guard running[id] == nil else { return }
        running[id] = action
        actionErrors[id] = nil
        let backend = self.backend
        Task { [weak self] in
            do {
                let transfer = try await body(backend)
                self?.apply(transfer)
            } catch {
                self?.actionErrors[id] = OperationError(error)
            }
            self?.running[id] = nil
        }
    }

    // MARK: Seeding

    /// Replaces the list without asking the backend. Snapshot scenes use it.
    public func seed(_ transfers: [Transfer], expanded: Set<String> = []) {
        self.transfers = transfers
        revs = Dictionary(transfers.map { ($0.transferID, $0.rev) }, uniquingKeysWith: max)
        self.expanded = expanded
        loaded = true
    }
}
