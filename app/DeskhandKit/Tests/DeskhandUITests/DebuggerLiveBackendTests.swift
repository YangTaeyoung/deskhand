import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandUI

/// `debug.*` over the local API socket, against `MockDaemon`.
final class DebuggerLiveBackendTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private static let envelope = #""schema_version":1,"request_id":"r-1","host_id":"windows-dev","connection_generation":4,"action":{"executed":true,"outcome":"done"},"error":null"#
    private static let session = #"{"debug_id":"d-1","host_id":"windows-dev","kind":"launch","name":"Shop Admin","ports":{"inspect":52011},"state":"running","created_at":"2026-10-20T10:00:00.000Z","targets":[{"id":"t-main","role":"main","type":"node","url":"file:///main.js","title":"main","attached":true,"paused":false,"gen":0}]}"#

    private func backend(_ daemon: MockDaemon) async throws -> (LiveBackend, DeskhandClient) {
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path)
        try await client.connect(kind: "app", name: "test", version: "0")
        return (LiveBackend(client: client), client)
    }

    func testLaunchAndAttachSendContractParamsAndReturnTheSession() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let envelope = Self.envelope
        let session = Self.session
        daemon.setHandler { request in
            switch request.method {
            case "debug.electron.launch", "debug.attach":
                return .result(#"{\#(envelope),"status":"succeeded","job_id":"j-1","session":\#(session)}"#)
            default:
                return nil
            }
        }
        let (backend, client) = try await backend(daemon)

        let launched = try await backend.debugLaunch(DebugLaunchRequest(
            hostID: dev, path: "C:\\Shop\\desktop\\Shop Admin.exe", args: ["--env=staging"], cwd: nil, pauseOnStart: true,
            breakpoints: [BreakpointSpec(match: .fileName, pattern: "checkout.js", line: 31, condition: "order.total > 0")],
            pauseOnExceptions: .all))
        XCTAssertEqual(launched.debugID, "d-1")
        XCTAssertEqual(launched.targets.first?.targetRole, .main)
        let launch = try XCTUnwrap(daemon.requests(method: "debug.electron.launch").first)
        XCTAssertEqual(launch.params["host_id"] as? String, dev)
        XCTAssertEqual(launch.params["path"] as? String, "C:\\Shop\\desktop\\Shop Admin.exe")
        XCTAssertEqual(launch.params["args"] as? [String], ["--env=staging"])
        XCTAssertEqual(launch.params["pause_on_start"] as? Bool, true)
        XCTAssertNil(launch.params["cwd"])
        XCTAssertEqual(launch.params["pause_on_exceptions"] as? String, "all")
        let specs = try XCTUnwrap(launch.params["breakpoints"] as? [[String: Any]])
        XCTAssertEqual(specs.first?["match"] as? String, "file_name")
        XCTAssertEqual(specs.first?["pattern"] as? String, "checkout.js")
        XCTAssertEqual(specs.first?["line"] as? Int, 31)
        XCTAssertEqual(specs.first?["condition"] as? String, "order.total > 0")

        _ = try await backend.debugAttach(DebugAttachRequest(hostID: dev, target: .port(9229, role: .main)))
        _ = try await backend.debugAttach(DebugAttachRequest(hostID: dev, target: .pid(7000)))
        let attaches = daemon.requests(method: "debug.attach")
        XCTAssertEqual(attaches.first?.params["port"] as? Int, 9229)
        XCTAssertEqual(attaches.first?.params["role"] as? String, "main")
        XCTAssertNil(attaches.first?.params["pid"])
        XCTAssertEqual(attaches.last?.params["pid"] as? Int, 7000)
        XCTAssertNil(attaches.last?.params["port"])
        await client.close()
    }

    func testFailedEnvelopesThrowWithTheirCode() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let envelope = Self.envelope
        daemon.setHandler { request in
            switch request.method {
            case "debug.electron.launch":
                return .result(#"{"schema_version":1,"status":"failed","action":{"executed":false,"outcome":"not_started"},"#
                    + #""error":{"code":"TIMEOUT","message":"20초 안에 디버그 포트가 열리지 않았습니다"},"next_action":null}"#)
            case "debug.step":
                return .result(#"{\#(envelope.replacingOccurrences(of: #","error":null"#, with: "")),"status":"failed","#
                    + #""error":{"code":"STALE_TARGET","message":"대상이 멈춰 있지 않습니다"},"next_action":"check_state"}"#)
            case "debug.variables":
                return .error(code: -32000, message: "개체가 더 이상 유효하지 않습니다", data: #"{"code":"STALE_TARGET","retryable":false}"#)
            default:
                return nil
            }
        }
        let (backend, client) = try await backend(daemon)
        do {
            _ = try await backend.debugLaunch(DebugLaunchRequest(hostID: dev, path: "C:\\a.exe"))
            XCTFail("failed launch throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "TIMEOUT")
        }
        do {
            _ = try await backend.debugStep(debugID: "d-1", targetID: "t", kind: .over)
            XCTFail("failed step throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "STALE_TARGET")
            XCTAssertEqual(OperationError(error).nextAction, "check_state")
        }
        do {
            _ = try await backend.debugVariables(debugID: "d-1", targetID: "t", objectID: "o", offset: 0, limit: 50)
            XCTFail("JSON-RPC error throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "STALE_TARGET")
        }
        await client.close()
    }

    func testExecutionControlBreakpointsEvaluateAndConsoleShapes() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let envelope = Self.envelope
        daemon.setHandler { request in
            let ok = #"\#(envelope),"status":"succeeded""#
            switch request.method {
            case "debug.resume":
                return .result(#"{\#(ok),"target":{"id":"t-page","role":"renderer","type":"page","url":"u","title":"결제","attached":true,"paused":false,"gen":8}}"#)
            case "debug.pause":
                return .result(#"{\#(ok)}"#)
            case "debug.breakpoint.set", "debug.breakpoint.update":
                return .result(#"{\#(ok),"breakpoint":{"breakpoint_id":"bp-1","match":"url_regex","pattern":"checkout\\.js$","line":42,"enabled":true,"resolved":false,"locations":[],"rev":3}}"#)
            case "debug.breakpoint.remove":
                return .result(#"{\#(ok),"breakpoint":{"breakpoint_id":"bp-1","match":"url_regex","pattern":"checkout\\.js$","line":42,"enabled":true,"resolved":false,"locations":[],"rev":4}}"#)
            case "debug.console.clear":
                return .result(#"{\#(ok),"cleared_through":30}"#)
            case "debug.evaluate":
                return .result(#"{\#(ok),"type":"number","value_preview":"46800","has_children":false}"#)
            case "debug.console":
                return .result(#"{"entries":[],"next_cursor":30,"has_more":false,"has_earlier":false,"dropped":0}"#)
            case "debug.state":
                return .result(#"{"target_id":"t-page","gen":7,"paused":true,"reason":"breakpoint","hit_breakpoints":["bp-1"],"call_frames":[]}"#)
            case "debug.close", "debug.exceptions", "debug.breakpoints.activate":
                return .result(#"{\#(ok)}"#)
            case "debug.list":
                return .result(#"{"sessions":[\#(Self.session)]}"#)
            default:
                return nil
            }
        }
        let (backend, client) = try await backend(daemon)

        let resumed = try await backend.debugResume(debugID: "d-1", targetID: "t-page")
        XCTAssertEqual(resumed?.gen, 8)
        XCTAssertEqual(resumed?.paused, false)
        let paused = try await backend.debugPause(debugID: "d-1", targetID: "t-page")
        XCTAssertNil(paused)
        XCTAssertEqual(daemon.requests(method: "debug.pause").first?.params["target_id"] as? String, "t-page")

        let breakpoint = try await backend.debugBreakpointSet(debugID: "d-1", spec: BreakpointSpec(
            match: .urlRegex, pattern: #"checkout\.js$"#, line: 42, condition: "order.total > 0"))
        XCTAssertEqual(breakpoint.breakpointID, "bp-1")
        XCTAssertEqual(breakpoint.matchKind, .urlRegex)
        XCTAssertEqual(breakpoint.rev, 3)
        let set = try XCTUnwrap(daemon.requests(method: "debug.breakpoint.set").first)
        XCTAssertEqual(set.params["match"] as? String, "url_regex")
        XCTAssertEqual(set.params["pattern"] as? String, #"checkout\.js$"#)
        XCTAssertNil(set.params["url"])
        XCTAssertEqual(set.params["line"] as? Int, 42)
        XCTAssertEqual(set.params["condition"] as? String, "order.total > 0")

        _ = try await backend.debugBreakpointUpdate(debugID: "d-1", breakpointID: "bp-1", enabled: false, condition: nil)
        let update = try XCTUnwrap(daemon.requests(method: "debug.breakpoint.update").first)
        XCTAssertEqual(update.params["breakpoint_id"] as? String, "bp-1")
        XCTAssertEqual(update.params["enabled"] as? Bool, false)
        XCTAssertNil(update.params["condition"])
        let removed = try await backend.debugBreakpointRemove(debugID: "d-1", breakpointID: "bp-1")
        XCTAssertEqual(removed?.rev, 4)
        let cleared = try await backend.debugConsoleClear(debugID: "d-1")
        XCTAssertEqual(cleared, 30)
        XCTAssertEqual(daemon.requests(method: "debug.console.clear").first?.params["debug_id"] as? String, "d-1")

        let value = try await backend.debugEvaluate(debugID: "d-1", targetID: "t-page", expression: "order.total", frameID: "cf-0")
        XCTAssertEqual(value.value.valuePreview, "46800")
        XCTAssertEqual(daemon.requests(method: "debug.evaluate").first?.params["frame_id"] as? String, "cf-0")

        let page = try await backend.debugConsole(ConsoleQuery(debugID: "d-1", cursor: 12, limit: 5000))
        XCTAssertEqual(page.nextCursor, 30)
        let console = try XCTUnwrap(daemon.requests(method: "debug.console").first)
        XCTAssertEqual(console.params["cursor"] as? Int, 12)
        XCTAssertEqual(console.params["limit"] as? Int, ConsoleQuery.maximumLimit)
        XCTAssertNil(console.params["before"])

        let state = try await backend.debugState(debugID: "d-1", targetID: "t-page")
        XCTAssertEqual(state.hitBreakpoints, ["bp-1"])

        _ = try await backend.debugExceptions(debugID: "d-1", mode: .all)
        XCTAssertEqual(daemon.requests(method: "debug.exceptions").first?.params["mode"] as? String, "all")
        _ = try await backend.debugBreakpointsActivate(debugID: "d-1", active: false)
        XCTAssertEqual(daemon.requests(method: "debug.breakpoints.activate").first?.params["active"] as? Bool, false)
        try await backend.debugClose(debugID: "d-1", keepProcess: true)
        XCTAssertEqual(daemon.requests(method: "debug.close").first?.params["keep_process"] as? Bool, true)

        let sessions = try await backend.debugList(hostID: dev)
        XCTAssertEqual(sessions.map(\.debugID), ["d-1"])
        XCTAssertEqual(daemon.requests(method: "debug.list").first?.params["host_id"] as? String, dev)
        await client.close()
    }
}
