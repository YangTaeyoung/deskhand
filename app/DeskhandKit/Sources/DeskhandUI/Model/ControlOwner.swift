/// Who holds input control of a remote session (`control.get` `owner`).
public enum ControlOwner: String, CaseIterable, Codable, Hashable, Sendable {
    case none
    case human
    case ai
}
