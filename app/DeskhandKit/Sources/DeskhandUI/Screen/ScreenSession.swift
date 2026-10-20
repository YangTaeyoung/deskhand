import AVFoundation
import DeskhandClient
import DeskhandMedia
import Foundation
import Observation
import Synchronization
import os

/// The live screen of one computer's display: attaches the `screen` stream, feeds access units
/// to the display layer from a background task, forwards cursor frames to the surface, measures
/// the stream once a second and sends input and control frames in order. Re-attaches with
/// backoff when the stream ends.
@MainActor
@Observable
public final class ScreenSession {
    public enum Phase: Equatable, Sendable {
        /// No stream is attached.
        case idle
        /// Attaching, or waiting for the first displayable frame.
        case connecting
        case live
        /// The agent stopped sending frames (protocol.md §4 `0x04`).
        case paused(PauseReason)
        /// The stream or the daemon's connection to the PC dropped; attaching again.
        case reconnecting
        /// The daemon refused the stream with an error that retrying does not fix.
        case failed(OperationError)
    }

    /// A cursor shape ready to draw.
    struct CursorImage: Equatable {
        let image: CGImage
        /// Hot spot in image pixels from the top left.
        let hotSpot: CGPoint
        let pixelSize: CGSize
    }

    public private(set) var phase: Phase = .idle
    /// True once a frame of the current display was handed to the display layer.
    public private(set) var hasFrame = false
    /// True while the display layer holds a decoded frame it can show.
    public private(set) var isReadyForDisplay = false
    public private(set) var info: StreamInfo?
    public private(set) var stats: StreamStats?
    public private(set) var metrics: FrameClock.Snapshot?
    public private(set) var isCaptured = false
    /// Text the Mac input method is composing.
    public private(set) var markedText: String?
    /// The latest failure while attaching; cleared when frames arrive.
    public private(set) var connectError: OperationError?
    /// When the daemon last dropped human input because an AI client holds control.
    public private(set) var inputDeniedAt: Date?
    /// Increases each time the person retries after `failed`.
    public private(set) var retries = 0

    /// Writer generation sent with input events: the latest known control `gen`.
    @ObservationIgnored public var gen: () -> Int = { 0 }
    /// Receives the stream quality once a second while live, and nil otherwise.
    @ObservationIgnored public var onQuality: (StreamQuality?) -> Void = { _ in }

    @ObservationIgnored public private(set) lazy var displayLayer: AVSampleBufferDisplayLayer = {
        let layer = AVSampleBufferDisplayLayer()
        layer.videoGravity = .resize
        layer.backgroundColor = CGColor(gray: 0, alpha: 1)
        return layer
    }()

    @ObservationIgnored private let backend: any ScreenBackend
    @ObservationIgnored private weak var surface: ScreenSurfaceView?
    @ObservationIgnored private var outbox: AsyncStream<OutFrame>.Continuation?
    @ObservationIgnored private var seq = 0
    @ObservationIgnored private var preset: StreamPreset
    @ObservationIgnored private var streamToken = 0
    @ObservationIgnored private var currentDisplay: (hostID: String, display: Int)?
    @ObservationIgnored private var cursorImages: [UInt32: CursorImage] = [:]
    @ObservationIgnored private var cursorPosition: CursorPosition?
    @ObservationIgnored private var readyObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var runToken = 0
    /// Native size of the display from the host's display list, used until the stream reports its source size.
    @ObservationIgnored public var fallbackSize: CGSize = .zero

    nonisolated static let log = Logger(subsystem: "io.github.yangtaeyoung.Deskhand", category: "screen")

    public init(backend: any ScreenBackend, preset: StreamPreset) {
        self.backend = backend
        self.preset = preset
    }

    isolated deinit {
        if let readyObserver { NotificationCenter.default.removeObserver(readyObserver) }
    }

    /// A session showing fixed state, for snapshot stand-ins.
    public static func standIn(phase: Phase, hasFrame: Bool = true, info: StreamInfo? = nil, stats: StreamStats? = nil,
                               metrics: FrameClock.Snapshot? = nil, captured: Bool = false, markedText: String? = nil,
                               connectError: OperationError? = nil) -> ScreenSession {
        let session = ScreenSession(backend: UnavailableScreenBackend(), preset: .balanced)
        session.phase = phase
        session.hasFrame = hasFrame
        session.isReadyForDisplay = hasFrame
        session.info = info
        session.stats = stats
        session.metrics = metrics
        session.isCaptured = captured
        session.markedText = markedText
        session.connectError = connectError
        return session
    }

