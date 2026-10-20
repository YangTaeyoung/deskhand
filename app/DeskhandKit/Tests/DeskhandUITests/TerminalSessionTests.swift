import DeskhandClient
import Foundation
import Synchronization
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// Records terminal calls and opens a `FakeScreenStream` per attach; attaches fail with the
/// queued failures first.
final class FakeTerminalBackend: TerminalBackend {
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
        var killed: [String] = []
        var jobs: [Job] = []
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

    func failAttaches(_ failures: [OperationError]) {
        state.withLock { $0.attachFailures = failures }
    }

    func failOpen(_ failure: OperationError?) {
        state.withLock { $0.openFailure = failure }
    }

    func setJobs(_ jobs: [Job]) {
        state.withLock { $0.jobs = jobs }
    }

    func ptyOpen(hostID: String, shell: TerminalShell, cols: Int, rows: Int) async throws -> PTYOpening {
        let failure = state.withLock { s -> OperationError? in
            s.opens.append(Open(hostID: hostID, shell: shell, size: TerminalSize(cols: cols, rows: rows)))
            return s.openFailure
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
        state.withLock { $0.jobs.filter { $0.hostID == hostID } }
    }

    func ptyKill(jobID: String) async throws {
        state.withLock {
            $0.killed.append(jobID)
            $0.jobs.removeAll { $0.jobID == jobID }
        }
    }
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
        let request = PasteRequest(sessionID: UUID(), text: "cd C:\\Shop\r\nnpm run dev\n")
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

@MainActor
final class TerminalWorkspaceTests: XCTestCase {
    private var workspace: TerminalWorkspace!

    override func setUp() async throws {
        let defaults = UserDefaults(suiteName: "TerminalWorkspaceTests-\(UUID().uuidString)")!
        workspace = TerminalWorkspace(hostID: PreviewFixtures.devPCID, backend: PreviewBackend(stepDelay: .zero),
                                      preferences: TerminalPreferences(defaults: defaults))
        workspace.start()
    }

    override func tearDown() async throws {
        workspace.stop()
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

    private func openLiveShell() async throws -> TerminalSession {
        workspace.open(.powershell)
        let session = try XCTUnwrap(workspace.selected)
        await wait("live") { session.phase == .live }
        await wait("the prompt") { session.surface.bufferText().contains("PS C:\\Users\\taeyoung>") }
        return session
    }

    func testClosingARunningShellAsksFirst() async throws {
        let session = try await openLiveShell()
        workspace.requestClose()
        XCTAssertEqual(workspace.pendingClose?.id, session.id)
        XCTAssertEqual(workspace.sessions.count, 1)
    }

    func testDetachedShellIsListedAndReopensWithItsScrollback() async throws {
        let session = try await openLiveShell()
        let job = try XCTUnwrap(session.jobID)
        session.surface.send(txt: "whoami\r")
        await wait("output") { session.surface.bufferText().contains("dev-pc\\taeyoung") }

        workspace.close(session, ending: false)
        XCTAssertTrue(workspace.sessions.isEmpty)
        await wait("the detached job") { workspace.detachedJobs.contains { $0.jobID == job } }

        let listed = try XCTUnwrap(workspace.detachedJobs.first { $0.jobID == job })
        workspace.reopen(listed)
        let reopened = try XCTUnwrap(workspace.selected)
        await wait("live again") { reopened.phase == .live }
        await wait("the replayed scrollback") { reopened.surface.bufferText().contains("dev-pc\\taeyoung") }
        XCTAssertFalse(workspace.detachedJobs.contains { $0.jobID == job })
    }

    func testEndingAShellRemovesItsJob() async throws {
        let session = try await openLiveShell()
        let job = try XCTUnwrap(session.jobID)
        workspace.close(session, ending: true)
        await wait("the job list without it") { workspace.jobsLoaded && !workspace.detachedJobs.contains { $0.jobID == job } }
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertFalse(workspace.detachedJobs.contains { $0.jobID == job })
        XCTAssertTrue(workspace.sessions.isEmpty)
    }

    func testMultilinePasteWaitsForConfirmation() async throws {
        let session = try await openLiveShell()
        workspace.requestPaste(session, text: "whoami\nhostname\n")
        let request = try XCTUnwrap(workspace.pendingPaste)
        XCTAssertEqual(request.lineCount, 2)
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertFalse(session.surface.bufferText().contains("dev-pc\\taeyoung"))

        workspace.confirmPaste(request, dontAskAgain: false)
        XCTAssertNil(workspace.pendingPaste)
        await wait("both commands ran") { session.surface.bufferText().contains("DEV-PC") }
        XCTAssertTrue(session.surface.bufferText().contains("dev-pc\\taeyoung"))
    }

    func testSingleLinePasteAndDisabledConfirmationSendAtOnce() async throws {
        let session = try await openLiveShell()
        workspace.requestPaste(session, text: "whoami")
        XCTAssertNil(workspace.pendingPaste)
        workspace.preferences.confirmsMultilinePaste = false
        workspace.requestPaste(session, text: "\rhostname\r")
        XCTAssertNil(workspace.pendingPaste)
        await wait("the pasted commands ran") { session.surface.bufferText().contains("DEV-PC") }
    }
}
