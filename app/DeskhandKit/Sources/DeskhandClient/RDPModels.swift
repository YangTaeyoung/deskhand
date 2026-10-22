import Foundation

/// Listener identity read by the paired Windows agent, never supplied by an unauthenticated RDP peer.
public struct RDPEndpoint: Decodable, Sendable, Equatable {
    public let enabled: Bool
    public let port: Int
    public let certificateSHA256: String
    public let sessionID: Int
    public let user: String
    public let sid: String
    public let protocolKind: String
    public let connectState: String
    public let locked: Bool?

    public init(enabled: Bool, port: Int, certificateSHA256: String, sessionID: Int, user: String,
                sid: String, protocolKind: String, connectState: String, locked: Bool?) {
        self.enabled = enabled; self.port = port; self.certificateSHA256 = certificateSHA256
        self.sessionID = sessionID; self.user = user; self.sid = sid; self.protocolKind = protocolKind
        self.connectState = connectState; self.locked = locked
    }

    private enum CodingKeys: String, CodingKey {
        case enabled, port, user, sid, connectState, locked
        case certificateSHA256 = "certificateSha256"
        case sessionID = "sessionId"
        case protocolKind = "protocol"
    }
}
