import DeskhandClient
import Foundation
import XCTest

final class RDPModelsTests: XCTestCase {
    func testEndpointUsesWireKeysAndKeepsSessionIdentity() throws {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        let json = Data("""
        {"enabled":true,"port":3389,"certificate_sha256":"\(String(repeating: "AB", count: 32))",
        "session_id":7,"user":"PC\\\\dev","sid":"S-1-test","protocol":"rdp","connect_state":"active","locked":false}
        """.utf8)
        let endpoint = try decoder.decode(RDPEndpoint.self, from: json)
        XCTAssertEqual(endpoint.port, 3389)
        XCTAssertEqual(endpoint.certificateSHA256, String(repeating: "AB", count: 32))
        XCTAssertEqual(endpoint.sessionID, 7)
        XCTAssertEqual(endpoint.user, "PC\\dev")
        XCTAssertEqual(endpoint.sid, "S-1-test")
        XCTAssertEqual(endpoint.protocolKind, "rdp")
        XCTAssertEqual(endpoint.locked, false)
    }
}
