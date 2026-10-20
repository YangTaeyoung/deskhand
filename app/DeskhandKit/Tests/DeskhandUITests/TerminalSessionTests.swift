import DeskhandClient
import Foundation
import Synchronization
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// Records terminal calls and opens a `FakeScreenStream` per attach; attaches fail with the
/// queued failures first. `pty.open` can be held until the test releases it, `jobs.list` can
/// fail, and the test pushes `job.updated` events or ends the event stream.
final class FakeTerminalBackend: TerminalBackend, EventsBackend {
    struct Open: Equatable {
        let hostID: String
        let shell: TerminalShell
        let size: TerminalSize
    }

    private struct State {
        var opens: [Open] = []
        var attaches: [PTYTarget] = []
        var streams: [FakeScreenStream] = []
        var attachFailures: [OperationError] = []
        var openFailure: OperationError?
        var holdOpen = false
        var heldOpens: [CheckedContinuation<Void, Never>] = []
        var killed: [String] = []
        var jobs: [Job] = []
        var jobsFailure: OperationError?
        var subscriptions = 0
        var events: AsyncThrowingStream<DaemonEvent, Error>.Continuation?
        var seq = 0
    }

    private let state = Mutex(State())
    let opening: PTYOpening

    init(opening: PTYOpening = PTYOpening(ptyID: "p-1", jobID: "j-1")) {
        self.opening = opening
    }

    var opens: [Open] { state.withLock { $0.opens } }
    var attaches: [PTYTarget] { state.withLock { $0.attaches } }
    var streams: [FakeScreenStream] { state.withLock { $0.streams } }
    var killed: [String] { state.withLock { $0.killed } }
    var subscriptions: Int { state.withLock { $0.subscriptions } }
    var heldOpenCount: Int { state.withLock { $0.heldOpens.count } }

    func failAttaches(_ failures: [OperationError]) {
        state.withLock { $0.attachFailures = failures }
    }

    func failOpen(_ failure: OperationError?) {
        state.withLock { $0.openFailure = failure }
    }

    func setJobs(_ jobs: [Job]) {
        state.withLock { $0.jobs = jobs }
    }

    func failJobs(_ failure: OperationError?) {
        state.withLock { $0.jobsFailure = failure }
    }

    /// Makes `pty.open` wait until `releaseOpens()`.
    func holdOpens(_ hold: Bool) {
        state.withLock { $0.holdOpen = hold }
    }

    func releaseOpens() {
        let held = state.withLock { s -> [CheckedContinuation<Void, Never>] in
            defer { s.heldOpens = [] }
            s.holdOpen = false
            return s.heldOpens
        }
        for continuation in held { continuation.resume() }
    }

    /// Delivers `job.updated` for `job` on the current event stream.
    func pushJobUpdate(_ job: Job) {
        let (continuation, seq) = state.withLock { s -> (AsyncThrowingStream<DaemonEvent, Error>.Continuation?, Int) in
            s.seq += 1
            return (s.events, s.seq)
        }
        let payload: [String: JSONValue] = ["job": PreviewBackend.payload(job)]
        continuation?.yield(DaemonEvent(seq: seq, hostID: job.hostID, kind: EventKind.jobUpdated, payload: payload))
    }

    /// Ends the current event stream with an error, as a dropped subscription does.
    func failEvents() {
        let continuation = state.withLock { s -> AsyncThrowingStream<DaemonEvent, Error>.Continuation? in
            defer { s.events = nil }
            return s.events
        }
        continuation?.finish(throwing: ClientError.disconnected)
    }

    func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: DaemonEvent.self)
        state.withLock {
            $0.subscriptions += 1
            $0.events = continuation
        }
        return stream
    }

    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        let (failure, hold) = state.withLock { s -> (OperationError?, Bool) in
            s.opens.append(Open(hostID: hostID, shell: shell, size: TerminalSize(cols: cols, rows: rows)))
            return (s.openFailure, s.holdOpen)
        }
        if hold {
            await withCheckedContinuation { continuation in
                state.withLock { $0.heldOpens.append(continuation) }
            }
        }
        if let failure { throw failure }
        return opening
    }

    func ptyAttach(_ target: PTYTarget) async throws -> any FrameStream {
        let result = state.withLock { s -> Result<FakeScreenStream, OperationError> in
            s.attaches.append(target)
            if !s.attachFailures.isEmpty { return .failure(s.attachFailures.removeFirst()) }
            let stream = FakeScreenStream()
            s.streams.append(stream)
            return .success(stream)
        }
        return try result.get()
    }

    func ptyJobs(hostID: String) async throws -> [Job] {
        let (jobs, failure) = state.withLock { s in (s.jobs.filter { $0.hostID == hostID }, s.jobsFailure) }
        if let failure { throw failure }
        return jobs
    }

    func ptyKill(jobID: String) async throws {
        state.withLock {
            $0.killed.append(jobID)
            $0.jobs.removeAll { $0.jobID == jobID }
        }
    }
}

