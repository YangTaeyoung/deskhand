import DeskhandClient
import DeskhandUI
import Foundation
import struct DeskhandClient.Host

/// `Backend` for snapshot scenes. Control and timeline reads return exactly what the scene
/// seeded; a timeline can stay pending or fail, and control changes can fail. File changes and
/// transfers do nothing, since scenes seed the files tab. Everything else goes to a
/// `PreviewBackend`.
final class SnapshotBackend: Backend {
    enum TimelineReply: Sendable {
        case entries([TimelineEntry])
        case failure(OperationError)
        /// Never answers while the scene renders.
        case pending
    }

    private let base = PreviewBackend(scenario: .standard, stepDelay: .zero)
    private let controls: [String: ControlState]
    private let timelines: [String: TimelineReply]
    private let controlFailure: OperationError?

    init(controls: [String: ControlState], timelines: [String: TimelineReply], controlFailure: OperationError? = nil) {
        self.controls = controls
        self.timelines = timelines
        self.controlFailure = controlFailure
    }

    func start() async throws {}
    func hostList() async throws -> [Host] { try await base.hostList() }
    func hostAdd(name: String, ssh: String) async throws -> JobReceipt { try await base.hostAdd(name: name, ssh: ssh) }
    func hostRemove(hostID: String, uninstall: Bool) async throws {}
    func hostDoctor(hostID: String) async throws -> [DoctorCheck] { PreviewFixtures.doctor(hostID: hostID) }
    func hostConnect(hostID: String) async throws {}
    func hostDisconnect(hostID: String) async throws {}
    func hostRepair(hostID: String) async throws -> JobReceipt { JobReceipt(jobID: nil, status: .running) }
    func sessionInspect(hostID: String) async throws -> SessionInspect { try await base.sessionInspect(hostID: hostID) }
    func sshAliases() async -> [String] { PreviewFixtures.sshAliases }

    func controlGet(hostID: String) async throws -> ControlState {
        controls[hostID] ?? ControlState(owner: "none", gen: 0, aiPaused: false)
    }

    func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        try changeControl(hostID)
    }

    func controlRelease(hostID: String) async throws {
        _ = try changeControl(hostID)
    }

    func controlStop(hostID: String) async throws -> ControlState {
        try changeControl(hostID)
    }

    func controlResumeAI(hostID: String) async throws -> ControlState {
        try changeControl(hostID)
    }

    /// Fails with `controlFailure` when set; otherwise leaves the seeded control unchanged.
    private func changeControl(_ hostID: String) throws -> ControlState {
        if let controlFailure { throw controlFailure }
        return controls[hostID] ?? ControlState(owner: "none", gen: 0, aiPaused: false)
    }

    func timelineList(hostID: String, since: Int?, limit: Int) async throws -> [TimelineEntry] {
        switch timelines[hostID] ?? .entries([]) {
        case .entries(let entries):
            return entries
        case .failure(let error):
            throw error
        case .pending:
            try await Task.sleep(for: .seconds(3600))
            return []
        }
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        AsyncThrowingStream { _ in }
    }

    func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        try await base.filesList(hostID: hostID, path: path, cursor: cursor, limit: limit)
    }

    func filesStat(hostID: String, path: String) async throws -> FileEntry { try await base.filesStat(hostID: hostID, path: path) }
    func filesMkdir(hostID: String, path: String) async throws {}
    func filesRename(hostID: String, from: String, to: String) async throws {}
    func filesTrash(hostID: String, path: String) async throws {}
    func filesDelete(hostID: String, path: String) async throws {}
    func appLaunch(hostID: String, path: String) async throws {}
    func transferStart(_ request: TransferRequest) async throws -> String { throw ClientError.notConnected }
    func transferList(hostID: String) async throws -> [Transfer] { [] }
    func transferGet(transferID: String) async throws -> Transfer { throw ClientError.notConnected }
    func transferPause(transferID: String) async throws -> Transfer { throw ClientError.notConnected }
    func transferResume(transferID: String) async throws -> Transfer { throw ClientError.notConnected }
    func transferCancel(transferID: String) async throws -> Transfer { throw ClientError.notConnected }

    /// Throws; scenes draw the screen with `ScreenStandIn`.
    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        throw ClientError.notConnected
    }
}
