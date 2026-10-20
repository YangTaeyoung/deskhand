import Foundation
import XCTest
@testable import DeskhandClient
import struct DeskhandClient.Host

private struct HostListResult: Decodable, Sendable { let hosts: [Host] }
private struct WaitTimeout: Error {}
private struct HostParams: Encodable, Sendable { let hostID: String }

final class ClientTests: XCTestCase {
    private func makeServer() throws -> MockDaemon {
        let server = MockDaemon()
        try server.start()
        return server
    }

    private func makeClient(_ server: MockDaemon) -> DeskhandClient {
        DeskhandClient(socketPath: server.path, reconnectInitialDelay: 0.02, reconnectMaxDelay: 0.1)
    }

    private func next<S: AsyncSequence & Sendable>(_ seq: S, timeout: TimeInterval = 5) async throws -> S.Element
    where S.Element: Sendable {
        try await withThrowingTaskGroup(of: S.Element?.self) { group in
            group.addTask {
                var it = seq.makeAsyncIterator()
                return try await it.next()
            }
            group.addTask {
                try await Task.sleep(for: .seconds(timeout))
                return nil
            }
            defer { group.cancelAll() }
            guard let first = try await group.next(), let value = first else {
                throw WaitTimeout()
            }
            return value
        }
    }

    func testHelloAndCall() async throws {
        let server = try makeServer()
        server.setHandler { req in
            req.method == "host.list" ? .result(#"{"hosts":[{"host_id":"win-1","name":"Win","state":"connected"}]}"#) : nil
        }
        let client = makeClient(server)
        try await client.connect(kind: "app", name: "Deskhand", version: "1.2.3")
        let hello = server.requests(method: "hello")
        XCTAssertEqual(hello.count, 1)
        XCTAssertEqual(hello[0].id, 1)
        XCTAssertEqual(hello[0].params["client"] as? String, "app")
        XCTAssertEqual(hello[0].params["name"] as? String, "Deskhand")
        XCTAssertEqual(hello[0].params["version"] as? String, "1.2.3")
        let clientID = await client.clientID
        XCTAssertEqual(clientID, "c-test")

        let result: HostListResult = try await client.call("host.list", NoParams())
        XCTAssertEqual(result.hosts.map(\.hostID), ["win-1"])
        let listed = server.requests(method: "host.list")
        XCTAssertEqual(listed[0].id, 2)

        let _: Empty = try await client.call("control.release", HostParams(hostID: "win-1"))
        XCTAssertEqual(server.requests(method: "control.release")[0].params["host_id"] as? String, "win-1")
        await client.close()
    }

    func testErrorMapsToRPCErrorInfo() async throws {
        let server = try makeServer()
        server.setHandler { req in
            switch req.method {
            case "input.click":
                return .error(code: -32000, message: "사람이 조작 중입니다.",
                              data: #"{"code":"CONTROL_HELD_BY_HUMAN","retryable":false,"next_action":"human_release"}"#)
            case "bare":
                return .error(code: -32601, message: "no such method", data: nil)
            default:
                return nil
            }
        }
        let client = makeClient(server)
        try await client.connect(kind: "app", name: "Deskhand", version: "1")

        do {
            let _: Empty = try await client.call("input.click", NoParams())
            XCTFail("expected error")
        } catch let e as RPCErrorInfo {
            XCTAssertEqual(e.code, "CONTROL_HELD_BY_HUMAN")
            XCTAssertEqual(e.message, "사람이 조작 중입니다.")
            XCTAssertFalse(e.retryable)
            XCTAssertEqual(e.nextAction, "human_release")
        }

        do {
            let _: Empty = try await client.call("bare", NoParams())
            XCTFail("expected error")
        } catch let e as RPCErrorInfo {
            XCTAssertEqual(e.code, "INTERNAL")
            XCTAssertEqual(e.message, "no such method")
            XCTAssertNil(e.nextAction)
        }
        await client.close()
    }

    func testEventsStream() async throws {
        let server = try makeServer()
        let client = makeClient(server)
        try await client.connect(kind: "app", name: "Deskhand", version: "1")
        let stream = client.events(kinds: ["control.changed"], hostID: "win-1")
        XCTAssertTrue(server.waitForRequests(method: "events.subscribe", count: 1))
        let sub = server.requests(method: "events.subscribe")[0]
        XCTAssertEqual(sub.params["kinds"] as? [String], ["control.changed"])
        XCTAssertEqual(sub.params["host_id"] as? String, "win-1")

        server.emit(#"{"seq":1,"ts":"2026-10-20T10:00:00Z","host_id":"other","kind":"control.changed"}"#)
        server.emit(#"{"seq":2,"ts":"2026-10-20T10:00:01Z","host_id":"win-1","kind":"jobs.changed"}"#)
        server.emit(#"{"seq":3,"ts":"2026-10-20T10:00:02Z","host_id":"win-1","kind":"control.changed","owner":"human"}"#)
        let ev = try await next(stream)
        XCTAssertEqual(ev.seq, 3)
        XCTAssertEqual(ev.hostID, "win-1")
        XCTAssertEqual(ev.kind, "control.changed")
        XCTAssertEqual(ev.payload["owner"], .string("human"))
        await client.close()
    }

    func testReconnectsAfterDaemonRestart() async throws {
        let server = try makeServer()
        server.setHandler { req in
            req.method == "slow" ? .result("{}") : nil
        }
        let client = makeClient(server)
        try await client.connect(kind: "app", name: "Deskhand", version: "1")
        let stream = client.events(kinds: ["a", "b"], hostID: nil)
        XCTAssertTrue(server.waitForRequests(method: "events.subscribe", count: 1))

        server.stop()
        try await Task.sleep(for: .milliseconds(100))
        try server.start()

        XCTAssertTrue(server.waitForRequests(method: "hello", count: 2))
        XCTAssertTrue(server.waitForRequests(method: "events.subscribe", count: 2))
        let resub = server.requests(method: "events.subscribe")[1]
        XCTAssertEqual(resub.params["kinds"] as? [String], ["a", "b"])
        XCTAssertNil(resub.params["host_id"])

        server.emit(#"{"seq":9,"ts":"t","host_id":"h","kind":"a"}"#)
        let ev = try await next(stream)
        XCTAssertEqual(ev.seq, 9)

        let result: Empty = try await client.call("slow", NoParams())
        _ = result
        await client.close()
    }

    func testInFlightCallFailsWithDisconnected() async throws {
        let server = try makeServer()
        let gate = DispatchSemaphore(value: 0)
        server.setHandler { req in
            if req.method == "hang" { gate.wait() }
            return nil
        }
        let client = makeClient(server)
        try await client.connect(kind: "app", name: "Deskhand", version: "1")
        let call = Task { () -> Bool in
            do {
                let _: Empty = try await client.call("hang", NoParams())
                return false
            } catch ClientError.disconnected {
                return true
            } catch {
                return false
            }
        }
        XCTAssertTrue(server.waitForRequests(method: "hang", count: 1))
        server.dropConnections()
        let failed = await call.value
        gate.signal()
        XCTAssertTrue(failed)
        await client.close()
    }

    func testCallBeforeConnectThrowsNotConnected() async throws {
        let client = DeskhandClient(socketPath: "/tmp/dhc-none/d.sock")
        do {
            let _: Empty = try await client.call("host.list", NoParams())
            XCTFail("expected error")
        } catch let e as ClientError {
            XCTAssertEqual(e, .notConnected)
        }
    }

    func testStreamFramesParse() async throws {
        let server = try makeServer()
        func frame(_ kind: UInt8, _ payload: [UInt8]) -> Data {
            var d = Data()
            var len = UInt32(1 + payload.count).bigEndian
            withUnsafeBytes(of: &len) { d.append(contentsOf: $0) }
            d.append(kind)
            d.append(contentsOf: payload)
            return d
        }
        var wire = Data()
        wire.append(frame(0x01, [1, 2, 3, 4, 5]))
        wire.append(frame(0x30, Array(#"{"a":1}"#.utf8)))
        wire.append(frame(0x11, []))
        let bytes = wire
        server.setAttachHandler { conn in
            // 3-byte slices split headers and bodies across writes.
            var i = 0
            while i < bytes.count {
                let end = min(i + 3, bytes.count)
                conn.sendRaw(bytes.subdata(in: i..<end))
                i = end
                usleep(1000)
            }
            conn.shutdownConnection()
        }
        let stream = try await StreamConnection.attach(
            kind: "screen", params: ["host_id": "win-1", "display": 0, "preset": "balanced"],
            socketPath: server.path)
        let attach = server.requests(method: "stream.attach")
        XCTAssertEqual(attach.count, 1)
        XCTAssertEqual(attach[0].params["kind"] as? String, "screen")
        XCTAssertEqual(attach[0].params["host_id"] as? String, "win-1")
        XCTAssertEqual(server.requests(method: "hello")[0].params["client"] as? String, "app")

        var got: [(UInt8, Data)] = []
        for try await f in stream.frames { got.append((f.kind, f.payload)) }
        XCTAssertEqual(got.count, 3)
        XCTAssertEqual(got[0].0, 0x01)
        XCTAssertEqual([UInt8](got[0].1), [1, 2, 3, 4, 5])
        XCTAssertEqual(got[1].0, 0x30)
        XCTAssertEqual(String(data: got[1].1, encoding: .utf8), #"{"a":1}"#)
        XCTAssertEqual(got[2].0, 0x11)
        XCTAssertEqual(got[2].1.count, 0)
    }

    func testStreamSendWritesFrame() async throws {
        let server = try makeServer()
        let received = Received()
        server.setAttachHandler { conn in
            var buf = Data()
            while buf.count < 8, let c = conn.readChunk() { buf.append(c) }
            received.set(buf)
        }
        let stream = try await StreamConnection.attach(kind: "pty", params: ["pty_id": "p1"], socketPath: server.path)
        try await stream.send(kind: 0x20, payload: Data("ls\n".utf8))
        let deadline = Date().addingTimeInterval(5)
        while received.get().count < 8 && Date() < deadline { try await Task.sleep(for: .milliseconds(5)) }
        XCTAssertEqual([UInt8](received.get()), [0, 0, 0, 4, 0x20, 0x6C, 0x73, 0x0A])
        stream.close()
    }

    func testStreamAttachFailsWhenSocketMissing() async throws {
        let server = MockDaemon()
        try server.start()
        server.stop()
        do {
            _ = try await StreamConnection.attach(kind: "pty", params: [:], socketPath: server.path)
            XCTFail("expected error")
        } catch let e as ClientError {
            guard case .connectFailed = e else { return XCTFail("\(e)") }
        }
    }

    func testSnakeCaseModels() throws {
        let hostJSON = #"""
        {"hosts":[{"host_id":"windows-dev","name":"Windows Dev","os":"windows","arch":"amd64",
         "address":"10.0.0.5","state":"connected",
         "caps":{"screen":{"state":"ready"},"input":{"state":"needs_user","reason":"UAC"}},
         "session":{"session_id":"s-1","user":"dev","connect_state":"active","protocol":"console",
                    "locked":false,"input_desktop":"Default","integrity":"medium"},
         "owner":"human","last_seen":"2026-10-20T10:00:00Z"}]}
        """#
        let hosts = try Wire.decode(HostListResult.self, from: Data(hostJSON.utf8))
        let h = try XCTUnwrap(hosts.hosts.first)
        XCTAssertEqual(h.hostID, "windows-dev")
        XCTAssertEqual(h.caps?.screen?.state, "ready")
        XCTAssertEqual(h.caps?.input?.reason, "UAC")
        XCTAssertEqual(h.session?.sessionID, "s-1")
        XCTAssertEqual(h.session?.connectState, "active")
        XCTAssertEqual(h.session?.inputDesktop, "Default")
        XCTAssertEqual(h.owner, "human")
        XCTAssertNotNil(h.lastSeen?.date)

        let control = try Wire.decode(ControlState.self, from: Data(#"{"owner":"ai","owner_client":"c-9","gen":4,"ai_paused":true}"#.utf8))
        XCTAssertEqual(control, ControlState(owner: "ai", ownerClient: "c-9", gen: 4, aiPaused: true))

        struct Payload: Decodable, Sendable { let code: Int; let stdout: String }
        let env = try Wire.decode(Envelope<Payload>.self, from: Data(#"""
        {"schema_version":1,"request_id":"r-1","job_id":"j-1","host_id":"windows-dev","session_id":"s-1",
         "connection_generation":4,"status":"succeeded","action":{"executed":true,"outcome":"done"},
         "error":null,"next_action":null,"artifacts":[{"path":"/tmp/a.png","kind":"image"}],
         "code":0,"stdout":"hi"}
        """#.utf8))
        XCTAssertEqual(env.schemaVersion, 1)
        XCTAssertEqual(env.requestID, "r-1")
        XCTAssertEqual(env.jobID, "j-1")
        XCTAssertEqual(env.hostID, "windows-dev")
        XCTAssertEqual(env.sessionID, "s-1")
        XCTAssertEqual(env.connectionGeneration, 4)
        XCTAssertEqual(env.status, .succeeded)
        XCTAssertEqual(env.action?.executed, true)
        XCTAssertEqual(env.action?.outcome, "done")
        XCTAssertNil(env.error)
        XCTAssertEqual(env.artifacts.first?.path, "/tmp/a.png")
        XCTAssertEqual(env.payload.code, 0)
        XCTAssertEqual(env.payload.stdout, "hi")

        let target = try Wire.decode(DebugLaunch.self, from: Data(#"""
        {"debug_id":"d-1","pid":42,"targets":[{"id":"t1","role":"main","url":"file:///a","title":"A"}]}
        """#.utf8))
        XCTAssertEqual(target.debugID, "d-1")
        XCTAssertEqual(target.targets.first?.role, "main")

        let state = try Wire.decode(DebugState.self, from: Data(#"""
        {"paused":true,"reason":"breakpoint","call_frames":[{"function_name":"f","url":"u","line":3,"column":7}]}
        """#.utf8))
        XCTAssertEqual(state.callFrames.first, CallFrame(functionName: "f", url: "u", line: 3, column: 7))
    }

    func testPathsHonorDeskhandHome() {
        XCTAssertTrue(Paths.socket(home: URL(fileURLWithPath: "/x/y")).hasSuffix("/x/y/run/d.sock"))
        XCTAssertEqual(Paths.home(environment: ["DESKHAND_HOME": "/tmp/dh"]).path, "/tmp/dh")
        XCTAssertTrue(Paths.home(environment: [:]).path.hasSuffix("Library/Application Support/Deskhand"))
    }

    func testLauncherSkipsLaunchWhenSocketResponds() async throws {
        let server = try makeServer()
        let counter = Received()
        let launcher = DaemonLauncher(socketPath: server.path, launch: { counter.set(Data([1])) })
        try await launcher.ensureRunning()
        XCTAssertEqual(counter.get().count, 0)
    }

    func testLauncherStartsDaemonAndWaitsForSocket() async throws {
        let server = MockDaemon()
        let launcher = DaemonLauncher(socketPath: server.path, launch: {
            try server.start()
        }, timeout: 3, pollInterval: 0.01)
        try await launcher.ensureRunning()
        XCTAssertTrue(FileManager.default.fileExists(atPath: server.path))
    }

    func testLauncherThrowsDaemonUnavailableOnTimeout() async throws {
        let server = MockDaemon()
        let launcher = DaemonLauncher(socketPath: server.path, launch: {}, timeout: 0.2, pollInterval: 0.01)
        do {
            try await launcher.ensureRunning()
            XCTFail("expected error")
        } catch ClientError.daemonUnavailable(let logPath) {
            XCTAssertTrue(logPath.hasSuffix("daemon.log"))
        }
    }
}

final class Received: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    func set(_ d: Data) { lock.lock(); data = d; lock.unlock() }
    func get() -> Data { lock.lock(); defer { lock.unlock() }; return data }
}
