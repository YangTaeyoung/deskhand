import DeskhandClient

/// Finite recovery of an authenticated, remembered connection. No secrets live here.
struct RDPReconnectPolicy {
    private(set) var wanted = false
    private var authenticated = false
    private var attempts = 0

    mutating func requested(remembered: Bool) {
        wanted = true
        authenticated = remembered
        attempts = 0
    }

    mutating func connected() {
        authenticated = true
        attempts = 0
    }

    mutating func disconnected() {
        wanted = false
        authenticated = false
        attempts = 0
    }

    mutating func nextDelayMS(error: UInt32, remembered: Bool) -> Int? {
        // FreeRDP 3.32.2 error.h: CONNECT_FAILED and CONNECT_TRANSPORT_FAILED.
        // Authentication, account state, certificate, and unknown failures stop.
        guard wanted, authenticated, remembered, attempts < 3,
              error == 0x00020006 || error == 0x0002000D else { return nil }
        let delay = 1000 * (1 << attempts)
        attempts += 1
        return delay
    }

    static func sameTarget(_ prior: RDPEndpoint, _ current: RDPEndpoint) -> Bool {
        current.enabled && prior.port == current.port && prior.sessionID == current.sessionID &&
            prior.sid == current.sid && prior.user == current.user &&
            prior.certificateSHA256.lowercased() == current.certificateSHA256.lowercased()
    }
}
