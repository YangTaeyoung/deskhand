import DeskhandClient
import Foundation
import Observation

/// Control actions of the person at this Mac (local-api.md §3 `control.*`, design.md §5.1):
/// take control, give it back to the AI, and the emergency stop. Tracks each call in flight,
/// the last failure of each action per computer, and takeovers awaiting confirmation.
@MainActor
@Observable
public final class ControlActions {
    public enum Action: Sendable, Hashable, CaseIterable {
        /// `control.acquire{as: "human"}`.
        case takeOver
        /// `control.resume_ai`.
        case giveBack
        /// `control.stop`.
        case stop
    }

    /// The last failure of one action on a computer.
    public struct Failure: Sendable, Equatable, Identifiable {
        public let action: Action
        public let error: OperationError

        public var id: Action { action }

        public init(action: Action, error: OperationError) {
            self.action = action
            self.error = error
        }
    }

    /// A takeover waiting for the person to confirm while an AI client holds control.
    public struct PendingTakeover: Identifiable, Sendable, Equatable {
        public let hostID: String
        /// Name of the AI client holding control.
        public let client: String?

        public var id: String { hostID }

        public init(hostID: String, client: String?) {
            self.hostID = hostID
            self.client = client
        }
    }

    private var pending: [String: PendingTakeover] = [:]
    /// Per host, the number of calls of each action in flight.
    private var running: [String: [Action: Int]] = [:]
    /// Per host, newest first, at most one per action.
    private var failures: [String: [Failure]] = [:]

    @ObservationIgnored weak var store: AppStore?
    @ObservationIgnored private let backend: any Backend

    init(backend: any Backend) {
        self.backend = backend
    }

    // MARK: State

    public func isRunning(_ action: Action, on hostID: String) -> Bool {
        (running[hostID]?[action] ?? 0) > 0
    }

    /// Failed actions on `hostID`, newest first. Each stays until it is dismissed or the same
    /// action runs again.
    public func failures(for hostID: String) -> [Failure] {
        failures[hostID] ?? []
    }

    public func dismissFailure(_ action: Action, for hostID: String) {
        failures[hostID]?.removeAll { $0.action == action }
    }

    /// The takeover of `hostID` waiting for confirmation.
    public func pendingTakeover(for hostID: String) -> PendingTakeover? {
        pending[hostID]
    }

    // MARK: Actions

    /// Takes control for the person at this Mac. While an AI client holds control this only
    /// records a pending takeover; `confirmTakeover(_:)` then acquires with `force`.
    @discardableResult
    public func takeOver(_ hostID: String) -> Task<Void, Never>? {
        guard let control = store?.control(for: hostID) else { return nil }
        if control.owner == ControlOwner.ai.rawValue {
            pending[hostID] = PendingTakeover(hostID: hostID, client: control.ownerClient)
            return nil
        }
        return acquire(hostID, force: false)
    }

    /// Acquires control for the pending takeover of `hostID` with `force`.
    @discardableResult
    public func confirmTakeover(_ hostID: String) -> Task<Void, Never>? {
        guard pending.removeValue(forKey: hostID) != nil else { return nil }
        return acquire(hostID, force: true)
    }

    public func cancelTakeover(_ hostID: String) {
        pending[hostID] = nil
    }

    /// Lets the AI take control again: clears the AI pause and releases human control.
    @discardableResult
    public func giveBack(_ hostID: String) -> Task<Void, Never>? {
        run(.giveBack, on: hostID) { try await $0.controlResumeAI(hostID: hostID) }
    }

    /// Emergency stop: the human takes control and AI input stays paused until `giveBack(_:)`.
    /// Runs even while a takeover, a give-back or another stop is in flight, and on computers
    /// that are offline.
    @discardableResult
    public func stop(_ hostID: String) -> Task<Void, Never>? {
        run(.stop, on: hostID) { try await $0.controlStop(hostID: hostID) }
    }

    /// Repeats the failed `action` on `hostID`. A takeover runs again without asking, forcing
    /// when an AI client still holds control.
    @discardableResult
    public func retry(_ action: Action, on hostID: String) -> Task<Void, Never>? {
        switch action {
        case .takeOver:
            let owner = store?.control(for: hostID)?.owner
            return acquire(hostID, force: owner == ControlOwner.ai.rawValue)
        case .giveBack:
            return giveBack(hostID)
        case .stop:
            return stop(hostID)
        }
    }

    // MARK: Store notifications

    /// Drops every pending takeover.
    func selectionDidChange() {
        pending = [:]
    }

    /// Drops the pending takeover and failures of a computer that left the list.
    func forget(_ hostID: String) {
        pending[hostID] = nil
        failures[hostID] = nil
    }

    // MARK: Calls

    private func acquire(_ hostID: String, force: Bool) -> Task<Void, Never>? {
        run(.takeOver, on: hostID) { try await $0.controlAcquire(hostID: hostID, as: .human, force: force) }
    }

    /// Starts `call` unless take-over or give-back is in flight on `hostID` and `action` is one of
    /// them; a stop always starts. Starting clears the earlier failure of the same action only. The
    /// returned control state goes through `AppStore.applyControl`; a failure is kept for
    /// `failures(for:)` and the control state is read again.
    private func run(
        _ action: Action,
        on hostID: String,
        _ call: @escaping @Sendable (any Backend) async throws -> ControlState
    ) -> Task<Void, Never>? {
        guard store?.host(hostID) != nil else { return nil }
        if action != .stop, isRunning(.takeOver, on: hostID) || isRunning(.giveBack, on: hostID) { return nil }
        running[hostID, default: [:]][action, default: 0] += 1
        dismissFailure(action, for: hostID)
        let backend = self.backend
        return Task { [weak self] in
            do {
                let control = try await call(backend)
                self?.finished(action, on: hostID)
                self?.store?.applyControl(control, for: hostID)
            } catch {
                guard let self else { return }
                finished(action, on: hostID)
                if error is CancellationError { return }
                if store?.host(hostID) != nil {
                    failures[hostID, default: []].insert(Failure(action: action, error: OperationError(error)), at: 0)
                }
                await store?.loadControl(for: hostID)
            }
        }
    }

    private func finished(_ action: Action, on hostID: String) {
        guard let count = running[hostID]?[action] else { return }
        running[hostID]?[action] = count > 1 ? count - 1 : nil
    }
}
