import DeskhandClient
import DeskhandMedia
import Foundation
import Synchronization
import XCTest
@testable import DeskhandUI

/// A screen stream the test feeds frames into and whose sent frames it reads.
final class FakeScreenStream: ScreenStream {
    let frames: AsyncThrowingStream<StreamConnection.Frame, Error>
    private let continuation: AsyncThrowingStream<StreamConnection.Frame, Error>.Continuation
    private let sentFrames = Mutex<[(kind: UInt8, payload: Data)]>([])
    private let closed = Mutex(false)

    init() {
        (frames, continuation) = AsyncThrowingStream.makeStream(of: StreamConnection.Frame.self)
    }

    var sent: [(kind: UInt8, payload: Data)] { sentFrames.withLock { $0 } }
    var isClosed: Bool { closed.withLock { $0 } }

    func push(_ kind: UInt8, _ payload: Data) {
        continuation.yield((kind: kind, payload: payload))
    }

    /// Ends the stream from the daemon side.
    func end() {
        continuation.finish()
    }

    func send(kind: UInt8, payload: Data) async throws {
        sentFrames.withLock { $0.append((kind, payload)) }
    }

    func close() {
        closed.withLock { $0 = true }
        continuation.finish()
    }
}

/// Opens a `FakeScreenStream` per attach, or fails with `failure`.
final class FakeScreenBackend: ScreenBackend {
    private let opened = Mutex<[FakeScreenStream]>([])
    private let failure: (any Error & Sendable)?

    init(failure: (any Error & Sendable)? = nil) {
        self.failure = failure
    }

    var streams: [FakeScreenStream] { opened.withLock { $0 } }

    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        if let failure { throw failure }
        let stream = FakeScreenStream()
        opened.withLock { $0.append(stream) }
        return stream
    }
}

@MainActor
final class ScreenSessionTests: XCTestCase {
    private var backend: FakeScreenBackend!
    private var session: ScreenSession!
    private var runTask: Task<Void, Never>?

    private static let fixture: [Data] = {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../Fixtures/sample.h264")
        return AnnexB.accessUnits((try? Data(contentsOf: url)) ?? Data())
    }()

    override func tearDown() async throws {
        runTask?.cancel()
        await runTask?.value
    }

    private func start(failure: (any Error & Sendable)? = nil) {
        backend = FakeScreenBackend(failure: failure)
        session = ScreenSession(backend: backend, preset: .balanced)
        let session = session!
        runTask = Task { await session.run(hostID: "windows-dev", display: 0, preset: .balanced) }
    }

    private func wait(_ what: String, timeout: TimeInterval = 3, until condition: () -> Bool) async {
        let deadline = Date.now.addingTimeInterval(timeout)
        while !condition() {
            if Date.now > deadline {
                XCTFail("timed out waiting for \(what)")
                return
            }
            try? await Task.sleep(for: .milliseconds(10))
        }
    }

    private func attached() async -> FakeScreenStream? {
        await wait("an attached stream") { backend.streams.count == 1 }
        return backend.streams.first
    }

    private func au(_ index: Int, seq: UInt64) -> Data {
        AUFrame(key: index == 0, hasConfig: index == 0, width: 1920, height: 1080, seq: seq, captureMonoUS: seq * 16_667,
                encodeDoneMonoUS: seq * 16_667 + 3_000, data: Self.fixture[index]).payload
    }

    private func objects(_ stream: FakeScreenStream, kind: UInt8) -> [[String: Any]] {
        stream.sent.filter { $0.kind == kind }.compactMap { (try? JSONSerialization.jsonObject(with: $0.payload)) as? [String: Any] }
    }

    func testKeyframeTurnsTheSessionLive() async throws {
        start()
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        XCTAssertEqual(session.phase, .connecting)
        stream.push(ScreenFrameKind.info, ScreenJSON.encode(StreamInfo(width: 1920, height: 1080, srcWidth: 3840, srcHeight: 2160)))
        stream.push(ScreenFrameKind.accessUnit, au(0, seq: 0))
        await wait("live") { session.phase == .live }
        XCTAssertTrue(session.hasFrame)
        XCTAssertEqual(session.nativeSize, CGSize(width: 3840, height: 2160))
    }

    func testAccessUnitBeforeAnyKeyframeRequestsOne() async throws {
        start()
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        stream.push(ScreenFrameKind.accessUnit, au(1, seq: 1))
        await wait("a keyframe request") { !objects(stream, kind: ScreenFrameKind.control).isEmpty }
        XCTAssertEqual(objects(stream, kind: ScreenFrameKind.control).first?["op"] as? String, "keyframe")
        XCTAssertEqual(session.phase, .connecting)
    }

