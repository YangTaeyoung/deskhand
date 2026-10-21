import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandClient
import struct DeskhandClient.Host

/// Host and session models decode what the daemon's encoders write.
final class DaemonHostFixtureTests: XCTestCase {
    private struct HostList: Decodable { let hosts: [Host] }

    func testHostListOfTheDaemonDecodes() throws {
        let list = try Wire.decode(HostList.self, from: DaemonFixture.data("host-list"))
        let host = try XCTUnwrap(list.hosts.first)
        XCTAssertEqual(host.hostID, "pc")
        XCTAssertEqual(host.state, "ready")
        XCTAssertEqual(host.connectionGeneration, 1)
        XCTAssertEqual(host.caps.pty?.shells, ["sh"])
        XCTAssertNil(host.session)
        XCTAssertNotNil(host.lastSeen)
    }

    func testSessionInspectWithoutALockStateDecodes() throws {
        let inspect = try Wire.decode(SessionInspect.self, from: DaemonFixture.data("session-inspect"))
        XCTAssertNil(inspect.session.locked)
        XCTAssertEqual(inspect.session.connectState, "unknown")
        XCTAssertEqual(inspect.connectionGeneration, 1)
        XCTAssertEqual(inspect.control?.owner, "none")
    }

    func testASessionThatReportsItsLockStateKeepsIt() throws {
        let session = try Wire.decode(SessionInfo.self, from: Data(#"""
        {"session_id":2,"user":"taeyoung","connect_state":"active","protocol":"console","locked":true,"input_desktop":"Winlogon"}
        """#.utf8))
        XCTAssertEqual(session.locked, true)
    }

    func testHostUpdatedEventCarriesTheNewGeneration() throws {
        let event = try XCTUnwrap(DaemonEvent(params: Wire.decode(JSONValue.self, from: DaemonFixture.data("event-host-updated"))))
        let host = try Wire.decode(Host.self, from: Wire.encode(try XCTUnwrap(event.payload["host"])))
        XCTAssertEqual(event.kind, "host.updated")
        XCTAssertEqual(host.connectionGeneration, 2)
        XCTAssertEqual(host.state, "ready")
    }

    func testConnectionStateEventsCarryTheirGeneration() throws {
        for (name, state, generation) in [("event-connection-state-offline", "offline", 1), ("event-connection-state-connecting", "connecting", 1),
                                          ("event-connection-state-ready", "ready", 2)] {
            let event = try XCTUnwrap(DaemonEvent(params: Wire.decode(JSONValue.self, from: DaemonFixture.data(name))))
            XCTAssertEqual(event.kind, "connection.state")
            XCTAssertEqual(event.hostID, "pc")
            XCTAssertEqual(event.payload["state"]?.stringValue, state)
            XCTAssertEqual(event.payload["connection_generation"]?.intValue, generation)
            XCTAssertGreaterThan(event.seq, 0)
        }
    }
}
