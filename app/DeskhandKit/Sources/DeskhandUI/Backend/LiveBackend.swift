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
