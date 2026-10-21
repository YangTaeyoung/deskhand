/// State of one agent capability (`caps` in docs/protocol.md). Raw values are the protocol strings.
public enum CapabilityState: String, CaseIterable, Codable, Hashable, Sendable {
    case ready
    case unavailable
    case unsupported
    case needsUser = "needs_user"
}
