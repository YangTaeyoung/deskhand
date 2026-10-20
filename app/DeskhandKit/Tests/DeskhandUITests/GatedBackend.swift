import DeskhandClient
import Foundation
import Synchronization
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// Backend that forwards to a `PreviewBackend`, counts `hostList` calls, can hold `hostList`
/// replies until the test resumes them in any order, and can fail `timelineList`.
final class GatedBackend: Backend {
    let base: PreviewBackend

    private struct State {
        var holdHostList = false
        var pendingHostLists: [CheckedContinuation<[Host], Error>] = []
        var hostListCalls = 0
        var timelineFailure: OperationError?
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
        try await base.controlAcquire(hostID: hostID, as: owner, force: force)
    }
    func controlRelease(hostID: String) async throws { try await base.controlRelease(hostID: hostID) }
    func controlStop(hostID: String) async throws -> ControlState { try await base.controlStop(hostID: hostID) }
    func controlResumeAI(hostID: String) async throws -> ControlState { try await base.controlResumeAI(hostID: hostID) }

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