    /// The remote display size in its own pixels: the source size the encoder reports, the
    /// host's display list entry, or the encoded size.
    public var nativeSize: CGSize {
        if let info, let w = info.srcWidth, let h = info.srcHeight, w > 0, h > 0 { return CGSize(width: w, height: h) }
        if fallbackSize.width > 0, fallbackSize.height > 0 { return fallbackSize }
        if let info, info.width > 0, info.height > 0 { return CGSize(width: info.width, height: info.height) }
        return .zero
    }

    // MARK: Running

    /// Attaches the stream of `display` and keeps it attached until the task is cancelled,
    /// attaching again with backoff when it ends. Returns early in `failed` for errors that
    /// retrying does not fix.
    public func run(hostID: String, display: Int, preset: StreamPreset) async {
        runToken += 1
        let run = runToken
        self.preset = preset
        observeReadiness()
        if let current = currentDisplay, current.hostID != hostID || current.display != display {
            clearDisplay()
        }
        currentDisplay = (hostID, display)
        var delay = Duration.milliseconds(500)
        while !Task.isCancelled, run == runToken {
            phase = hasFrame ? .reconnecting : .connecting
            let stream: any ScreenStream
            do {
                stream = try await backend.screenAttach(hostID: hostID, display: display, preset: self.preset)
            } catch {
                if Task.isCancelled { break }
                let failure = OperationError(error)
                if Self.isPermanent(failure) {
                    phase = .failed(failure)
                    onQuality(nil)
                    return
                }
                connectError = failure
                try? await Task.sleep(for: delay)
                delay = min(delay * 2, .seconds(8))
                continue
            }
            if Task.isCancelled {
                stream.close()
                break
            }
            let received = await consume(stream)
            stream.close()
            onQuality(nil)
            if Task.isCancelled { break }
            if received { delay = .milliseconds(500) }
            phase = hasFrame ? .reconnecting : .connecting
            try? await Task.sleep(for: delay)
            delay = min(delay * 2, .seconds(8))
        }
        guard run == runToken else { return }
        streamToken += 1
        onQuality(nil)
        phase = .idle
    }

    /// Asks `run` to try again after `failed`; the caller restarts the task keyed on `retries`.
    public func retry() {
        connectError = nil
        retries += 1
    }

    /// Error codes for which attaching again cannot succeed without a change by the person.
    static func isPermanent(_ error: OperationError) -> Bool {
        switch error.code {
        case "NOT_FOUND", "INVALID_ARGUMENT", "CAPABILITY_UNSUPPORTED", "PERMISSION_DENIED", "AUTH_REQUIRED",
             "ELEVATION_REQUIRED":
            true
        default:
            false
        }
    }

