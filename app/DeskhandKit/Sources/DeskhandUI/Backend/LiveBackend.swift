import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// `Backend` over the daemon's local API socket.
public final class LiveBackend: Backend {
    public let client: DeskhandClient
    private let launcher: DaemonLauncher
    private let clientVersion: String
    private let sshConfig: URL

    public init(
        client: DeskhandClient = DeskhandClient(),
        launcher: DaemonLauncher = DaemonLauncher(),
        clientVersion: String = LiveBackend.bundleVersion,
        sshConfig: URL = SSHConfigReader.defaultConfig
    ) {
        self.client = client
        self.launcher = launcher
        self.clientVersion = clientVersion
        self.sshConfig = sshConfig
    }

    /// `CFBundleShortVersionString` of the running app, or `0.0.0` outside a bundle.
    public static var bundleVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.0.0"
    }

    /// Decodes a result value that `DeskhandClient.call` returned as `JSONValue`.
    static func decode<T: Decodable>(_ type: T.Type, from value: JSONValue) throws -> T {
        let data = try JSONEncoder().encode(value)
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(type, from: data)
    }
}

// MARK: Lifecycle

extension LiveBackend {
    public func start() async throws {
        if await client.clientID != nil { return }
        try await launcher.ensureRunning()
        try await client.connect(kind: "app", name: "Deskhand", version: clientVersion)
    }
}

// MARK: Hosts

private struct HostIDParams: Encodable {
    let hostID: String
}

private struct HostAddParams: Encodable {
    let name: String
    let ssh: String
}

private struct HostRemoveParams: Encodable {
    let hostID: String
    let uninstall: Bool
}

private struct HostListResult: Decodable {
    let hosts: [Host]
}

private struct DoctorResult: Decodable {
    let checks: [DoctorCheck]
}

extension JobReceipt {
    init(_ envelope: Envelope<Empty>) {
        self.init(jobID: envelope.jobID, hostID: envelope.hostID, status: envelope.status,
                  error: envelope.error, nextAction: envelope.nextAction)
    }
}

extension LiveBackend {
    public func hostList() async throws -> [Host] {
        let result: HostListResult = try await client.call("host.list", NoParams())
        return result.hosts
    }

    public func hostAdd(name: String, ssh: String) async throws -> JobReceipt {
        let envelope: Envelope<Empty> = try await client.call("host.add", HostAddParams(name: name, ssh: ssh))
        return JobReceipt(envelope)
    }

    public func hostRemove(hostID: String, uninstall: Bool) async throws {
        let _: Empty = try await client.call("host.remove", HostRemoveParams(hostID: hostID, uninstall: uninstall))
    }

    public func hostDoctor(hostID: String) async throws -> [DoctorCheck] {
        let result: DoctorResult = try await client.call("host.doctor", HostIDParams(hostID: hostID))
        return result.checks
    }

    public func hostConnect(hostID: String) async throws {
        let _: Empty = try await client.call("host.connect", HostIDParams(hostID: hostID))
    }

    public func hostDisconnect(hostID: String) async throws {
        let _: Empty = try await client.call("host.disconnect", HostIDParams(hostID: hostID))
    }

    public func hostRepair(hostID: String) async throws -> JobReceipt {
        let envelope: Envelope<Empty> = try await client.call("host.repair", HostIDParams(hostID: hostID))
        return JobReceipt(envelope)
    }

    public func sessionInspect(hostID: String) async throws -> SessionInspect {
        try await client.call("session.inspect", HostIDParams(hostID: hostID))
    }

    public func sshAliases() async -> [String] {
        SSHConfigReader.hosts(from: sshConfig)
    }
}

// MARK: Control

private struct ControlAcquireParams: Encodable {
    let hostID: String
    let `as`: String
    let force: Bool
}

extension LiveBackend {
    public func controlGet(hostID: String) async throws -> ControlState {
        try await client.call("control.get", HostIDParams(hostID: hostID))
    }

    public func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        try await controlChange("control.acquire", hostID: hostID,
                                params: ControlAcquireParams(hostID: hostID, as: owner.rawValue, force: force))
    }

    public func controlRelease(hostID: String) async throws {
        let _: Empty = try await client.call("control.release", HostIDParams(hostID: hostID))
    }

    public func controlStop(hostID: String) async throws -> ControlState {
        try await controlChange("control.stop", hostID: hostID, params: HostIDParams(hostID: hostID))
    }

    public func controlResumeAI(hostID: String) async throws -> ControlState {
        try await controlChange("control.resume_ai", hostID: hostID, params: HostIDParams(hostID: hostID))
    }

    /// Calls a control method and returns the control state from its result, or from
    /// `control.get` when the result does not carry the full state.
    private func controlChange(_ method: String, hostID: String, params: some Encodable & Sendable) async throws -> ControlState {
        let value: JSONValue = try await client.call(method, params)
        if let state = try? Self.decode(ControlState.self, from: value) { return state }
        return try await controlGet(hostID: hostID)
    }
}

// MARK: Timeline

private struct TimelineParams: Encodable {
    let hostID: String
    let since: Int?
    let limit: Int
}

/// `{entries, next_cursor?}`; entries are newest first.
private struct TimelineResult: Decodable {
    let entries: [TimelineEntry]
}

extension LiveBackend {
    public func timelineList(hostID: String, since: Int?, limit: Int) async throws -> [TimelineEntry] {
        let result: TimelineResult = try await client.call("timeline.list", TimelineParams(hostID: hostID, since: since, limit: limit))
        return result.entries
    }
}

// MARK: Events

