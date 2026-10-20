import DeskhandClient
import Foundation
import Synchronization
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// One `control.*` call as `GatedBackend` received it.
struct ControlCall: Equatable {
    let method: String
    let hostID: String
    var owner: ControlOwner?
    var force: Bool?
}

/// Backend that forwards to a `PreviewBackend`, counts `hostList` calls, can hold `hostList`
/// replies until the test resumes them in any order, can fail `timelineList`, and records,
/// holds or fails `control.*` changes.
final class GatedBackend: Backend {
    let base: PreviewBackend

    private struct HeldControl: Hashable {
        let method: String
        let afterApplying: Bool
    }

    private struct State {
        var holdHostList = false
        var pendingHostLists: [CheckedContinuation<[Host], Error>] = []
        var hostListCalls = 0
        var timelineFailure: OperationError?
        var controlCalls: [ControlCall] = []
        var controlFailures: [String: OperationError] = [:]
        var heldControls: Set<HeldControl> = []
        var pendingControls: [(method: String, continuation: CheckedContinuation<Void, Never>)] = []
    }

    private let state = Mutex(State())

    init(_ base: PreviewBackend) {
        self.base = base
    }

    func holdHostList(_ hold: Bool) {
        state.withLock { $0.holdHostList = hold }
    }

    var pendingHostListCount: Int { state.withLock { $0.pendingHostLists.count } }
    var hostListCalls: Int { state.withLock { $0.hostListCalls } }

    /// Replies to the held `hostList` call at `index` (in call order among those still held).
    func resumeHostList(at index: Int, with result: Result<[Host], Error>) {
        let continuation = state.withLock { $0.pendingHostLists.remove(at: index) }
        continuation.resume(with: result)
    }

    func failTimeline(with error: OperationError?) {
        state.withLock { $0.timelineFailure = error }
    }

    var controlCalls: [ControlCall] { state.withLock { $0.controlCalls } }

    /// Makes `method` (for example `control.stop`) throw `error`, or succeed again when nil.
    func failControl(_ method: String, with error: OperationError?) {
        state.withLock { $0.controlFailures[method] = error }
    }

    /// Holds calls of `method` until `releaseControl(_:)`: before they reach the preview
    /// daemon, or with `afterApplying`, after it applied them and before the reply returns.
    func holdControl(_ method: String, afterApplying: Bool = false) {
        state.withLock { _ = $0.heldControls.insert(HeldControl(method: method, afterApplying: afterApplying)) }
    }

    var pendingControlCount: Int { state.withLock { $0.pendingControls.count } }

    /// Stops holding `method` and lets its held calls continue.
    func releaseControl(_ method: String) {
        let released = state.withLock { s -> [CheckedContinuation<Void, Never>] in
            s.heldControls = s.heldControls.filter { $0.method != method }
            let matching = s.pendingControls.filter { $0.method == method }.map(\.continuation)
            s.pendingControls.removeAll { $0.method == method }
            return matching
        }
        for continuation in released { continuation.resume() }
    }

    private func gate(_ method: String, afterApplying: Bool) async {
        let hold = state.withLock { $0.heldControls.contains(HeldControl(method: method, afterApplying: afterApplying)) }
        guard hold else { return }
        await withCheckedContinuation { continuation in
            state.withLock { $0.pendingControls.append((method, continuation)) }
        }
    }

    /// Records `call`, applies the hold and failure configured for its method, and runs `apply`.
    private func control<T>(_ call: ControlCall, _ apply: () async throws -> T) async throws -> T {
        state.withLock { $0.controlCalls.append(call) }
        await gate(call.method, afterApplying: false)
        if let failure = state.withLock({ $0.controlFailures[call.method] }) { throw failure }
        let result = try await apply()
        await gate(call.method, afterApplying: true)
        return result
    }

    func start() async throws { try await base.start() }

    func hostList() async throws -> [Host] {
        let hold = state.withLock { s -> Bool in
            s.hostListCalls += 1
            return s.holdHostList
        }
        guard hold else { return try await base.hostList() }
        return try await withCheckedThrowingContinuation { continuation in
            state.withLock { $0.pendingHostLists.append(continuation) }
        }
    }

    func hostAdd(name: String, ssh: String) async throws -> JobReceipt { try await base.hostAdd(name: name, ssh: ssh) }
    func hostRemove(hostID: String, uninstall: Bool) async throws { try await base.hostRemove(hostID: hostID, uninstall: uninstall) }
    func hostDoctor(hostID: String) async throws -> [DoctorCheck] { try await base.hostDoctor(hostID: hostID) }
    func hostConnect(hostID: String) async throws { try await base.hostConnect(hostID: hostID) }
    func hostDisconnect(hostID: String) async throws { try await base.hostDisconnect(hostID: hostID) }
    func hostRepair(hostID: String) async throws -> JobReceipt { try await base.hostRepair(hostID: hostID) }
    func sessionInspect(hostID: String) async throws -> SessionInspect { try await base.sessionInspect(hostID: hostID) }
    func sshAliases() async -> [String] { await base.sshAliases() }
    func controlGet(hostID: String) async throws -> ControlState { try await base.controlGet(hostID: hostID) }
    func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        try await control(ControlCall(method: "control.acquire", hostID: hostID, owner: owner, force: force)) {
            try await base.controlAcquire(hostID: hostID, as: owner, force: force)
        }
    }
    func controlRelease(hostID: String) async throws {
        try await control(ControlCall(method: "control.release", hostID: hostID)) {
            try await base.controlRelease(hostID: hostID)
        }
    }
    func controlStop(hostID: String) async throws -> ControlState {
        try await control(ControlCall(method: "control.stop", hostID: hostID)) {
            try await base.controlStop(hostID: hostID)
        }
    }
    func controlResumeAI(hostID: String) async throws -> ControlState {
        try await control(ControlCall(method: "control.resume_ai", hostID: hostID)) {
            try await base.controlResumeAI(hostID: hostID)
        }
    }

    func timelineList(hostID: String, since: Int?, limit: Int) async throws -> [TimelineEntry] {
        if let failure = state.withLock({ $0.timelineFailure }) { throw failure }
        return try await base.timelineList(hostID: hostID, since: since, limit: limit)
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        base.events(kinds: kinds, hostID: hostID)
    }
}

/// Polls `condition` on the main actor until it holds or `timeout` passes.
@MainActor
func waitUntil(timeout: Duration = .seconds(3), _ condition: @MainActor () -> Bool,
               file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = ContinuousClock.now + timeout
    while !condition() {
        if ContinuousClock.now > deadline {
            return XCTFail("condition not met within \(timeout)", file: file, line: line)
        }
        try await Task.sleep(for: .milliseconds(5))
    }
}