/// A running pty job of the dev PC.
func ptyJob(_ id: String, summary: String = "PowerShell · C:\\Shop", status: String = "running") -> Job {
    Job(jobID: id, hostID: "windows-dev", kind: "pty", status: status, summary: summary,
        createdAt: Timestamp(date: .now), updatedAt: Timestamp(date: .now),
        result: .object(["shell": .string("powershell"), "pid": .int(4812)]))
}

@MainActor
final class TerminalSessionTests: XCTestCase {
    private static let fast = AppStore.ResubscribeBackoff(initial: .milliseconds(10), maximum: .milliseconds(40))

    private var sessions: [TerminalSession] = []

    override func tearDown() async throws {
        for session in sessions { session.detach() }
        sessions = []
    }

    private func makeSession(_ backend: FakeTerminalBackend, jobID: String? = nil) -> TerminalSession {
        let session = TerminalSession(hostID: "windows-dev", shell: .powershell, backend: backend,
                                      size: TerminalSize(cols: 80, rows: 24), jobID: jobID, backoff: Self.fast)
        sessions.append(session)
        return session
    }

    private func wait(_ what: String, timeout: TimeInterval = 3, until condition: () -> Bool) async {
        let deadline = Date.now.addingTimeInterval(timeout)
        while !condition() {
            if Date.now > deadline {
                XCTFail("timed out waiting for \(what)")
                return
            }
            try? await Task.sleep(for: .milliseconds(5))
        }
    }

    private func attached(_ json: String) -> Data { Data(json.utf8) }

    private func resizeFrames(_ stream: FakeScreenStream) -> [TerminalSize] {
        stream.sent.filter { $0.kind == TerminalFrameKind.resize }.compactMap {
            try? JSONDecoder().decode(TerminalSize.self, from: $0.payload)
        }
    }

    /// Opens a session and attaches its first stream with the attached frame.
    private func live(_ backend: FakeTerminalBackend) async throws -> (TerminalSession, FakeScreenStream) {
        let session = makeSession(backend)
        session.start()
        await wait("an attached stream") { backend.streams.count == 1 }
        let stream = try XCTUnwrap(backend.streams.first)
        stream.push(TerminalFrameKind.attached, attached(#"{"job":"j-1","pid":4812}"#))
        await wait("live") { session.phase == .live }
        return (session, stream)
    }

    func testResizeSendsFrame() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        await wait("the size after attaching") { resizeFrames(stream) == [TerminalSize(cols: 80, rows: 24)] }

        session.resize(TerminalSize(cols: 132, rows: 40))
        await wait("a resize frame") { resizeFrames(stream).last == TerminalSize(cols: 132, rows: 40) }
        XCTAssertEqual(session.size, TerminalSize(cols: 132, rows: 40))

        session.resize(TerminalSize(cols: 132, rows: 40))
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(resizeFrames(stream).count, 2, "an unchanged size sends nothing")
    }

    func testResizeBurstSendsOnlyTheLastSize() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        await wait("the size after attaching") { resizeFrames(stream).count == 1 }
        for cols in [90, 100, 110, 120] {
            session.resize(TerminalSize(cols: cols, rows: 30))
        }
        await wait("a resize frame") { resizeFrames(stream).count == 2 }
        try await Task.sleep(for: TerminalSession.resizeDelay * 2)
        XCTAssertEqual(resizeFrames(stream), [TerminalSize(cols: 80, rows: 24), TerminalSize(cols: 120, rows: 30)])
    }