    private func observeReadiness() {
        guard readyObserver == nil else { return }
        readyObserver = NotificationCenter.default.addObserver(
            forName: .AVSampleBufferDisplayLayerReadyForDisplayDidChange, object: displayLayer, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.readinessChanged() }
        }
    }

    private func readinessChanged() {
        let ready = displayLayer.isReadyForDisplay
        guard ready != isReadyForDisplay else { return }
        isReadyForDisplay = ready
        if ready, let frame = displayLayer.sampleBufferRenderer.displayedPixelBuffer() {
            Self.log.info("displaying a \(CVPixelBufferGetWidth(frame))×\(CVPixelBufferGetHeight(frame)) frame")
        }
    }

    private func clearDisplay() {
        displayLayer.sampleBufferRenderer.flush(removingDisplayedImage: true, completionHandler: nil)
        hasFrame = false
        info = nil
        stats = nil
        metrics = nil
        cursorImages = [:]
        cursorPosition = nil
        surface?.setCursor(position: nil, image: nil)
    }

    /// Pumps `stream` until it ends or the task is cancelled; returns whether any access unit arrived.
    private func consume(_ stream: any ScreenStream) async -> Bool {
        streamToken += 1
        let token = streamToken
        let (outFrames, outbox) = AsyncStream.makeStream(of: OutFrame.self)
        self.outbox = outbox
        let sender = Task.detached {
            for await frame in outFrames {
                try? await stream.send(kind: frame.kind, payload: frame.payload)
            }
        }
        let renderer = RendererBox(displayLayer.sampleBufferRenderer)
        let metrics = MetricsBox()
        let sink = UpdateSink(session: self, token: token)
        let pump = Task.detached {
            await ScreenPump.run(stream: stream, renderer: renderer, metrics: metrics, sink: sink)
        }
        let ticker = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled else { return }
                self?.tick(metrics, token: token)
            }
        }
        let received = await withTaskCancellationHandler {
            await pump.value
        } onCancel: {
            pump.cancel()
            stream.close()
        }
        ticker.cancel()
        outbox.finish()
        sender.cancel()
        if self.outbox != nil, token == streamToken { self.outbox = nil }
        return received
    }

    private func tick(_ metrics: MetricsBox, token: Int) {
        guard token == streamToken else { return }
        let snapshot = metrics.clock.withLock { $0.snapshot(at: ProcessInfo.processInfo.systemUptime) }
        self.metrics = snapshot
        guard phase == .live else {
            onQuality(nil)
            return
        }
        onQuality(StreamQuality(fps: snapshot.fps, latencyMS: snapshot.latencyMS.map { Int($0.rounded()) },
                                bitsPerSecond: snapshot.bitsPerSecond))
    }

    fileprivate func apply(_ update: PumpUpdate, token: Int) {
        guard token == streamToken else { return }
        switch update {
        case .live:
            phase = .live
            hasFrame = true
            connectError = nil
        case .info(let info):
            self.info = info
            surface?.remoteSize = nativeSize
        case .stats(let stats):
            self.stats = stats
        case .paused(let reason):
            phase = .paused(reason)
            onQuality(nil)
        case .cursorShape(let shape):
            guard let image = shape.makeImage() else { return }
            if cursorImages.count >= 64 { cursorImages.removeAll() }
            cursorImages[shape.id] = CursorImage(image: image, hotSpot: CGPoint(x: shape.hotX, y: shape.hotY),
                                                 pixelSize: CGSize(width: shape.width, height: shape.height))
            pushCursor()
        case .cursorPosition(let position):
            cursorPosition = position
            pushCursor()
        case .notice(let notice):
            switch notice {
            case .controlDenied:
                inputDeniedAt = .now
            case .connectionState(let state):
                guard state != "ready" else { return }
                phase = .reconnecting
                onQuality(nil)
            case .latency, .ownerChanged, .other:
                break
            }
        }
    }

    private func pushCursor() {
        surface?.setCursor(position: cursorPosition, image: cursorPosition.flatMap { cursorImages[$0.shapeID] })
    }

    // MARK: Surface and input

    /// Connects the view that shows this session: its display layer, cursor and input.
    func attach(surface: ScreenSurfaceView) {
        self.surface = surface
        surface.attach(displayLayer: displayLayer)
        surface.remoteSize = nativeSize
        surface.capture.onEvent = { [weak self] in self?.send($0) }
        surface.capture.onCaptureChange = { [weak self, weak surface] captured in
            self?.isCaptured = captured
            surface?.refreshCursor()
        }
        surface.capture.onMarkedText = { [weak self] in self?.markedText = $0 }
        pushCursor()
    }

    /// Sends a human input event (`0x41`) with the next sequence number and the current `gen`.
    public func send(_ event: InputEvent) {
        guard let outbox else { return }
        seq += 1
        outbox.yield(OutFrame(kind: ScreenFrameKind.input, payload: event.json(seq: seq, gen: gen())))
    }

    /// Switches the live stream to `preset` (`0x40` `preset`); later attaches use it too.
    public func setPreset(_ preset: StreamPreset) {
        guard preset != self.preset else { return }
        self.preset = preset
        outbox?.yield(OutFrame(kind: ScreenFrameKind.control, payload: ScreenControl.preset(preset.rawValue).json))
    }

    /// Asks the agent for a keyframe (`0x40` `keyframe`).
    public func requestKeyframe() {
        outbox?.yield(OutFrame(kind: ScreenFrameKind.control, payload: ScreenControl.keyframe.json))
    }

    /// Hides the notice that input was dropped while an AI client held control.
    public func dismissDeniedNotice() {
        inputDeniedAt = nil
    }
}

/// One app → daemon frame.
struct OutFrame: Sendable {
    let kind: UInt8
    let payload: Data
}

/// What the frame pump reports to the session on the main actor.
enum PumpUpdate: Sendable {
    case live
    case info(StreamInfo)
    case stats(StreamStats)
    case paused(PauseReason)
    case cursorShape(CursorShape)
    case cursorPosition(CursorPosition)
    case notice(StreamNotice)
}

/// Delivers pump updates to the session on the main queue, in order. Updates of an earlier
/// stream (`token`) are dropped by the session.
struct UpdateSink: Sendable {
    weak var session: ScreenSession?
    let token: Int

