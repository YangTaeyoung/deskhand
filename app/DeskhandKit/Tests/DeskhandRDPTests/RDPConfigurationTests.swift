import Foundation
import Testing
@testable import DeskhandRDP

struct RDPConfigurationTests {
    private let fingerprint = String(repeating: "aB09", count: 16)

    @Test func defaultsAndCanonicalFingerprint() throws {
        let value = try RDPConfiguration(host: "desktop.local", port: 3389, certificateSHA256: fingerprint)
        #expect(value.host == "desktop.local")
        #expect(value.port == 3389)
        #expect(value.certificateSHA256 == fingerprint.lowercased())
        #expect(value.width == 1920)
        #expect(value.height == 1080)
        #expect(value == (try RDPConfiguration(host: "desktop.local", port: 3389, certificateSHA256: fingerprint.lowercased())))
    }

    @Test(arguments: ["", " ", "\n", "host\u{0}suffix", "host name", "host\nname"])
    func rejectsInvalidHost(host: String) {
        #expect(throws: (any Error).self) {
            try RDPConfiguration(host: host, port: 3389, certificateSHA256: fingerprint)
        }
    }

    @Test(arguments: ["", String(repeating: "a", count: 63), String(repeating: "a", count: 65), String(repeating: "g", count: 64), String(repeating: "é", count: 64), String(repeating: "a:", count: 32)])
    func rejectsInvalidFingerprint(fingerprint: String) {
        #expect(throws: (any Error).self) {
            try RDPConfiguration(host: "localhost", port: 3389, certificateSHA256: fingerprint)
        }
    }

    @Test func rejectsZeroPortAndOutOfBoundsDimensions() {
        #expect(throws: (any Error).self) {
            try RDPConfiguration(host: "localhost", port: 0, certificateSHA256: fingerprint)
        }
        for (width, height): (UInt32, UInt32) in [(0, 1), (1, 0), (8193, 1), (1, 8193), (.max, .max)] {
            #expect(throws: (any Error).self) {
                try RDPConfiguration(host: "localhost", port: 3389, certificateSHA256: fingerprint, width: width, height: height)
            }
        }
    }

    @Test func acceptsDimensionBoundariesAndIPv6() throws {
        for dimension: UInt32 in [1, 8192] {
            _ = try RDPConfiguration(host: "::1", port: .max, certificateSHA256: fingerprint, width: dimension, height: dimension)
        }
    }
}
