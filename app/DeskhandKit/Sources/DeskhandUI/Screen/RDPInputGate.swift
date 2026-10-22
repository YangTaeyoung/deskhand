import DeskhandClient
import struct DeskhandClient.Host

/// The RDP display and SendInput agent must belong to the same active Windows session.
/// This check supplements, and never replaces, the daemon/agent writer generation checks.
enum RDPInputGate {
    static func permits(host: Host, serverSessionID: UInt32?, expectedSID: String) -> Bool {
        guard host.connection == .ready, let session = host.session,
              let serverSessionID, session.sessionID == Int(serverSessionID),
              !expectedSID.isEmpty, session.sid == expectedSID,
              session.protocolKind == "rdp", session.connectState == "active",
              session.locked == false, session.inputDesktop == "Default" else { return false }
        return host.caps.items["input"]?.capabilityState == .ready
    }
}
