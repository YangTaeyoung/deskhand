import DeskhandClient
import DeskhandMedia
import Foundation
import Synchronization
import XCTest
@testable import DeskhandUI

private final class RDPTestStream: ScreenStream {
    let frames: AsyncThrowingStream<StreamConnection.Frame, Error>
    let continuation: AsyncThrowingStream<StreamConnection.Frame, Error>.Continuation
    let closed = Mutex(false)
    let sent = Mutex<[Data]>([])
    init() { (frames, continuation) = AsyncThrowingStream.makeStream() }
    func send(kind: UInt8, payload: Data) async throws {
        if closed.withLock({ $0 }) { throw CancellationError() }
        sent.withLock { $0.append(payload) }
    }
    func close() { closed.withLock { $0 = true }; continuation.finish() }
}

private actor RDPDeferredBackend: ScreenBackend {
    private var pending: [CheckedContinuation<any ScreenStream, Error>?] = []
    var pendingCount: Int { pending.count }
    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        throw OperationError(code: "UNEXPECTED_VIDEO", message: "video must not be attached")
    }
    func inputAttach(hostID: String, sessionID: UInt32, sid: String, connectionGeneration: Int) async throws -> any ScreenStream {
        try await withCheckedThrowingContinuation { pending.append($0) }
    }
    func complete(_ index: Int, with stream: RDPTestStream) {
        let continuation = pending[index]; pending[index] = nil; continuation?.resume(returning: stream)
    }
    func fail(_ index: Int, code: String) {
        let continuation = pending[index]; pending[index] = nil
        continuation?.resume(throwing: OperationError(code: code, message: "test refusal"))
    }
    func disconnect(_ index: Int) {
        let continuation = pending[index]; pending[index] = nil
        continuation?.resume(throwing: ClientError.disconnected)
    }
}

@MainActor
final class RDPInputChannelTests: XCTestCase {
    private func waitFor(_ check: () async -> Bool) async throws {
        let end = ContinuousClock.now + .seconds(3)
        while !(await check()) {
            if ContinuousClock.now > end { XCTFail("condition did not become true"); return }
            try await Task.sleep(for: .milliseconds(1))
        }
    }

    func testPhysicalHoldTransitionsReachTheDaemonWithoutDuplication() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel(), stream = RDPTestStream()
        let task = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "sid", connectionGeneration: 1) }
        try await waitFor { await backend.pendingCount == 1 }
        await backend.complete(0, with: stream)
        try await waitFor { channel.ready }
        channel.reportHumanActivity(holding: true)
        channel.reportHumanActivity(holding: true)
        channel.reportHumanActivity(holding: false)
        try await waitFor { stream.sent.withLock { $0.count == 2 } }
        let payloads = stream.sent.withLock { $0 }
        let values = try payloads.map { try XCTUnwrap(JSONSerialization.jsonObject(with: $0) as? [String: Any]) }
        XCTAssertEqual(values.map { $0["k"] as? String }, ["human_activity", "human_activity"])
        XCTAssertEqual(values.map { $0["down"] as? Bool }, [true, false])
        task.cancel(); channel.close(); await task.value
    }

    func testLateAttachCannotReplaceOrCloseTheNewSession() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel()
        let old = RDPTestStream(), current = RDPTestStream()
        let first = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "old", connectionGeneration: 1) }
        try await waitFor { await backend.pendingCount == 1 }
        let second = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 2, sid: "new", connectionGeneration: 2) }
        try await waitFor { await backend.pendingCount == 2 }
        await backend.complete(1, with: current)
        try await waitFor { channel.ready }
        await backend.complete(0, with: old)
        await first.value
        XCTAssertTrue(channel.ready)
        XCTAssertTrue(old.closed.withLock { $0 })
        XCTAssertFalse(current.closed.withLock { $0 })
        channel.send(.text("x"), gen: 3)
        try await waitFor { current.sent.withLock { !$0.isEmpty } }
        XCTAssertTrue(old.sent.withLock { $0.isEmpty })
        second.cancel(); channel.close(); await second.value
        XCTAssertTrue(current.closed.withLock { $0 })
    }

    func testBacklogClosesInputInsteadOfDroppingAKeyRelease() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel(), stream = RDPTestStream()
        let task = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "sid", connectionGeneration: 1) }
        try await waitFor { await backend.pendingCount == 1 }
        await backend.complete(0, with: stream)
        try await waitFor { channel.ready }
        // No await: fill the bounded sender queue before its main-actor task can drain.
        for _ in 0..<300 { channel.send(.releaseAll, gen: 1) }
        XCTAssertFalse(channel.ready)
        XCTAssertTrue(stream.closed.withLock { $0 })
        XCTAssertEqual(channel.failure?.code, "INPUT_BACKLOG")
        task.cancel(); await task.value
    }

    func testDroppedInputStreamReconnectsWithoutReplayingInput() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel()
        let old = RDPTestStream(), replacement = RDPTestStream()
        let task = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "sid", connectionGeneration: 3) }
        try await waitFor { await backend.pendingCount == 1 }
        await backend.complete(0, with: old)
        try await waitFor { channel.ready }
        channel.send(.text("once"), gen: 1)
        try await waitFor { old.sent.withLock { $0.count == 1 } }
        old.close()
        try await Task.sleep(for: .milliseconds(750))
        let count = await backend.pendingCount
        XCTAssertEqual(count, 2, "a dropped input stream should reconnect automatically")
        guard count == 2 else { task.cancel(); channel.close(); await task.value; return }
        await backend.complete(1, with: replacement)
        try await waitFor { channel.ready }
        XCTAssertTrue(replacement.sent.withLock { $0.isEmpty }, "input from the old stream must never be replayed")
        task.cancel(); channel.close(); await task.value
    }

    func testClosingDuringBackoffDoesNotReconnect() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel(), stream = RDPTestStream()
        let task = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "sid", connectionGeneration: 1) }
        try await waitFor { await backend.pendingCount == 1 }
        await backend.complete(0, with: stream)
        try await waitFor { channel.ready }
        stream.close()
        try await waitFor { !channel.ready }
        channel.close()
        await task.value
        let count = await backend.pendingCount
        XCTAssertEqual(count, 1)
    }

    func testChangedSessionRefusalIsNeverRetried() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel()
        let task = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "sid", connectionGeneration: 1) }
        try await waitFor { await backend.pendingCount == 1 }
        await backend.fail(0, code: "STALE_TARGET")
        await task.value
        let count = await backend.pendingCount
        XCTAssertEqual(count, 1)
        XCTAssertEqual(channel.failure?.code, "STALE_TARGET")
    }

    func testUnreachableInputStopsAfterThreeRetries() async throws {
        let backend = RDPDeferredBackend(), channel = RDPInputChannel()
        let task = Task { await channel.run(backend: backend, hostID: "pc", sessionID: 1, sid: "sid", connectionGeneration: 1) }
        for i in 0..<4 {
            try await waitFor { await backend.pendingCount == i + 1 }
            await backend.disconnect(i)
        }
        await task.value
        let count = await backend.pendingCount
        XCTAssertEqual(count, 4)
        XCTAssertFalse(channel.ready)
        XCTAssertEqual(channel.failure?.code, "OFFLINE")
        XCTAssertTrue(channel.failure?.message.contains("복구하지 못") == true)
    }
}
