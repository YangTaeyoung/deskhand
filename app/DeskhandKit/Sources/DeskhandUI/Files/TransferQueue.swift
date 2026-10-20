import DeskhandClient
import Foundation
import Observation

/// Transfers of one computer, newest first, kept current by `transfer.updated` events. Tracks
/// the pause, resume and cancel calls in flight and their failures.
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

    /// Called once for each transfer that reaches a finished state while the queue watches it.
    @ObservationIgnored var onFinish: ((Transfer) -> Void)?
    @ObservationIgnored private let backend: any Backend
    @ObservationIgnored private var eventsTask: Task<Void, Never>?

    init(hostID: String, backend: any Backend) {
        self.hostID = hostID
        self.backend = backend
    }

    /// Transfers shown in the drawer.
    public var visible: [Transfer] { transfers.filter { !dismissed.contains($0.transferID) } }

    public var summary: TransferSummary { TransferSummary(visible) }

    public func transfer(_ id: String) -> Transfer? {
        transfers.first { $0.transferID == id }
    }

    // MARK: Lifecycle

    /// Loads the host's transfers and follows `transfer.updated` events until `stop()`.
    func start() {
        guard eventsTask == nil else { return }
        let backend = self.backend
        let hostID = self.hostID
        eventsTask = Task { [weak self] in
            await self?.reload()
            while !Task.isCancelled {
                let stream = backend.events(kinds: [EventKind.transferUpdated], hostID: hostID)
                do {
                    for try await event in stream {
                        guard let value = event.payload["transfer"],
                              let transfer = try? LiveBackend.decode(Transfer.self, from: value) else { continue }
                        self?.apply(transfer)
                    }
                } catch {}
                guard !Task.isCancelled else { return }
                try? await Task.sleep(for: .seconds(1))
                await self?.reload()
            }
        }
    }

    func stop() {
        eventsTask?.cancel()
        eventsTask = nil
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

    /// Inserts or replaces a transfer of this host. Newly seen transfers go to the top.
    func apply(_ transfer: Transfer, notify: Bool = true) {
        guard transfer.hostID == hostID else { return }
        if let i = transfers.firstIndex(where: { $0.transferID == transfer.transferID }) {
            let wasFinished = transfers[i].transferState.isFinished
            transfers[i] = transfer
            if notify, !wasFinished, transfer.transferState.isFinished { onFinish?(transfer) }
        } else {
            transfers.insert(transfer, at: 0)
            dismissed.remove(transfer.transferID)
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
        self.expanded = expanded
        loaded = true
    }
}
