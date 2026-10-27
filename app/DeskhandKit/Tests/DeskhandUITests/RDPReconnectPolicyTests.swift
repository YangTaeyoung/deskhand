import DeskhandClient
import XCTest
@testable import DeskhandUI

final class RDPReconnectPolicyTests: XCTestCase {
    func testRecoveryIsFiniteAndExplicitDisconnectStopsIt() {
        var policy = RDPReconnectPolicy()
        policy.requested(remembered: true)
        XCTAssertEqual(policy.nextDelayMS(error: 0x2000D, remembered: true), 1000)
        XCTAssertEqual(policy.nextDelayMS(error: 0x20006, remembered: true), 2000)
        XCTAssertEqual(policy.nextDelayMS(error: 0x2000D, remembered: true), 4000)
        XCTAssertNil(policy.nextDelayMS(error: 0x2000D, remembered: true))
        policy.connected()
        XCTAssertEqual(policy.nextDelayMS(error: 0x2000D, remembered: true), 1000)
        policy.disconnected()
        XCTAssertNil(policy.nextDelayMS(error: 0x2000D, remembered: true))
    }

    func testAuthenticationAndCertificateErrorsNeverRetry() {
        var policy = RDPReconnectPolicy()
        policy.requested(remembered: true)
        for code: UInt32 in [0x20009, 0x20014, 0x20018, 0x2000E, 0x2000F, 0x20013, 0xD4000004, 1] {
            XCTAssertNil(policy.nextDelayMS(error: code, remembered: true))
        }
        XCTAssertEqual(policy.nextDelayMS(error: 0x2000D, remembered: true), 1000)
        XCTAssertNil(policy.nextDelayMS(error: 0x2000D, remembered: false))
        policy.requested(remembered: false)
        XCTAssertNil(policy.nextDelayMS(error: 0x2000D, remembered: true))
    }

    func testNewEndpointMustRetainCertificateAccountAndSession() {
        func endpoint(cert: String = "AA", sid: String = "sid", session: Int = 1, port: Int = 3389) -> RDPEndpoint {
            .init(enabled: true, port: port, certificateSHA256: cert, sessionID: session,
                  user: "PC\\user", sid: sid, protocolKind: "rdp", connectState: "active", locked: false)
        }
        XCTAssertTrue(RDPReconnectPolicy.sameTarget(endpoint(), endpoint(cert: "aa")))
        XCTAssertFalse(RDPReconnectPolicy.sameTarget(endpoint(), endpoint(cert: "BB")))
        XCTAssertFalse(RDPReconnectPolicy.sameTarget(endpoint(), endpoint(sid: "other")))
        XCTAssertFalse(RDPReconnectPolicy.sameTarget(endpoint(), endpoint(session: 2)))
        XCTAssertFalse(RDPReconnectPolicy.sameTarget(endpoint(), endpoint(port: 3390)))
    }
}
