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
final class GatedBackend: Backend, LocalOverlayProviding {
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
        var filesListCalls: [FilesListCall] = []
        var onePageListing = false
        var transferRequests: [TransferRequest] = []
        var fileCalls: [FileCall] = []
    }

    private let state = Mutex(State())

    init(_ base: PreviewBackend) {
        self.base = base
    }

    var localOverlay: LocalOverlay { base.localOverlay }

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

    var filesListCalls: [FilesListCall] { state.withLock { $0.filesListCalls } }
    var transferRequests: [TransferRequest] { state.withLock { $0.transferRequests } }
    var fileCalls: [FileCall] { state.withLock { $0.fileCalls } }

    /// Answers `filesList` with the whole folder in one page, as `files.list` without paging does.
    func listInOnePage(_ onePage: Bool) {
        state.withLock { $0.onePageListing = onePage }
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
    /// Reads the preview daemon's state, then waits while `control.get` is held with `afterApplying`.
    func controlGet(hostID: String) async throws -> ControlState {
        let control = try await base.controlGet(hostID: hostID)
        await gate("control.get", afterApplying: true)
        return control
    }
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

    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        try await base.screenAttach(hostID: hostID, display: display, preset: preset)
    }

    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        try await base.ptyOpen(hostID: hostID, shell: shell, cols: cols, rows: rows)
    }

    func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream { try await base.ptyAttach(target) }
    func ptyJobs(hostID: String) async throws -> [Job] { try await base.ptyJobs(hostID: hostID) }
    func ptyKill(jobID: String) async throws { try await base.ptyKill(jobID: jobID) }

    func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        let onePage = state.withLock { s -> Bool in
            s.filesListCalls.append(FilesListCall(path: path, cursor: cursor, limit: limit))
            return s.onePageListing
        }
        guard onePage else { return try await base.filesList(hostID: hostID, path: path, cursor: cursor, limit: limit) }
        let all = try await base.filesList(hostID: hostID, path: path, cursor: nil, limit: Int.max)
        return FileEntryPage(entries: all.entries, total: all.entries.count)
    }

    func filesStat(hostID: String, path: String) async throws -> FileEntry { try await base.filesStat(hostID: hostID, path: path) }

    func filesMkdir(hostID: String, path: String) async throws {
        record(FileCall(method: "files.mkdir", path: path))
        try await base.filesMkdir(hostID: hostID, path: path)
    }

    func filesRename(hostID: String, from: String, to: String) async throws {
        record(FileCall(method: "files.rename", path: from, to: to))
        try await base.filesRename(hostID: hostID, from: from, to: to)
    }

    func filesTrash(hostID: String, path: String) async throws {
        record(FileCall(method: "files.trash", path: path))
        try await base.filesTrash(hostID: hostID, path: path)
    }

    func filesDelete(hostID: String, path: String) async throws {
        record(FileCall(method: "files.delete", path: path))
        try await base.filesDelete(hostID: hostID, path: path)
    }

    func appLaunch(hostID: String, path: String, args: [String]?, cwd: String?) async throws -> Int? {
        record(FileCall(method: "app.launch", path: path))
        return try await base.appLaunch(hostID: hostID, path: path, args: args, cwd: cwd)
    }

    func transferStart(_ request: TransferRequest) async throws -> String {
        state.withLock { $0.transferRequests.append(request) }
        return try await base.transferStart(request)
    }

    func transferList(hostID: String) async throws -> [Transfer] { try await base.transferList(hostID: hostID) }
    func transferGet(transferID: String) async throws -> Transfer { try await base.transferGet(transferID: transferID) }
    func transferPause(transferID: String) async throws -> Transfer { try await base.transferPause(transferID: transferID) }
    func transferResume(transferID: String) async throws -> Transfer { try await base.transferResume(transferID: transferID) }
    func transferCancel(transferID: String) async throws -> Transfer { try await base.transferCancel(transferID: transferID) }

    private func record(_ call: FileCall) {
        state.withLock { $0.fileCalls.append(call) }
    }
}

/// One `filesList` call as `GatedBackend` received it.
struct FilesListCall: Equatable {
    let path: String
    let cursor: String?
    let limit: Int
}

/// One state-changing `files.*` or `app.launch` call.
struct FileCall: Equatable {
    let method: String
    let path: String
    var to: String?
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
