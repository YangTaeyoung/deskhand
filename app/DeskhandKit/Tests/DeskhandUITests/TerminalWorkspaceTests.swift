import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

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

/// Workspace behavior around job events, held `pty.open` calls and paste delivery, against
/// `FakeTerminalBackend`.
@MainActor
final class TerminalWorkspaceEventTests: XCTestCase {
    private static let fast = AppStore.ResubscribeBackoff(initial: .milliseconds(10), maximum: .milliseconds(40))
    private var backend: FakeTerminalBackend!
    private var workspace: TerminalWorkspace!

    override func setUp() async throws {
        backend = FakeTerminalBackend()
        let defaults = UserDefaults(suiteName: "TerminalWorkspaceEventTests-\(UUID().uuidString)")!
        workspace = TerminalWorkspace(hostID: "windows-dev", backend: backend, preferences: TerminalPreferences(defaults: defaults),
                                      backoff: Self.fast)
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

    private func dataFrames(_ stream: FakeScreenStream) -> [Data] {
        stream.sent.filter { $0.kind == TerminalFrameKind.data }.map(\.payload)
    }

    private func liveSession() async throws -> (TerminalSession, FakeScreenStream) {
        workspace.open(.powershell)
        let session = try XCTUnwrap(workspace.selected)
        await wait("an attached stream") { backend.streams.count == 1 }
        let stream = try XCTUnwrap(backend.streams.first)
        stream.push(TerminalFrameKind.attached, Data(#"{"job":"j-1","pid":4812}"#.utf8))
        await wait("live") { session.phase == .live }
        return (session, stream)
    }

    func testJobEventBeforeOpenReturnsIsNotListedAsDetached() async throws {
        workspace.start()
        await wait("the subscription") { backend.subscriptions == 1 }
        backend.holdOpens(true)
        workspace.open(.powershell)
        let session = try XCTUnwrap(workspace.selected)
        await wait("the held pty.open") { backend.heldOpenCount == 1 }
        backend.pushJobUpdate(ptyJob("j-1"))
        await wait("the running job") { workspace.runningJobs.contains { $0.jobID == "j-1" } }
        backend.releaseOpens()
        await wait("the session's job") { session.jobID == "j-1" }
        XCTAssertFalse(workspace.detachedJobs.contains { $0.jobID == "j-1" })
        backend.pushJobUpdate(ptyJob("j-other"))
        await wait("another shell") { workspace.detachedJobs.map(\.jobID) == ["j-other"] }
    }

    func testClosingATabWhileOpeningEndsTheJob() async throws {
        backend.holdOpens(true)
        workspace.open(.powershell)
        let session = try XCTUnwrap(workspace.selected)
        await wait("the held pty.open") { backend.heldOpenCount == 1 }
        XCTAssertEqual(session.phase, .opening)
        workspace.requestClose(session)
        XCTAssertNil(workspace.pendingClose)
        XCTAssertTrue(workspace.sessions.isEmpty)
        backend.releaseOpens()
        await wait("the job ended") { backend.killed == ["j-1"] }
        XCTAssertTrue(backend.attaches.isEmpty)
    }

    func testJobEventsSubscribeAgainAfterTheStreamFails() async throws {
        workspace.start()
        await wait("the subscription") { backend.subscriptions == 1 }
        backend.failEvents()
        await wait("a new subscription") { backend.subscriptions == 2 }
        backend.pushJobUpdate(ptyJob("j-9"))
        await wait("the event on the new stream") { workspace.detachedJobs.contains { $0.jobID == "j-9" } }
    }

    func testJobListFailureIsKeptUntilAReloadSucceeds() async throws {
        backend.failJobs(OperationError(code: "OFFLINE", message: "연결되어 있지 않습니다"))
        workspace.start()
        await wait("the failure") { workspace.jobsError?.code == "OFFLINE" }
        backend.failJobs(nil)
        backend.setJobs([ptyJob("j-5")])
        workspace.refreshJobs()
        await wait("the reload") { workspace.jobsError == nil && workspace.detachedJobs.map(\.jobID) == ["j-5"] }
    }

    func testPasteWithControlCharactersAlwaysAsks() async throws {
        let (session, stream) = try await liveSession()
        workspace.preferences.confirmsMultilinePaste = false
        workspace.requestPaste(session, text: "ls\u{1b}[201~ -la")
        let request = try XCTUnwrap(workspace.pendingPaste)
        XCTAssertEqual(request.text, "ls -la")
        XCTAssertEqual(request.removedControls, 1)
        XCTAssertTrue(workspace.confirmPaste(request, dontAskAgain: false))
        await wait("the pasted text") { dataFrames(stream) == [Data("ls -la".utf8)] }
    }

    func testBracketedPasteCannotBeEndedEarly() async throws {
        let (session, stream) = try await liveSession()
        stream.push(TerminalFrameKind.data, Data("\u{1b}[?2004h".utf8))
        await wait("bracketed paste mode") { session.surface.getTerminal().bracketedPasteMode }
        workspace.requestPaste(session, text: "a\u{1b}[201~b\n")
        let request = try XCTUnwrap(workspace.pendingPaste)
        XCTAssertTrue(workspace.confirmPaste(request, dontAskAgain: false))
        await wait("the pasted text") { !dataFrames(stream).isEmpty }
        XCTAssertEqual(dataFrames(stream), [Data("\u{1b}[200~ab\r\u{1b}[201~".utf8)])
    }

    func testPasteIsKeptWhenTheStreamDropsBeforeConfirming() async throws {
        let (session, stream) = try await liveSession()
        workspace.requestPaste(session, text: "whoami\nhostname\n")
        let request = try XCTUnwrap(workspace.pendingPaste)
        XCTAssertTrue(workspace.canSendPaste(request))
        stream.end()
        await wait("reconnecting") { session.phase == .reconnecting(nil) }
        XCTAssertFalse(workspace.canSendPaste(request))
        XCTAssertFalse(workspace.confirmPaste(request, dontAskAgain: false))
        XCTAssertEqual(workspace.pendingPaste, request)
        XCTAssertTrue(dataFrames(stream).isEmpty)
    }
}
