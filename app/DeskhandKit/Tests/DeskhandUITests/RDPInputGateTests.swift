import DeskhandClient
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

final class RDPInputGateTests: XCTestCase {
    private func host() -> Host {
        var h = PreviewFixtures.hosts().first { $0.hostID == PreviewFixtures.devPCID }!
        h.state = "ready"
        h.session = SessionInfo(sessionID: 7, user: "PC\\dev", sid: "S-1-test", connectState: "active",
                                protocolKind: "rdp", locked: false, inputDesktop: "Default")
        return h
    }

    func testOnlyMatchingActiveRDPUserSessionAllowsInput() {
        let h = host()
        XCTAssertTrue(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: nil, expectedSID: "S-1-test"))
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 8, expectedSID: "S-1-test"))
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-other"))
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: ""))
    }

    func testLockDisconnectConsoleAndUnknownStateFailClosed() {
        var h = host()
        h.session?.locked = true
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
        h = host(); h.session?.locked = nil
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
        h = host(); h.session?.protocolKind = "console"
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
        h = host(); h.session?.connectState = "disconnected"
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
        h = host(); h.session?.inputDesktop = "Winlogon"
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
        h = host(); h.state = "offline"
        XCTAssertFalse(RDPInputGate.permits(host: h, serverSessionID: 7, expectedSID: "S-1-test"))
    }
}
