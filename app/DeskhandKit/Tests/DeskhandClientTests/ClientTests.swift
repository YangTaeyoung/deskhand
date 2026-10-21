import Foundation
import XCTest
@testable import DeskhandClient
import DeskhandTestSupport
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
            req.method == "host.list" ? .result(#"{"hosts":[{"host_id":"win-1","name":"Win","os":"windows","arch":"amd64","address":"a","port":1,"ssh_alias":"","state":"connected","caps":{},"displays":[],"owner":"none","connection_generation":1}]}"#) : nil
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
        let daemonVersion = await client.daemonVersion
        let apiVersion = await client.apiVersion
        XCTAssertEqual(daemonVersion, "0.4.2")
        XCTAssertEqual(apiVersion, 1)

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

        server.emit(#"{"seq":1,"ts":"2026-10-20T10:00:00.000Z","host_id":"other","kind":"control.changed"}"#)
        server.emit(#"{"seq":2,"ts":"2026-10-20T10:00:01.000Z","host_id":"win-1","kind":"jobs.changed"}"#)
        server.emit(#"{"seq":3,"ts":"2026-10-20T10:00:02.000Z","host_id":"win-1","kind":"control.changed","owner":"human"}"#)
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
         "address":"10.0.0.5","port":47600,"ssh_alias":"win","state":"connected",
         "caps":{"screen":{"state":"ready"},"input":{"state":"needs_user","reason":"UAC"},"future_cap":{"state":"ready"}},
         "session":{"session_id":3,"user":"dev","connect_state":"active","protocol":"console",
                    "locked":false,"input_desktop":"Default","integrity":"medium"},
         "displays":[{"index":0,"name":"D1","adapter":"GPU","x":0,"y":0,"width":1920,"height":1080,"dpi":144,"scale":1.5,"primary":true,"refresh_hz":60}],
         "owner":"human","owner_client":"c-1","agent_version":"0.1.0","connection_generation":4,
         "last_seen":"2026-10-20T10:00:00.123Z"}]}
        """#
        let hosts = try Wire.decode(HostListResult.self, from: Data(hostJSON.utf8))
        let h = try XCTUnwrap(hosts.hosts.first)
        XCTAssertEqual(h.hostID, "windows-dev")
        XCTAssertEqual(h.port, 47600)
        XCTAssertEqual(h.sshAlias, "win")
        XCTAssertEqual(h.caps.screen?.state, "ready")
        XCTAssertEqual(h.caps.input?.reason, "UAC")
        XCTAssertEqual(h.caps["future_cap"]?.state, "ready")
        XCTAssertEqual(h.session?.sessionID, 3)
        XCTAssertEqual(h.session?.connectState, "active")
        XCTAssertEqual(h.session?.protocolKind, "console")
        XCTAssertEqual(h.session?.inputDesktop, "Default")
        XCTAssertEqual(h.displays.first?.refreshHz, 60)
        XCTAssertEqual(h.displays.first?.scale, 1.5)
        XCTAssertEqual(h.owner, "human")
        XCTAssertEqual(h.ownerClient, "c-1")
        XCTAssertEqual(h.agentVersion, "0.1.0")
        XCTAssertEqual(h.connectionGeneration, 4)
        XCTAssertEqual(h.lastSeen?.date.timeIntervalSince1970 ?? 0, 1_792_490_400.123, accuracy: 0.001)

        XCTAssertThrowsError(try Wire.decode(Timestamp.self, from: Data(#""2026-10-20T10:00:00Z""#.utf8)))

        let inspect = try Wire.decode(SessionInspect.self, from: Data(#"""
        {"session_id":3,"user":"dev","connect_state":"active","protocol":"rdp","locked":true,"input_desktop":"Winlogon",
         "displays":[],"caps":{"exec":{"state":"ready"}},"owner":"ai","owner_client":"c-2","gen":2,"ai_paused":false,
         "connection_generation":7}
        """#.utf8))
        XCTAssertEqual(inspect.session.sessionID, 3)
        XCTAssertEqual(inspect.session.locked, true)
        XCTAssertEqual(inspect.caps.exec?.state, "ready")
        XCTAssertEqual(inspect.control, ControlState(owner: "ai", ownerClient: "c-2", gen: 2, aiPaused: false))
        XCTAssertEqual(inspect.connectionGeneration, 7)

        let control = try Wire.decode(ControlState.self, from: Data(#"{"owner":"ai","owner_client":"c-9","gen":4,"ai_paused":true}"#.utf8))
        XCTAssertEqual(control, ControlState(owner: "ai", ownerClient: "c-9", gen: 4, aiPaused: true))

        struct Payload: Decodable, Sendable { let code: Int; let stdout: String }
        let env = try Wire.decode(Envelope<Payload>.self, from: Data(#"""
        {"schema_version":1,"request_id":"r-1","job_id":"j-1","host_id":"windows-dev","session_id":"s-1",
         "connection_generation":4,"status":"succeeded","action":{"executed":true,"outcome":"done"},
         "error":null,"next_action":null,
         "artifacts":[{"artifact_id":"a-1","job_id":"j-1","kind":"screenshot","name":"a.png","path":"/tmp/a.png","size":10,
                       "mime":"image/png","sha256":"ab","created_at":"2026-10-20T10:00:00.000Z"}],
         "code":0,"stdout":"hi"}
        """#.utf8))
        XCTAssertEqual(env.schemaVersion, 1)
        XCTAssertEqual(env.requestID, "r-1")
        XCTAssertEqual(env.jobID, "j-1")
        XCTAssertEqual(env.hostID, "windows-dev")
        XCTAssertEqual(env.connectionGeneration, 4)
        XCTAssertEqual(env.status, .succeeded)
        XCTAssertEqual(env.action?.outcome, "done")
        XCTAssertNil(env.error)
        XCTAssertEqual(env.artifacts.first?.artifactID, "a-1")
        XCTAssertEqual(env.artifacts.first?.jobID, "j-1")
        XCTAssertEqual(env.payload.stdout, "hi")

        let file = try Wire.decode(FileEntry.self, from: Data(#"""
        {"name":"a","path":"C:\\a","dir":false,"size":5,"mtime":"2026-10-20T10:00:00.000Z","link":true,"hidden":true}
        """#.utf8))
        XCTAssertEqual(file.link, true)
        XCTAssertEqual(file.hidden, true)
        XCTAssertNil(file.readonly)

        let job = try Wire.decode(Job.self, from: Data(#"""
        {"job_id":"j","host_id":"h","kind":"exec","status":"running","summary":"s","progress":0.5,
         "created_at":"2026-10-20T10:00:00.000Z","updated_at":"2026-10-20T10:00:01.000Z","result":{"code":0}}
        """#.utf8))
        XCTAssertEqual(job.progress, 0.5)
        XCTAssertEqual(job.result, .object(["code": .int(0)]))
        XCTAssertNil(job.finishedAt)

        let transfer = try Wire.decode(Transfer.self, from: Data(#"""
        {"transfer_id":"t","host_id":"h","direction":"push","local":"/a","remote":"C:\\a","on_conflict":"rename","state":"running",
         "bytes_done":1,"bytes_total":2,"files_done":0,"files_total":1,"rate_bps":100,"rev":3,"updated_at":"2026-10-20T10:00:02.000Z",
         "files":[{"rel":"a","size":2,"bytes_done":1,"state":"running"}]}
        """#.utf8))
        XCTAssertEqual(transfer.onConflict, "rename")
        XCTAssertEqual(transfer.rateBps, 100)
        XCTAssertEqual(transfer.files.first?.rel, "a")
        XCTAssertNil(transfer.error)
        XCTAssertEqual(transfer.rev, 3)
        XCTAssertEqual(transfer.updatedAt, Timestamp(string: "2026-10-20T10:00:02.000Z"))

        let failed = try Wire.decode(Transfer.self, from: Data(#"""
        {"transfer_id":"t","host_id":"h","direction":"pull","local":"/a","remote":"C:\\a","on_conflict":"fail","state":"failed",
         "bytes_done":0,"bytes_total":2,"files_done":0,"files_total":2,"rate_bps":0,"outcome":"partial","rev":9,"updated_at":"2026-10-20T10:00:09.000Z",
         "error":{"code":"DISK_FULL","message":"공간이 부족합니다","next_action":"free_space"},
         "files":[{"rel":"a","size":1,"bytes_done":0,"state":"failed","error":{"code":"PERMISSION_DENIED","message":"거부됨"}},
                  {"rel":"b","size":1,"bytes_done":0,"state":"failed","error":"옛 형식"}]}
        """#.utf8))
        XCTAssertEqual(failed.error, EnvelopeError(code: "DISK_FULL", message: "공간이 부족합니다", nextAction: "free_space"))
        XCTAssertEqual(failed.files.first?.error?.code, "PERMISSION_DENIED")
        XCTAssertEqual(failed.files.last?.error, EnvelopeError(message: "옛 형식"))

        let entry = try Wire.decode(TimelineEntry.self, from: Data(#"""
        {"seq":1,"ts":"2026-10-20T10:00:00.000Z","host_id":"h","actor":"ai","client_name":"mcp","kind":"input",
         "summary":"clicked","outcome":"ok","artifacts":[]}
        """#.utf8))
        XCTAssertEqual(entry.clientName, "mcp")
        XCTAssertNil(entry.clientID)
    }

    func testDiagnosticsModelsDecodeProtocolFields() throws {
        let proc = try Wire.decode(ProcessInfo.self, from: Data(#"""
        {"pid":7712,"ppid":5520,"name":"node.exe","path":"C:\\Program Files\\nodejs\\node.exe","cmd":"node server.js",
         "mem_kb":184320,"cpu_pct":12.5,"user":"DEV-PC\\taeyoung","started":"2026-10-20T09:12:00.000Z","started_by_deskhand":true,"self":false}
        """#.utf8))
        XCTAssertEqual(proc.memKb, 184_320)
        XCTAssertEqual(proc.cpuPct, 12.5)
        XCTAssertEqual(proc.user, "DEV-PC\\taeyoung")
        XCTAssertEqual(proc.started, Timestamp(string: "2026-10-20T09:12:00.000Z"))
        XCTAssertTrue(proc.startedByDeskhand)

        let system = try Wire.decode(ProcessInfo.self, from: Data(#"""
        {"pid":4,"ppid":0,"name":"System","mem_kb":144,"cpu_pct":0.1,"started_by_deskhand":false}
        """#.utf8))
        XCTAssertNil(system.started)
        XCTAssertFalse(system.isSelf)
        XCTAssertFalse(proc.isSelf)
        let agent = try Wire.decode(ProcessInfo.self, from: Data(#"""
        {"pid":3120,"ppid":2032,"name":"deskhand-agent.exe","mem_kb":38000,"cpu_pct":0.4,"started":"2026-10-18T09:00:00.000Z",
         "started_by_deskhand":false,"self":true}
        """#.utf8))
        XCTAssertTrue(agent.isSelf)
        XCTAssertNil(system.user)
        XCTAssertNil(system.path)

        let diag = try Wire.decode(DiagSystem.self, from: Data(#"""
        {"os":{"name":"Windows 11 Pro","version":"24H2","build":"26100.2314","edition":"Professional"},"computer_name":"DEV-PC",
         "cpu":{"name":"AMD Ryzen 9 7950X","cores":16,"threads":32,"usage_pct":12.5},"memory":{"total_bytes":68719476736,"used_bytes":21474836480},
         "disks":[{"root":"C:\\","label":"Windows","fs":"NTFS","total_bytes":100,"free_bytes":50},{"root":"D:\\","fs":"exFAT","total_bytes":10,"free_bytes":1}],
         "gpus":[{"name":"NVIDIA GeForce RTX 2070","driver_version":"32.0.15.6636","memory_bytes":8589934592},{"name":"Microsoft Basic Display Adapter"}],
         "boot_time":"2026-10-18T10:00:00.000Z","uptime_s":187200}
        """#.utf8))
        XCTAssertEqual(diag.os.name, "Windows 11 Pro")
        XCTAssertEqual(diag.os.build, "26100.2314")
        XCTAssertEqual(diag.os.edition, "Professional")
        XCTAssertEqual(diag.computerName, "DEV-PC")
        XCTAssertEqual(diag.cpu.usagePct, 12.5)
        XCTAssertEqual(diag.cpu.threads, 32)
        XCTAssertEqual(diag.memory.usedBytes, 21_474_836_480)
        XCTAssertEqual(diag.disks.first?.root, "C:\\")
        XCTAssertEqual(diag.disks.first?.label, "Windows")
        XCTAssertNil(diag.disks.last?.label)
        XCTAssertEqual(diag.disks.last?.fs, "exFAT")
        XCTAssertEqual(diag.gpus.first?.driverVersion, "32.0.15.6636")
        XCTAssertEqual(diag.gpus.first?.memoryBytes, 8_589_934_592)
        XCTAssertNil(diag.gpus.last?.driverVersion)
        XCTAssertEqual(diag.bootTime, Timestamp(string: "2026-10-18T10:00:00.000Z"))
        XCTAssertEqual(diag.uptimeS, 187_200)

        let log = try Wire.decode(EventLogEntry.self, from: Data(#"""
        {"record_id":88123,"time":"2026-10-20T10:00:00.000Z","level":"information","provider":"Service Control Manager","event_id":7036,"message":"m"}
        """#.utf8))
        XCTAssertEqual(log.recordID, 88_123)
        XCTAssertEqual(log.eventID, 7036)
        XCTAssertEqual(log.eventLevel, .information)
        let odd = try Wire.decode(EventLogEntry.self, from: Data(#"""
        {"record_id":1,"time":"2026-10-20T10:00:00.000Z","level":"audit_success","provider":"p","event_id":1,"message":""}
        """#.utf8))
        XCTAssertNil(odd.eventLevel)

        let dump = try Wire.decode(CrashDump.self, from: Data(#"""
        {"path":"C:\\ProgramData\\Shop\\CrashDumps\\Shop.exe.4013.dmp","app":"Shop.exe","time":"2026-10-20T09:00:00.000Z",
         "size_bytes":51200000,"kind":"minidump"}
        """#.utf8))
        XCTAssertEqual(dump.app, "Shop.exe")
        XCTAssertEqual(dump.sizeBytes, 51_200_000)
        XCTAssertEqual(dump.dumpKind, .minidump)
    }

    func testFrameParserLimits() throws {
        var p = FrameParser()
        XCTAssertThrowsError(try p.append(Data([0, 0, 0, 0])))

        var big = FrameParser()
        let over = UInt32(FrameParser.maxFrameLength + 1)
        XCTAssertThrowsError(try big.append(Data([UInt8(over >> 24), UInt8((over >> 16) & 0xFF), UInt8((over >> 8) & 0xFF), UInt8(over & 0xFF)])))

        var exact = FrameParser()
        let cap = UInt32(FrameParser.maxFrameLength)
        var data = Data([UInt8(cap >> 24), UInt8((cap >> 16) & 0xFF), UInt8((cap >> 8) & 0xFF), UInt8(cap & 0xFF), 0x20])
        data.append(Data(count: FrameParser.maxFrameLength - 1))
        let frames = try exact.append(data)
        XCTAssertEqual(frames.count, 1)
        XCTAssertEqual(frames[0].payload.count, FrameParser.maxFrameLength - 1)

        var split = FrameParser()
        XCTAssertEqual(try split.append(Data([0, 0])).count, 0)
        XCTAssertEqual(try split.append(Data([0, 3, 0x21])).count, 0)
        let done = try split.append(Data([9, 8, 0, 0, 0, 1, 0x11]))
        XCTAssertEqual(done.count, 2)
        XCTAssertEqual(done[0].kind, 0x21)
        XCTAssertEqual([UInt8](done[0].payload), [9, 8])
        XCTAssertEqual(done[1].kind, 0x11)
    }

    func testStreamAttachReplyCoalescedWithFrames() async throws {
        let server = try makeServer()
        let payload = Data([0x0A, 0x0A, 0x7B, 0x0A])
        var frame = Data([0, 0, 0, 5, 0x20])
        frame.append(payload)
        var newlineLength = Data([0, 0, 0x0A, 0x0A, 0x01])
        newlineLength.append(Data(count: 0x0A0A - 1))
        let wire = frame + newlineLength
        server.setAttachReply { conn, id in
            var one = Data(#"{"jsonrpc":"2.0","id":\#(id),"result":{"ok":true}}"#.utf8)
            one.append(0x0A)
            one.append(wire)
            conn.sendRaw(one)
        }
        server.setAttachHandler { conn in conn.shutdownConnection() }
        let stream = try await StreamConnection.attach(kind: "pty", params: [:], socketPath: server.path)
        var got: [(UInt8, Data)] = []
        for try await f in stream.frames { got.append((f.kind, f.payload)) }
        XCTAssertEqual(got.count, 2)
        XCTAssertEqual(got[0].0, 0x20)
        XCTAssertEqual(got[0].1, payload)
        XCTAssertEqual(got[1].0, 0x01)
        XCTAssertEqual(got[1].1.count, 0x0A0A - 1)
    }

    func testSubscriptionAddedDuringReconnectResubscribeIsSent() async throws {
        let server = try makeServer()
        let arrived = DispatchSemaphore(value: 0)
        let release = DispatchSemaphore(value: 0)
        let count = Received()
        server.setSubscribeHook { _ in
            let n = count.get().count + 1
            count.set(Data(count: n))
            if n == 2 {
                arrived.signal()
                release.wait()
            }
        }
        let client = makeClient(server)
        try await client.connect(kind: "app", name: "Deskhand", version: "1")
        let early = client.events(kinds: ["a"], hostID: nil)
        defer { _ = early }
        XCTAssertTrue(server.waitForRequests(method: "events.subscribe", count: 1))

        server.stop()
        try await Task.sleep(for: .milliseconds(50))
        try server.start()
        XCTAssertEqual(arrived.wait(timeout: .now() + 5), .success)

        let late = client.events(kinds: ["b"], hostID: nil)
        let deadline = Date().addingTimeInterval(5)
        while await client.subscriptionCount < 2 && Date() < deadline { try await Task.sleep(for: .milliseconds(5)) }
        release.signal()

        XCTAssertTrue(server.waitForRequests(method: "events.subscribe", count: 3))
        let last = server.requests(method: "events.subscribe")[2]
        XCTAssertEqual(Set(last.params["kinds"] as? [String] ?? []), ["a", "b"])
        server.emit(#"{"seq":5,"ts":"2026-10-20T10:00:00.000Z","host_id":"h","kind":"b"}"#)
        let ev = try await next(late)
        XCTAssertEqual(ev.seq, 5)
        await client.close()
    }

    func testPathsHonorDeskhandHome() {
        XCTAssertTrue(Paths.daemonLog.hasSuffix("/logs/daemon.log"))
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

    func testLauncherBoundsHangingStartCommand() async throws {
        let server = MockDaemon()
        let launcher = DaemonLauncher(socketPath: server.path, launch: {
            try await Task.sleep(for: .seconds(30))
        }, timeout: 0.3, pollInterval: 0.01)
        let started = Date()
        do {
            try await launcher.ensureRunning()
            XCTFail("expected error")
        } catch ClientError.daemonUnavailable {}
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
    }

    func testLauncherThrowsDaemonUnavailableOnTimeout() async throws {
        let server = MockDaemon()
        let launcher = DaemonLauncher(socketPath: server.path, launch: {}, timeout: 0.2, pollInterval: 0.01)
        do {
            try await launcher.ensureRunning()
            XCTFail("expected error")
        } catch ClientError.daemonUnavailable(let logPath) {
            XCTAssertTrue(logPath.hasSuffix("logs/daemon.log"))
        }
    }
}

final class DaemonRestartTests: XCTestCase {
    private static func responding(_ path: String) -> Bool {
        guard let connection = try? Connection.connect(path: path) else { return false }
        connection.close()
        return true
    }

    func testRestartWaitsForTheOldDaemonToStopBeforeStarting() async throws {
        let server = MockDaemon()
        try server.start()
        let path = server.path
        let startedWhileAnswering = Received()
        let launcher = DaemonLauncher(socketPath: path, launch: {
            startedWhileAnswering.set(Data([Self.responding(path) ? 1 : 0]))
            try server.start()
        }, stop: {
            Task.detached {
                try? await Task.sleep(for: .milliseconds(300))
                server.stop()
            }
        }, timeout: 3, stopTimeout: 3, pollInterval: 0.01)

        try await launcher.restart()

        XCTAssertEqual(startedWhileAnswering.get(), Data([0]))
        XCTAssertTrue(Self.responding(path))
    }

    func testRestartGivesUpOnAStopCommandThatHangs() async throws {
        let server = MockDaemon()
        try server.start()
        let launcher = DaemonLauncher(socketPath: server.path, launch: {}, stop: {
            try await Task.sleep(for: .seconds(30))
        }, timeout: 1, stopTimeout: 0.3, pollInterval: 0.01)
        let started = Date()
        do {
            try await launcher.restart()
            XCTFail("expected error")
        } catch let error as DaemonCommandError {
            XCTAssertEqual(error, .timedOut(command: "deskhand daemon stop", seconds: 0.3))
        }
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
    }

    func testRestartReportsADaemonThatKeepsAnswering() async throws {
        let server = MockDaemon()
        try server.start()
        let launcher = DaemonLauncher(socketPath: server.path, launch: {}, stop: {}, timeout: 1, stopTimeout: 0.3, pollInterval: 0.01)
        do {
            try await launcher.restart()
            XCTFail("expected error")
        } catch let error as DaemonCommandError {
            XCTAssertEqual(error, .stillRunning)
        }
    }

    func testRestartReportsAFailingStartWithItsOutput() async throws {
        let server = MockDaemon()
        try server.start()
        let failure = DaemonCommandError.failed(command: "deskhand daemon start", status: 1, stderr: "address already in use")
        let launcher = DaemonLauncher(socketPath: server.path, launch: { throw failure }, stop: { server.stop() },
                                      timeout: 1, stopTimeout: 1, pollInterval: 0.01)
        do {
            try await launcher.restart()
            XCTFail("expected error")
        } catch let error as DaemonCommandError {
            XCTAssertEqual(error, failure)
        }
    }

    func testCommandRunnerKeepsStandardErrorOfAFailure() async throws {
        do {
            try await DaemonLauncher.run(URL(fileURLWithPath: "/bin/sh"), ["-c", "echo first >&2; echo 'bind: address in use' >&2; exit 3"],
                                         command: "deskhand daemon start", timeout: 5)
            XCTFail("expected error")
        } catch let error as DaemonCommandError {
            XCTAssertEqual(error, .failed(command: "deskhand daemon start", status: 3, stderr: "first\nbind: address in use"))
        }
    }

    func testCommandRunnerEndsACommandThatRunsTooLong() async throws {
        let started = Date()
        do {
            try await DaemonLauncher.run(URL(fileURLWithPath: "/bin/sleep"), ["30"], command: "deskhand daemon stop", timeout: 0.3)
            XCTFail("expected error")
        } catch let error as DaemonCommandError {
            XCTAssertEqual(error, .timedOut(command: "deskhand daemon stop", seconds: 0.3))
        }
        XCTAssertLessThan(Date().timeIntervalSince(started), 3)
    }

    func testCommandRunnerSucceedsOnExitZero() async throws {
        try await DaemonLauncher.run(URL(fileURLWithPath: "/bin/sh"), ["-c", "exit 0"], command: "deskhand daemon status", timeout: 5)
    }
}

final class Received: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    func set(_ d: Data) { lock.lock(); data = d; lock.unlock() }
    func get() -> Data { lock.lock(); defer { lock.unlock() }; return data }
}