extension LiveBackend {
    public func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        client.events(kinds: kinds, hostID: hostID)
    }
}

// MARK: Screen

extension LiveBackend {
    public func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        try await StreamConnection.attach(
            kind: "screen", params: ["host_id": hostID, "display": display, "preset": preset.rawValue],
            socketPath: client.socketPath, clientName: "Deskhand", clientVersion: clientVersion)
    }
}

// MARK: Files

private struct FilePathParams: Encodable {
    let hostID: String
    let path: String
}

private struct FileRenameParams: Encodable {
    let hostID: String
    let from: String
    let to: String
}

private struct FileDeleteParams: Encodable {
    let hostID: String
    let path: String
    let permanent: Bool
}

private struct TransferStartParams: Encodable {
    let direction: String
    let hostID: String
    let local: String
    let remote: String
    let onConflict: String
}

private struct TransferIDParams: Encodable {
    let transferID: String
}

/// `{entries, truncated?}`.
private struct FileListResult: Decodable {
    let entries: [FileEntry]
    let truncated: Bool?
}

private struct TransferStartResult: Decodable {
    let transferID: String

    private enum CodingKeys: String, CodingKey {
        case transferID = "transferId"
    }
}

private struct TransferListResult: Decodable {
    let transfers: [Transfer]
}

extension LiveBackend {
    /// `files.list{host_id, path}` returns the whole folder in one page; `cursor` and `limit`
    /// are not sent and the page has no next cursor.
    public func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        let result: FileListResult = try await client.call("files.list", FilePathParams(hostID: hostID, path: path))
        return FileEntryPage(entries: result.entries, nextCursor: nil, total: result.entries.count,
                             truncated: result.truncated ?? false)
    }

    public func filesStat(hostID: String, path: String) async throws -> FileEntry {
        try await client.call("files.stat", FilePathParams(hostID: hostID, path: path))
    }

    public func filesMkdir(hostID: String, path: String) async throws {
        try await envelopeCall("files.mkdir", FilePathParams(hostID: hostID, path: path))
    }

    public func filesRename(hostID: String, from: String, to: String) async throws {
        try await envelopeCall("files.rename", FileRenameParams(hostID: hostID, from: from, to: to))
    }

    public func filesTrash(hostID: String, path: String) async throws {
        try await envelopeCall("files.trash", FilePathParams(hostID: hostID, path: path))
    }

    public func filesDelete(hostID: String, path: String) async throws {
        try await envelopeCall("files.delete", FileDeleteParams(hostID: hostID, path: path, permanent: true))
    }

    public func appLaunch(hostID: String, path: String) async throws {
        try await envelopeCall("app.launch", FilePathParams(hostID: hostID, path: path))
    }

    public func transferStart(_ request: TransferRequest) async throws -> String {
        let value: JSONValue = try await client.call("transfer.start", TransferStartParams(
            direction: request.direction.rawValue, hostID: request.hostID, local: request.local,
            remote: request.remote, onConflict: request.onConflict.rawValue))
        if value["status"] != nil {
            try Self.check(try Self.decode(Envelope<Empty>.self, from: value))
        }
        return try Self.decode(TransferStartResult.self, from: value).transferID
    }

    public func transferList(hostID: String) async throws -> [Transfer] {
        let result: TransferListResult = try await client.call("transfer.list", HostIDParams(hostID: hostID))
        return result.transfers
    }

    public func transferGet(transferID: String) async throws -> Transfer {
        try await client.call("transfer.get", TransferIDParams(transferID: transferID))
    }

    public func transferPause(transferID: String) async throws -> Transfer {
        try await transferChange("transfer.pause", transferID: transferID)
    }

    public func transferResume(transferID: String) async throws -> Transfer {
        try await transferChange("transfer.resume", transferID: transferID)
    }

    public func transferCancel(transferID: String) async throws -> Transfer {
        try await transferChange("transfer.cancel", transferID: transferID)
    }

    /// Calls a transfer method and returns the transfer from its result, or from `transfer.get`
    /// when the result does not carry it.
    private func transferChange(_ method: String, transferID: String) async throws -> Transfer {
        let value: JSONValue = try await client.call(method, TransferIDParams(transferID: transferID))
        if value["status"] != nil { try Self.check(try Self.decode(Envelope<Empty>.self, from: value)) }
        if let transfer = try? Self.decode(Transfer.self, from: value) { return transfer }
        if let nested = value["transfer"], let transfer = try? Self.decode(Transfer.self, from: nested) { return transfer }
        return try await transferGet(transferID: transferID)
    }

    /// Calls a method whose result is an envelope and throws its error unless it succeeded.
    private func envelopeCall(_ method: String, _ params: some Encodable & Sendable) async throws {
        let envelope: Envelope<Empty> = try await client.call(method, params)
        try Self.check(envelope)
    }

    /// Throws the envelope's error when its status is not `succeeded` or `running`.
    static func check<T>(_ envelope: Envelope<T>) throws {
        switch envelope.status {
        case .succeeded, .running, .waitingForUser:
            return
        case .unknown:
            throw OperationError(code: envelope.error?.code ?? "EXECUTION_OUTCOME_UNKNOWN",
                                 message: envelope.error?.message ?? .ui("error.outcomeUnknown", "원격 PC에서 실행됐는지 확인하지 못했습니다"),
                                 nextAction: envelope.nextAction ?? envelope.error?.nextAction)
        case .failed, .cancelled:
            if let error = envelope.error { throw OperationError(error) }
            throw OperationError(message: .ui("error.unknown", "알 수 없는 오류가 발생했습니다"), nextAction: envelope.nextAction)
        }
    }
}
