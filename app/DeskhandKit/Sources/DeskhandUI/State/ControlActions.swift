import DeskhandClient
import Foundation
import Observation

/// Control actions of the person at this Mac (local-api.md §3 `control.*`, design.md §5.1):
/// take control, give it back to the AI, and the emergency stop. Tracks each call in flight and
/// the last failure per computer.
@MainActor
@Observable
public final class ControlActions {
    public enum Action: Sendable, Hashable {
        /// `control.acquire{as: "human"}`.
        case takeOver
        /// `control.resume_ai`.
        case giveBack
        /// `control.stop`.
        case stop
    }

    /// The last failed action on a computer.
    public struct Failure: Sendable, Equatable {
        public let action: Action
        public let error: OperationError

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

    /// Set by `takeOver(_:)` while an AI client holds control; cleared by confirming or cancelling.
    public var pendingTakeover: PendingTakeover?

    private var running: [String: Set<Action>] = [:]
    private var failures: [String: Failure] = [:]

    @ObservationIgnored weak var store: AppStore?
    @ObservationIgnored private let backend: any Backend

    init(backend: any Backend) {
        self.backend = backend
    }

    // MARK: State

    public func isRunning(_ action: Action, on hostID: String) -> Bool {
        running[hostID]?.contains(action) ?? false
    }

    /// The last failure on `hostID`, until it is dismissed or another action starts.
    public func failure(for hostID: String) -> Failure? {
        failures[hostID]
    }

    public func dismissFailure(for hostID: String) {
        failures[hostID] = nil
    }

    // MARK: Actions

    /// Takes control for the person at this Mac. While an AI client holds control this only
    /// sets `pendingTakeover`; `confirmTakeover()` then acquires with `force`.
    @discardableResult
    public func takeOver(_ hostID: String) -> Task<Void, Never>? {
        guard let control = store?.control(for: hostID) else { return nil }
        if control.owner == ControlOwner.ai.rawValue {
            pendingTakeover = PendingTakeover(hostID: hostID, client: control.ownerClient)
            return nil
        }
        return acquire(hostID, force: false)
    }

    /// Acquires control for the pending takeover with `force`.
    @discardableResult
    public func confirmTakeover() -> Task<Void, Never>? {
        guard let pending = pendingTakeover else { return nil }
        pendingTakeover = nil
        return acquire(pending.hostID, force: true)
    }

    public func cancelTakeover() {
        pendingTakeover = nil
    }

    /// Lets the AI take control again: clears the AI pause and releases human control.
    @discardableResult
    public func giveBack(_ hostID: String) -> Task<Void, Never>? {
        run(.giveBack, on: hostID) { try await $0.controlResumeAI(hostID: hostID) }
    }

    /// Emergency stop: the human takes control and AI input stays paused until `giveBack(_:)`.
    /// Runs even while a takeover or give-back is in flight.
    @discardableResult
    public func stop(_ hostID: String) -> Task<Void, Never>? {
        run(.stop, on: hostID) { try await $0.controlStop(hostID: hostID) }
    }

    /// Repeats the failed action on `hostID`. A failed takeover runs again without asking,
    /// forcing when an AI client still holds control.
    @discardableResult
    public func retry(_ hostID: String) -> Task<Void, Never>? {
        guard let failure = failures[hostID] else { return nil }
        switch failure.action {
        case .takeOver:
            let owner = store?.control(for: hostID)?.owner
            return acquire(hostID, force: owner == ControlOwner.ai.rawValue)
        case .giveBack:
            return giveBack(hostID)
        case .stop:
            return stop(hostID)
        }
    }

    private func acquire(_ hostID: String, force: Bool) -> Task<Void, Never>? {
        run(.takeOver, on: hostID) { try await $0.controlAcquire(hostID: hostID, as: .human, force: force) }
    }

    /// Starts `call` unless the same action, or the other of take-over and give-back, is in
    /// flight on `hostID`. The returned control state is applied to the store; a failure is kept
    /// for `failure(for:)` and the control state is read again.
    private func run(
        _ action: Action,
        on hostID: String,
        _ call: @escaping @Sendable (any Backend) async throws -> ControlState
    ) -> Task<Void, Never>? {
        guard store?.host(hostID) != nil else { return nil }
        let active = running[hostID] ?? []
        let exclusive: Set<Action> = action == .stop ? [.stop] : [.takeOver, .giveBack]
        guard active.isDisjoint(with: exclusive) else { return nil }
        running[hostID, default: []].insert(action)
        failures[hostID] = nil
        let backend = self.backend
        return Task { [weak self] in
            do {
                let control = try await call(backend)
                self?.running[hostID]?.remove(action)
                self?.store?.applyControlResult(control, for: hostID)
            } catch {
                guard let self else { return }
                running[hostID]?.remove(action)
                if error is CancellationError { return }
                failures[hostID] = Failure(action: action, error: OperationError(error))
                await store?.loadControl(for: hostID)
            }
        }
    }
}
