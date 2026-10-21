import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// The debugger over what the daemon's debugger answers (local-api.md §9): pid attach on Windows,
/// the renderer still being attached after a `pause_on_start` launch, pending pauses, CDP
/// refusals and engine-checked `url_regex`.
@MainActor
final class DebuggerContractTests: XCTestCase {
    private func connected(_ daemon: MockDaemon) async throws -> (LiveBackend, DeskhandClient) {
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path, reconnectInitialDelay: 0.02, reconnectMaxDelay: 0.1)
        try await client.connect(kind: "app", name: "test", version: "0")
        return (LiveBackend(client: client), client)
    }

    private func object(_ name: String) throws -> [String: Any] {
        try XCTUnwrap(JSONSerialization.jsonObject(with: DaemonFixture.data(name)) as? [String: Any])
    }

    private func json(_ object: Any) throws -> String {
        String(decoding: try JSONSerialization.data(withJSONObject: object, options: [.withoutEscapingSlashes]), as: UTF8.self)
    }

    private func event(_ name: String) throws -> DaemonEvent {
        let value = try JSONDecoder().decode(JSONValue.self, from: DaemonFixture.data(name))
        guard case .object(let payload) = value else { throw DecodingError.typeMismatch(JSONValue.self, .init(codingPath: [], debugDescription: "")) }
        return DaemonEvent(seq: payload["seq"]?.intValue ?? 0, hostID: payload["host_id"]?.stringValue, kind: payload["kind"]?.stringValue ?? "",
                           payload: payload)
    }

    private func host(os: String) -> Host {
        Host(hostID: "h", name: "개발 PC", os: os, arch: "amd64", address: "a", port: 1, sshAlias: "", state: "ready", caps: HostCaps(),
             owner: "none", connectionGeneration: 1)
    }

    // MARK: Attach

    func testWindowsHostsAttachByPortAndProcessesWithAnInspectorPortAttachThroughIt() {
        XCTAssertEqual(AttachSheet.defaultMode(for: host(os: "windows")), .port)
        XCTAssertEqual(AttachSheet.defaultMode(for: host(os: "linux")), .process)
        XCTAssertEqual(AttachCandidate.inspectorPorts("\"node.exe\" --inspect=9230 C:\\worker\\index.js"), AttachCandidate.Ports(inspect: 9230))
        XCTAssertEqual(AttachCandidate.inspectorPorts("node --inspect-brk=127.0.0.1:9231 main.js"), AttachCandidate.Ports(inspect: 9231))
        XCTAssertEqual(AttachCandidate.inspectorPorts("node --inspect main.js"), AttachCandidate.Ports(inspect: 9229))
        XCTAssertEqual(AttachCandidate.inspectorPorts("node --inspect --inspect-port=9333 main.js"), AttachCandidate.Ports(inspect: 9333))
        XCTAssertEqual(AttachCandidate.inspectorPorts("\"Shop.exe\" --remote-debugging-port=9222"), AttachCandidate.Ports(remoteDebugging: 9222))
        XCTAssertEqual(AttachCandidate.inspectorPorts("\"Code.exe\""), AttachCandidate.Ports())
        let process = RemoteProcess(pid: 9330, ppid: 1, name: "node.exe", cmd: "\"node.exe\" --inspect=9230 C:\\worker\\index.js",
                                    memKb: 1, startedByDeskhand: false)
        let candidate = AttachCandidate(process: process, kind: .node, inspectorOpen: true)
        XCTAssertEqual(AttachSheet.portRequest(for: candidate, hostID: "h"), DebugAttachRequest(hostID: "h", target: .port(9230, role: .main)))
        let plain = AttachCandidate(process: RemoteProcess(pid: 7000, ppid: 1, name: "Code.exe", cmd: "\"Code.exe\"", memKb: 1,
                                                           startedByDeskhand: false),
                                    kind: .electron, inspectorOpen: false)
        XCTAssertNil(AttachSheet.portRequest(for: plain, hostID: "h"))
    }

    func testAPidAttachTheWindowsAgentRefusesReadsAsTheAppsOwnExplanation() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let refused = try DaemonFixture.line("debug-attach-pid-unsupported")
        daemon.setHandler { request in request.method == "debug.attach" ? .result(refused) : nil }
        let (backend, client) = try await connected(daemon)
        do {
            _ = try await backend.debugAttach(DebugAttachRequest(hostID: "h-fake", target: .pid(4120)))
            XCTFail("the refused attach throws")
        } catch {
            let failure = OperationError(error)
            XCTAssertEqual(failure.code, "CAPABILITY_UNSUPPORTED")
            let shown = AttachSheet.failure(failure, byPID: true, windows: true)
            XCTAssertEqual(shown.message, "Windows PC에서는 실행 중인 프로세스에 PID로 연결할 수 없습니다")
            XCTAssertEqual(AttachSheet.fix(shown, byPID: true, windows: true),
                           "Windows에서는 포트로 연결하세요. 앱을 --inspect=포트나 --remote-debugging-port=포트로 실행한 뒤 그 포트로 연결하거나, ‘앱 실행’으로 디버그하세요.")
            XCTAssertEqual(AttachSheet.failure(failure, byPID: false, windows: true).message, failure.message)
        }
        await client.close()
    }

    // MARK: Renderer still attaching

    func testARendererStillBeingAttachedIsWaitingNotAFailure() throws {
        let envelope = try object("debug-launch-waiting")
        let session = try LiveBackend.decode(DebugSession.self, from: JSONDecoder().decode(JSONValue.self, from: Data(json(envelope["session"] as Any).utf8)))
        XCTAssertEqual(session.rendererUnavailable, .waiting)
        XCTAssertEqual(DebugFormat.rendererUnavailableShort(.waiting), "렌더러 연결 중")
        XCTAssertFalse(RendererUnavailableReason.waiting.isFailure)
        XCTAssertTrue(RendererUnavailableReason.remoteDebuggingDisabled.isFailure)
        let final = try LiveBackend.decode(DebugSession.self, from: JSONDecoder().decode(JSONValue.self, from: DaemonFixture.data("debug-session-renderer-gave-up")))
        XCTAssertEqual(final.rendererUnavailable, .remoteDebuggingDisabled)
        XCTAssertGreaterThan(final.rev, session.rev)
    }

    // MARK: Pending pause, refusals

    func testAPendingPauseRaisesTheGenAndAResumeCancelsIt() throws {
        let model = DebuggerModel(hostID: "h-fake", backend: PreviewBackend(stepDelay: .zero),
                                  defaults: UserDefaults(suiteName: "debugger-contract-\(UUID())")!)
        let envelope = try object("debug-launch-waiting")
        var info = try LiveBackend.decode(DebugSession.self, from: JSONDecoder().decode(JSONValue.self, from: Data(json(envelope["session"] as Any).utf8)))
        let pending = try event("event-debug-target-changed-pending")
        info.debugID = try XCTUnwrap(pending.payload["debug_id"]?.stringValue)
        let session = model.makeSession(info)
        model.seed([session])
        model.handle(pending)
        XCTAssertEqual(session.info.targets.first?.pausePending, true)
        XCTAssertEqual(session.info.targets.first?.gen, 1)
        XCTAssertTrue(session.pausePending)
        model.handle(try event("event-debug-target-changed-cancelled"))
        XCTAssertEqual(session.info.targets.first?.pausePending, false)
        XCTAssertEqual(session.info.targets.first?.gen, 2)
        XCTAssertFalse(session.pausePending)
    }

    func testCDPRefusalsLeaveResumeSucceededAndFailPauseAndStep() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let pause = try DaemonFixture.line("debug-pause-refused")
        let resume = try DaemonFixture.line("debug-resume-refused-by-cdp")
        let step = try DaemonFixture.line("debug-step-refused")
        daemon.setHandler { request in
            switch request.method {
            case "debug.pause": .result(pause)
            case "debug.resume": .result(resume)
            case "debug.step": .result(step)
            default: nil
            }
        }
        let (backend, client) = try await connected(daemon)
        do {
            _ = try await backend.debugPause(debugID: "d", targetID: "node-1")
            XCTFail("a refused pause throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "INTERNAL")
        }
        let resumed = try await backend.debugResume(debugID: "d", targetID: "node-1")
        XCTAssertEqual(resumed?.gen, 4)
        XCTAssertEqual(resumed?.paused, false)
        do {
            _ = try await backend.debugStep(debugID: "d", targetID: "node-1", kind: .over)
            XCTFail("a refused step throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "INTERNAL")
        }
        await client.close()
    }

    func testAnURLRegexTheEnginesRejectsShowsTheEnginesMessage() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let refusal = try object("debug-breakpoint-bad-regex")
        let message = try XCTUnwrap(refusal["message"] as? String)
        let data = try json(refusal["data"] as Any)
        daemon.setHandler { request in
            request.method == "debug.breakpoint.set" ? .error(code: -32000, message: message, data: data) : nil
        }
        let (backend, client) = try await connected(daemon)
        do {
            _ = try await backend.debugBreakpointSet(debugID: "d", spec: BreakpointSpec(match: .urlRegex, pattern: "(", line: 3))
            XCTFail("the engine's refusal throws")
        } catch {
            let shown = BreakpointSheet.failure(OperationError(error))
            XCTAssertEqual(shown.code, "INVALID_ARGUMENT")
            XCTAssertEqual(shown.message, "대상의 JavaScript 엔진이 이 정규식을 받지 않습니다: SyntaxError: Invalid regular expression: /(/: Unterminated group")
        }
        await client.close()
    }

    // MARK: Reading again after a reconnect

    func testAReloadReadsThePauseAndTheConsoleThroughTheDebugAPI() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        var session = try XCTUnwrap(object("debug-launch-waiting")["session"] as? [String: Any])
        session["host_id"] = "h-fake"
        session["state"] = "paused"
        var target = try XCTUnwrap((session["targets"] as? [[String: Any]])?.first)
        target["paused"] = true
        target["gen"] = 3
        session["targets"] = [target]
        session["rev"] = 9
        let list = try json(["sessions": [session]])
        let console = try DaemonFixture.line("debug-console-page")
        let state = try json(["target_id": "node-1", "gen": 3, "paused": true, "reason": "breakpoint", "hit_breakpoints": [],
                              "call_frames": [["frame_id": "f0", "function": "calcTotal", "script_id": "s1", "url": "file:///app/main.js",
                                               "line": 10, "column": 3, "scope_chain": []]]])
        daemon.setHandler { request in
            switch request.method {
            case "debug.list": .result(list)
            case "debug.console": .result(console)
            case "debug.state": .result(state)
            case "debug.scripts": .result(#"{"scripts":[]}"#)
            default: nil
            }
        }
        let (backend, client) = try await connected(daemon)
        let model = DebuggerModel(hostID: "h-fake", backend: backend, defaults: UserDefaults(suiteName: "debugger-contract-\(UUID())")!)
        await model.reload()
        let loaded = try XCTUnwrap(model.sessions.first)
        try await waitUntil { loaded.pauseState?.gen == 3 }
        try await waitUntil { !daemon.requests(method: "debug.console").isEmpty }
        XCTAssertEqual(daemon.requests(method: "debug.state").first?.params["target_id"] as? String, "node-1")

        await model.reload()
        try await waitUntil { daemon.requests(method: "debug.console").count >= 2 }
        XCTAssertEqual(daemon.requests(method: "debug.console").last?.params["cursor"] as? Int, 1,
                       "a second read asks for what came after the last entry it holds")
        await client.close()
    }
}