    func post(_ update: PumpUpdate) {
        let session = session
        let token = token
        DispatchQueue.main.async {
            MainActor.assumeIsolated { session?.apply(update, token: token) }
        }
    }
}

/// The display layer's renderer, which accepts sample buffers and flushes from a background
/// thread (`AVSampleBufferDisplayLayer.sampleBufferRenderer`).
final class RendererBox: @unchecked Sendable {
    let renderer: AVSampleBufferVideoRenderer

    init(_ renderer: AVSampleBufferVideoRenderer) {
        self.renderer = renderer
    }
}

/// The frame clock shared by the pump and the once-a-second tick.
final class MetricsBox: Sendable {
    let clock = Mutex(FrameClock())
}

/// Reads the frames of one stream: decodes nothing itself, but hands access units to the
/// display layer's renderer as sample buffers, flushing and asking for a keyframe after a
/// decode failure or when an access unit needs one.
enum ScreenPump {
    static func run(stream: any ScreenStream, renderer: RendererBox, metrics: MetricsBox, sink: UpdateSink) async -> Bool {
        var h264 = H264Stream()
        var live = false
        var received = false
        var lastKeyframeRequest = -TimeInterval.infinity

        func requestKeyframe(at now: TimeInterval) async {
            guard now - lastKeyframeRequest > 0.5 else { return }
            lastKeyframeRequest = now
            try? await stream.send(kind: ScreenFrameKind.control, payload: ScreenControl.keyframe.json)
        }

        do {
            for try await frame in stream.frames {
                switch frame.kind {
                case ScreenFrameKind.accessUnit:
                    let receivedAt = ProcessInfo.processInfo.systemUptime
                    received = true
                    guard let au = try? AUFrame(payload: frame.payload) else { continue }
                    let output = renderer.renderer
                    if output.status == .failed || output.requiresFlushToResumeDecoding {
                        ScreenSession.log.error("display layer failed: \(output.error?.localizedDescription ?? "unknown", privacy: .public); flushing")
                        output.flush()
                        h264.reset()
                    }
                    let result: H264Stream.Output
                    do {
                        result = try h264.feed(au)
                    } catch {
                        ScreenSession.log.error("access unit \(au.seq) rejected: \(String(describing: error), privacy: .public)")
                        h264.reset()
                        result = .needsKeyframe
                    }
                    switch result {
                    case .needsKeyframe:
                        await requestKeyframe(at: receivedAt)
                    case .sample(let buffer, _):
                        output.enqueue(buffer)
                        let enqueuedAt = ProcessInfo.processInfo.systemUptime
                        metrics.clock.withLock { $0.frame(bytes: frame.payload.count, receivedAt: receivedAt, enqueuedAt: enqueuedAt) }
                        if !live {
                            live = true
                            sink.post(.live)
                        }
                    }
                case ScreenFrameKind.info:
                    if let info = ScreenJSON.decode(StreamInfo.self, from: frame.payload) { sink.post(.info(info)) }
                case ScreenFrameKind.stats:
                    if let stats = ScreenJSON.decode(StreamStats.self, from: frame.payload) { sink.post(.stats(stats)) }
                case ScreenFrameKind.paused:
                    live = false
                    sink.post(.paused(ScreenJSON.pauseReason(from: frame.payload) ?? .other("unknown")))
                case ScreenFrameKind.cursorShape:
                    if let shape = try? CursorShape(payload: frame.payload) { sink.post(.cursorShape(shape)) }
                case ScreenFrameKind.cursorPosition:
                    if let position = try? CursorPosition(payload: frame.payload) { sink.post(.cursorPosition(position)) }
                case ScreenFrameKind.notice:
                    guard let notice = StreamNotice(json: frame.payload) else { continue }
                    switch notice {
                    case .latency(let ms):
                        metrics.clock.withLock { $0.latency(ms, at: ProcessInfo.processInfo.systemUptime) }
                    case .connectionState(let state) where state != "ready":
                        live = false
                    default:
                        break
                    }
                    sink.post(.notice(notice))
                default:
                    continue
                }
            }
        } catch {
            ScreenSession.log.info("screen stream ended: \(String(describing: error), privacy: .public)")
        }
        return received
    }
}

/// Backend of stand-in sessions, which never attach.
private struct UnavailableScreenBackend: ScreenBackend {
    func screenAttach(hostID: String, display: Int, preset: StreamPreset) async throws -> any ScreenStream {
        throw ClientError.notConnected
    }
}
