import DeskhandClient
import Foundation

extension LiveBackend {
    public func rdpEndpoint(hostID: String) async throws -> RDPEndpoint {
        struct Params: Encodable, Sendable { let hostID: String }
        return try await client.call("session.rdp", Params(hostID: hostID), timeout: .seconds(25))
    }

    public func inputAttach(hostID: String, sessionID: UInt32, sid: String, connectionGeneration: Int) async throws -> any ScreenStream {
        try await StreamConnection.attach(kind: "input", params: ["host_id": hostID, "session_id": sessionID, "sid": sid, "connection_generation": connectionGeneration],
                                          socketPath: client.socketPath, clientName: "Deskhand",
                                          clientVersion: Self.bundleVersion)
    }
}
