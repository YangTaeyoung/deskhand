/// Connection state between the daemon and a remote computer. Raw values are the daemon's JSON strings.
public enum ConnectionState: String, CaseIterable, Codable, Hashable, Sendable {
    case offline
    case connecting
    case ready
    case degraded
    case reconnecting
    case blocked
}