    func testPausedFrameShowsTheReason() async throws {
        start()
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        stream.push(ScreenFrameKind.accessUnit, au(0, seq: 0))
        await wait("live") { session.phase == .live }
        stream.push(ScreenFrameKind.paused, Data(#"{"reason":"secure_desktop"}"#.utf8))
        await wait("paused") { session.phase == .paused(.secureDesktop) }
        stream.push(ScreenFrameKind.accessUnit, au(1, seq: 1))
        await wait("live again") { session.phase == .live }
    }

    func testInputFramesCarryIncreasingSeqAndTheControlGen() async throws {
        start()
        session.gen = { 7 }
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        await wait("the input outbox") { session.phase == .connecting }
        try? await Task.sleep(for: .milliseconds(50))
        session.send(.move(x: 10, y: 20, display: 0))
        session.send(.button(.left, down: true))
        session.send(.button(.left, down: false))
        await wait("three input frames") { objects(stream, kind: ScreenFrameKind.input).count == 3 }
        let sent = objects(stream, kind: ScreenFrameKind.input)
        XCTAssertEqual(sent.map { $0["seq"] as? Int }, [1, 2, 3])
        XCTAssertEqual(sent.map { $0["gen"] as? Int }, [7, 7, 7])
        XCTAssertEqual(sent.map { $0["k"] as? String }, ["move", "button", "button"])
        XCTAssertEqual(sent.map { $0["down"] as? Bool }, [nil, true, false])
    }

    func testPresetChangeIsSentOnTheLiveStream() async throws {
        start()
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        try? await Task.sleep(for: .milliseconds(50))
        session.setPreset(.sharp)
        await wait("a preset frame") { !objects(stream, kind: ScreenFrameKind.control).isEmpty }
        let control = try XCTUnwrap(objects(stream, kind: ScreenFrameKind.control).first)
        XCTAssertEqual(control["op"] as? String, "preset")
        XCTAssertEqual(control["preset"] as? String, "sharp")
    }

    func testEndedStreamIsAttachedAgain() async throws {
        start()
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        stream.push(ScreenFrameKind.accessUnit, au(0, seq: 0))
        await wait("live") { session.phase == .live }
        stream.end()
        await wait("reconnecting") { session.phase == .reconnecting }
        await wait("a second attach") { backend.streams.count == 2 }
    }

    func testCancellingTheRunClosesTheStream() async throws {
        start()
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        runTask?.cancel()
        await runTask?.value
        XCTAssertTrue(stream.isClosed)
        XCTAssertEqual(session.phase, .idle)
    }

    func testUnsupportedScreenFailsWithoutRetrying() async throws {
        start(failure: RPCErrorInfo(message: "이 에이전트 버전은 화면 스트리밍을 지원하지 않습니다", code: "CAPABILITY_UNSUPPORTED"))
        await runTask?.value
        guard case .failed(let error) = session.phase else { return XCTFail("phase \(session.phase)") }
        XCTAssertEqual(error.code, "CAPABILITY_UNSUPPORTED")
    }

    func testLatencyNoticeAddsCaptureToDisplayLatencyToTheQuality() async throws {
        start()
        let qualities = Mutex<[StreamQuality?]>([])
        session.onQuality = { quality in qualities.withLock { $0.append(quality) } }
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        stream.push(ScreenFrameKind.accessUnit, au(0, seq: 0))
        stream.push(ScreenFrameKind.accessUnit, au(1, seq: 1))
        stream.push(ScreenFrameKind.notice, Data(#"{"type":"latency","capture_to_receive_ms":14}"#.utf8))
        await wait("a reported quality", timeout: 4) { qualities.withLock { $0.contains { $0 != nil } } }
        let quality = try XCTUnwrap(qualities.withLock { $0.compactMap { $0 }.first })
        XCTAssertEqual(quality.fps, 2)
        XCTAssertNotNil(quality.bitsPerSecond)
        let latency = try XCTUnwrap(quality.latencyMS)
        XCTAssertGreaterThanOrEqual(latency, 14)
        XCTAssertLessThan(latency, 30)
    }

    func testWithoutLatencyNoticeTheQualityHasNoLatency() async throws {
        start()
        let qualities = Mutex<[StreamQuality?]>([])
        session.onQuality = { quality in qualities.withLock { $0.append(quality) } }
        let attachedStream = await attached()
        let stream = try XCTUnwrap(attachedStream)
        stream.push(ScreenFrameKind.accessUnit, au(0, seq: 0))
        await wait("a reported quality", timeout: 4) { qualities.withLock { $0.contains { $0 != nil } } }
        let quality = try XCTUnwrap(qualities.withLock { $0.compactMap { $0 }.first })
        XCTAssertNil(quality.latencyMS)
    }
}
