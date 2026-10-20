import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandUI

/// `proc.*` and `diag.*` over the local API socket, against `MockDaemon`.
final class DiagnosticsLiveBackendTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private let envelope = #""schema_version":1,"request_id":"r-1","host_id":"windows-dev","connection_generation":4"#

    private func backend(_ daemon: MockDaemon) async throws -> (LiveBackend, DeskhandClient) {
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path)
        try await client.connect(kind: "app", name: "test", version: "0")
        return (LiveBackend(client: client), client)
    }

    func testProcKillMapsEachEnvelopeStatus() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let envelope = self.envelope
        daemon.setHandler { request in
            guard request.method == "proc.kill" else { return nil }
            switch request.params["pid"] as? Int {
            case 1:
                return .result(#"{\#(envelope),"status":"succeeded","action":{"executed":true,"outcome":"done"},"error":null,"killed":4}"#)
            case 2:
                return .result(#"{\#(envelope),"status":"failed","action":{"executed":false,"outcome":"not_started"},"#
                    + #""error":{"code":"PERMISSION_DENIED","message":"Windows가 거부했습니다","retryable":false},"next_action":null}"#)
            case 3:
                return .result(#"{\#(envelope),"status":"unknown","action":{"executed":null,"outcome":"unknown"},"#
                    + #""error":{"code":"EXECUTION_OUTCOME_UNKNOWN","message":"응답을 잃었습니다"},"next_action":"check_state"}"#)
            case 4:
                return .result(#"{"killed":1}"#)
            default:
                return .error(code: -32000, message: "프로세스를 찾을 수 없습니다", data: #"{"code":"NOT_FOUND","retryable":false}"#)
            }
        }
        let (backend, client) = try await backend(daemon)

        let started = try XCTUnwrap(Timestamp(string: "2026-10-20T09:12:00.000Z"))
        let done = try await backend.procKill(hostID: dev, pid: 1, started: started, tree: true)
        XCTAssertEqual(done, KillReceipt(status: .succeeded, killed: 4))
        let request = try XCTUnwrap(daemon.requests(method: "proc.kill").first)
        XCTAssertEqual(request.params["host_id"] as? String, dev)
        XCTAssertEqual(request.params["tree"] as? Bool, true)
        XCTAssertEqual(request.params["started"] as? String, "2026-10-20T09:12:00.000Z")

        let refused = try await backend.procKill(hostID: dev, pid: 2, started: nil, tree: false)
        XCTAssertEqual(refused.status, .failed)
        XCTAssertEqual(refused.error?.code, "PERMISSION_DENIED")
        XCTAssertEqual(refused.error?.message, "Windows가 거부했습니다")

        let lost = try await backend.procKill(hostID: dev, pid: 3, started: nil, tree: false)
        XCTAssertEqual(lost.status, .unknown)
        XCTAssertEqual(lost.error?.code, "EXECUTION_OUTCOME_UNKNOWN")
        XCTAssertEqual(lost.error?.nextAction, "check_state")

        let bare = try await backend.procKill(hostID: dev, pid: 4, started: nil, tree: false)
        XCTAssertEqual(bare, KillReceipt(status: .succeeded, killed: 1))
        XCTAssertNil(daemon.requests(method: "proc.kill").last?.params["started"])

        do {
            _ = try await backend.procKill(hostID: dev, pid: 5, started: nil, tree: false)
            XCTFail("JSON-RPC errors throw")
        } catch {
            XCTAssertEqual(OperationError(error).code, "NOT_FOUND")
        }
        await client.close()
    }

    func testDiagEventsSendsTheQueryAndDiagCallsThrowFailedEnvelopes() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let envelope = self.envelope
        daemon.setHandler { request in
            switch request.method {
            case "diag.events":
                return .result(#"{\#(envelope),"status":"succeeded","error":null,"truncated":true,"events":["#
                    + #"{"record_id":9,"time":"2026-10-20T10:00:00.000Z","level":"error","provider":"disk","event_id":153,"message":"m"}]}"#)
            case "diag.system":
                return .result(#"{\#(envelope),"status":"failed","action":{"executed":false,"outcome":"not_started"},"#
                    + #""error":{"code":"OFFLINE","message":"개발 PC에 연결되어 있지 않습니다"}}"#)
            case "diag.crashes":
                return .result(#"{\#(envelope),"status":"succeeded","error":null,"dumps":[]}"#)
            case "proc.list":
                return .result(#"{"procs":[{"pid":4,"ppid":0,"name":"System","mem_kb":144,"cpu_pct":0.1,"started_by_deskhand":false}]}"#)
            default:
                return nil
            }
        }
        let (backend, client) = try await backend(daemon)
        let since = try XCTUnwrap(Timestamp(string: "2026-10-20T09:00:00.000Z")).date
        let page = try await backend.diagEvents(EventLogQuery(
            hostID: dev, log: .application, since: since, levels: [.critical, .error], provider: "disk", max: 5000))
        XCTAssertTrue(page.truncated)
        XCTAssertEqual(page.events.first?.recordID, 9)
        let request = try XCTUnwrap(daemon.requests(method: "diag.events").first)
        XCTAssertEqual(request.params["host_id"] as? String, dev)
        XCTAssertEqual(request.params["log"] as? String, "Application")
        XCTAssertEqual(request.params["since"] as? String, "2026-10-20T09:00:00.000Z")
        XCTAssertNil(request.params["until"])
        XCTAssertEqual(request.params["levels"] as? [String], ["critical", "error"])
        XCTAssertEqual(request.params["provider"] as? String, "disk")
        XCTAssertEqual(request.params["max"] as? Int, 1000)

        do {
            _ = try await backend.diagSystem(hostID: dev)
            XCTFail("a failed envelope throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "OFFLINE")
        }
        let dumps = try await backend.diagCrashes(hostID: dev, since: nil)
        XCTAssertTrue(dumps.isEmpty)
        XCTAssertNil(daemon.requests(method: "diag.crashes").first?.params["since"])
        let procs = try await backend.procList(hostID: dev)
        XCTAssertEqual(procs.map(\.name), ["System"])
        await client.close()
    }
}
