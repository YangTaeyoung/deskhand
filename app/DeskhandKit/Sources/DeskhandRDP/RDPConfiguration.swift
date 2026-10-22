import Foundation

public struct RDPConfiguration: Sendable, Equatable {
    public let host: String
    public let port: UInt16
    public let certificateSHA256: String
    public let width: UInt32
    public let height: UInt32

    public init(
        host: String,
        port: UInt16,
        certificateSHA256: String,
        width: UInt32 = 1920,
        height: UInt32 = 1080
    ) throws {
        guard !host.isEmpty,
              !host.unicodeScalars.contains(where: { CharacterSet.whitespacesAndNewlines.contains($0) || CharacterSet.controlCharacters.contains($0) }),
              port != 0,
              certificateSHA256.utf8.count == 64,
              certificateSHA256.utf8.allSatisfy({ (48...57).contains($0) || (65...70).contains($0) || (97...102).contains($0) }),
              RDPFrame.byteCount(width: Int(width), height: Int(height), stride: Int(width) * 4) != nil
        else { throw RDPFailure.invalidConfiguration }
        self.host = host
        self.port = port
        self.certificateSHA256 = certificateSHA256.lowercased()
        self.width = width
        self.height = height
    }
}

// Deliberately excludes native diagnostic strings, URLs and credentials.
enum RDPFailure: Error, LocalizedError {
    case invalidConfiguration
    case invalidCredentials
    case engineUnavailable
    case sessionUnavailable

    var errorDescription: String? {
        switch self {
        case .invalidConfiguration: "Invalid RDP configuration."
        case .invalidCredentials: "Invalid RDP credentials."
        case .engineUnavailable: "RDP engine is unavailable."
        case .sessionUnavailable: "RDP session could not be created."
        }
    }
}