    func testEveryAttachedFrameSendsTheCurrentSize() async throws {
        let backend = FakeTerminalBackend()
        let (session, first) = try await live(backend)
        await wait("the size after attaching") { resizeFrames(first).count == 1 }
        session.resize(TerminalSize(cols: 100, rows: 30))
        await wait("the new size") { resizeFrames(first).last == TerminalSize(cols: 100, rows: 30) }
        first.end()
        await wait("a second attach") { backend.streams.count == 2 }
        let second = try XCTUnwrap(backend.streams.last)
        second.push(TerminalFrameKind.attached, attached(#"{"job":"j-1","pid":4812}"#))
        await wait("the size after attaching again") { resizeFrames(second) == [TerminalSize(cols: 100, rows: 30)] }
    }

    func testDetachStopsAttaching() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        session.detach()
        XCTAssertTrue(stream.isClosed)
        try await Task.sleep(for: .milliseconds(120))
        XCTAssertEqual(backend.attaches.count, 1)
    }

    func testStreamsThatDropRightAfterAttachingKeepBackingOff() async throws {
        let backend = FakeTerminalBackend()
        let session = makeSession(backend)
        session.start()
        for count in 1...4 {
            await wait("attach \(count)") { backend.streams.count == count }
            let stream = try XCTUnwrap(backend.streams.last)
            stream.push(TerminalFrameKind.attached, attached(#"{"job":"j-1","pid":4812}"#))
            await wait("live \(count)") { session.phase == .live }
            stream.end()
            await wait("reconnecting \(count)") { session.phase == .reconnecting(nil) }
        }
        XCTAssertEqual(session.lastRetryDelay, Self.fast.maximum)
    }

    func testStreamThatStayedAttachedRetriesQuickly() async throws {
        let backend = FakeTerminalBackend()
        let session = TerminalSession(hostID: "windows-dev", shell: .powershell, backend: backend, size: TerminalSize(cols: 80, rows: 24),
                                      backoff: Self.fast, stableAttach: .milliseconds(30))
        sessions.append(session)
        session.start()
        for count in 1...3 {
            await wait("attach \(count)") { backend.streams.count == count }
            let stream = try XCTUnwrap(backend.streams.last)
            stream.push(TerminalFrameKind.attached, attached(#"{"job":"j-1","pid":4812}"#))
            await wait("live \(count)") { session.phase == .live }
            try await Task.sleep(for: .milliseconds(50))
            stream.end()
            await wait("reconnecting \(count)") { session.phase == .reconnecting(nil) }
        }
        XCTAssertEqual(session.lastRetryDelay, Self.fast.initial)
    }

    func testReattachUsesJobID() async throws {
        let backend = FakeTerminalBackend(opening: PTYOpening(ptyID: "p-1"))
        let session = makeSession(backend)
        session.start()
        await wait("an attached stream") { backend.streams.count == 1 }
        let first = try XCTUnwrap(backend.streams.first)
        first.push(TerminalFrameKind.attached, attached(#"{"job":"j-7","pid":42}"#))
        await wait("live") { session.phase == .live }
        XCTAssertEqual(session.jobID, "j-7")

        first.end()
        await wait("a second attach") { backend.streams.count == 2 }
        XCTAssertEqual(backend.attaches, [.pty("p-1"), .job("j-7")])
        XCTAssertEqual(backend.opens.count, 1, "reattaching does not open another shell")
    }

    func testReplayedScrollbackReplacesTheScreen() async throws {
        let backend = FakeTerminalBackend()
        let (session, first) = try await live(backend)
        first.push(TerminalFrameKind.data, Data("PS C:\\Users\\taeyoung> whoami\r\ndev-pc\\taeyoung\r\n".utf8))
        await wait("output") { session.surface.bufferText().contains("dev-pc") }
        first.end()
        await wait("a second attach") { backend.streams.count == 2 }
        let second = try XCTUnwrap(backend.streams.last)
        second.push(TerminalFrameKind.attached, attached(#"{"job":"j-1","pid":4812}"#))
        second.push(TerminalFrameKind.data, Data("PS C:\\Users\\taeyoung> whoami\r\ndev-pc\\taeyoung\r\n".utf8))
        await wait("live again") { session.phase == .live }
        await wait("the replay") { session.surface.bufferText().contains("dev-pc") }
        XCTAssertEqual(session.surface.bufferText().components(separatedBy: "dev-pc\\taeyoung").count - 1, 1)
    }

    func testTypedBytesGoAsDataFrames() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        session.surface.send(txt: "dir\r")
        session.surface.send([0x03])
        await wait("data frames") { stream.sent.filter { $0.kind == TerminalFrameKind.data }.count == 2 }
        let data = stream.sent.filter { $0.kind == TerminalFrameKind.data }.map(\.payload)
        XCTAssertEqual(data, [Data("dir\r".utf8), Data([0x03])])
    }

    func testExitFrameEndsTheSessionWithoutReattaching() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        stream.push(TerminalFrameKind.exit, Data(#"{"code":3}"#.utf8))
        await wait("exited") { session.phase == .exited(3) }
        try await Task.sleep(for: .milliseconds(60))
        XCTAssertEqual(backend.attaches.count, 1)
        XCTAssertFalse(session.isShellRunning)
    }

    func testMissingJobFailsInsteadOfRetrying() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        backend.failAttaches([OperationError(code: "NOT_FOUND", message: "셸을 찾을 수 없습니다")])
        stream.end()
        await wait("failed") {
            if case .failed(let error) = session.phase { return error.code == "NOT_FOUND" }
            return false
        }
        XCTAssertEqual(backend.attaches, [.pty("p-1"), .job("j-1")])
    }

    func testOfflineAttachKeepsReconnecting() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        backend.failAttaches([OperationError(code: "OFFLINE", message: "연결되어 있지 않습니다"),
                              OperationError(code: "OFFLINE", message: "연결되어 있지 않습니다")])
        stream.end()
        await wait("reconnecting with the failure") { session.phase == .reconnecting(OperationError(code: "OFFLINE", message: "연결되어 있지 않습니다")) }
        await wait("attached again") { backend.streams.count == 2 }
        backend.streams[1].push(TerminalFrameKind.attached, attached(#"{"job":"j-1","pid":4812}"#))
        await wait("live again") { session.phase == .live }
        XCTAssertEqual(backend.attaches.count, 4)
    }

    func testOpenFailureShowsTheError() async throws {
        let backend = FakeTerminalBackend()
        backend.failOpen(OperationError(code: "CAPABILITY_UNSUPPORTED", message: "터미널을 지원하지 않습니다"))
        let session = makeSession(backend)
        session.start()
        await wait("failed") { session.phase == .failed(OperationError(code: "CAPABILITY_UNSUPPORTED", message: "터미널을 지원하지 않습니다")) }
        XCTAssertTrue(backend.attaches.isEmpty)
    }

    func testSessionForAJobAttachesWithoutOpening() async throws {
        let backend = FakeTerminalBackend()
        let session = makeSession(backend, jobID: "j-dev")
        session.start()
        await wait("an attach") { backend.attaches.count == 1 }
        XCTAssertEqual(backend.attaches, [.job("j-dev")])
        XCTAssertTrue(backend.opens.isEmpty)
        XCTAssertEqual(session.phase, .attaching)
    }

    func testTerminateCancelsTheJob() async throws {
        let backend = FakeTerminalBackend()
        let (session, stream) = try await live(backend)
        try await session.terminate()
        XCTAssertEqual(backend.killed, ["j-1"])
        XCTAssertTrue(stream.isClosed)
    }

    func testTitleKeepsWhatFollowsTheProgramPath() {
        XCTAssertEqual(TerminalTitle.text(shell: .cmd, remoteTitle: "C:\\WINDOWS\\system32\\cmd.exe - ping localhost"), "cmd · ping localhost")
        XCTAssertEqual(TerminalTitle.text(shell: .powershell, remoteTitle: "C:\\WINDOWS\\System32\\WindowsPowerShell\\v1.0\\powershell.exe"),
                       "PowerShell")
        XCTAssertEqual(TerminalTitle.text(shell: .powershell, remoteTitle: "npm run dev"), "npm run dev")
        XCTAssertEqual(TerminalTitle.text(shell: .pwsh, remoteTitle: nil), "PowerShell 7")
        XCTAssertEqual(TerminalTitle.windowsPath("file://DEV-PC/C:/Users/taeyoung/Downloads"), "C:\\Users\\taeyoung\\Downloads")
        XCTAssertEqual(TerminalTitle.leaf("C:\\Users\\taeyoung\\Downloads"), "Downloads")
        XCTAssertEqual(TerminalTitle.leaf("C:\\"), "C:\\")
    }

    func testPasteNeedsConfirmationOnlyWithLineBreaks() {
        XCTAssertFalse(PasteRequest.needsConfirmation("Get-Process"))
        XCTAssertTrue(PasteRequest.needsConfirmation("cd C:\\Shop\nnpm run dev\n"))
        let request = PasteRequest(sessionID: UUID(), clipboard: "cd C:\\Shop\r\nnpm run dev\n")
        XCTAssertEqual(request.lineCount, 2)
        XCTAssertEqual(PasteRequest.normalized("a\r\nb\nc"), "a\rb\rc")
    }

    func testPowerShell7IsOfferedOnlyWhenReported() {
        var host = PreviewFixtures.hosts().first { $0.hostID == PreviewFixtures.meetingPCID }!
        XCTAssertEqual(TerminalShell.available(on: host), [.powershell, .cmd])
        host.caps = HostCaps(items: ["pty": Capability(state: "ready", shells: ["powershell", "pwsh", "cmd"])])
        XCTAssertEqual(TerminalShell.available(on: host), [.powershell, .pwsh, .cmd])
    }
}
