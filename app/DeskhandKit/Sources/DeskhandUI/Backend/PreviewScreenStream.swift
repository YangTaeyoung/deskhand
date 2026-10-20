import CoreGraphics
import DeskhandClient
import DeskhandMedia
import Foundation
import Synchronization
import os
import struct DeskhandClient.Host

/// Screen stream of `PreviewBackend`. Plays H.264 access units in a loop at 60 fps with the
/// frames an agent sends around them: stream info, an arrow cursor that circles while an AI
/// client holds control and follows input otherwise, and statistics and a latency notice once a
/// second. A locked session or a missing display is reported as paused. Input frames are logged
/// (category `preview.input`, and standard error); `keyframe` restarts the loop at its keyframe.
final class PreviewScreenStream: ScreenStream {
    let frames: AsyncThrowingStream<StreamConnection.Frame, Error>
    private let continuation: AsyncThrowingStream<StreamConnection.Frame, Error>.Continuation

    private struct State {
        var restart = false
        var cursor: (x: Int32, y: Int32)?
        var lastInput = -TimeInterval.infinity
        var preset: StreamPreset
        var producer: Task<Void, Never>?
    }

    private let state: Mutex<State>
    static let log = Logger(subsystem: "io.github.yangtaeyoung.Deskhand", category: "preview.input")
    static let frameInterval = Duration.microseconds(16_667)

    init(host: Host, display: Int, preset: StreamPreset, units: [Data], aiOwns: @escaping @Sendable () -> Bool) {
        (frames, continuation) = AsyncThrowingStream.makeStream(of: StreamConnection.Frame.self)
        state = Mutex(State(preset: preset))
        let producer = Task { [self] in
            await play(host: host, display: display, units: units, aiOwns: aiOwns)
        }
        state.withLock { $0.producer = producer }
        continuation.onTermination = { _ in producer.cancel() }
    }

    func send(kind: UInt8, payload: Data) async throws {
        let text = String(decoding: payload, as: UTF8.self)
        switch kind {
        case ScreenFrameKind.input:
            Self.log.info("input \(text, privacy: .public)")
            FileHandle.standardError.write(Data("[preview input] \(text)\n".utf8))
            guard let object = (try? JSONSerialization.jsonObject(with: payload)) as? [String: Any] else { return }
            let now = ProcessInfo.processInfo.systemUptime
            state.withLock {
                $0.lastInput = now
                if object["k"] as? String == "move", let x = (object["x"] as? NSNumber)?.int32Value,
                   let y = (object["y"] as? NSNumber)?.int32Value {
                    $0.cursor = (x, y)
                }
            }
        case ScreenFrameKind.control:
            Self.log.info("control \(text, privacy: .public)")
            FileHandle.standardError.write(Data("[preview control] \(text)\n".utf8))
            guard let object = (try? JSONSerialization.jsonObject(with: payload)) as? [String: Any] else { return }
            switch object["op"] as? String {
            case "keyframe":
                state.withLock { $0.restart = true }
            case "preset":
                if let preset = (object["preset"] as? String).flatMap(StreamPreset.init(rawValue:)) {
                    state.withLock { $0.preset = preset }
                }
            default:
                break
            }
        default:
            break
        }
    }

    func close() {
        continuation.finish()
        state.withLock { $0.producer?.cancel() }
    }

    // MARK: Playback

    private func play(host: Host, display: Int, units: [Data], aiOwns: @Sendable () -> Bool) async {
        let continuation = continuation
        func yield(_ kind: UInt8, _ payload: Data) {
            continuation.yield((kind: kind, payload: payload))
        }
        func json(_ object: [String: Any]) -> Data {
            (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
        }

        let displays = host.displays
        guard displays.isEmpty || displays.contains(where: { $0.index == display }) else {
            yield(ScreenFrameKind.paused, json(["reason": "display_unavailable"]))
            return
        }
        if host.isSessionLocked {
            yield(ScreenFrameKind.paused, json(["reason": "session_locked"]))
            return
        }
        guard !units.isEmpty else {
            yield(ScreenFrameKind.paused, json(["reason": "preview_video_missing"]))
            return
        }
        let target = displays.first { $0.index == display }
        let width = target?.width ?? 1920
        let height = target?.height ?? 1080
        let scale = target?.scale ?? 1
        var preset = state.withLock { $0.preset }
        func info() -> StreamInfo {
            StreamInfo(width: 1920, height: 1080, srcWidth: width, srcHeight: height, fps: 60,
                       bitrate: Double(Self.bitrate(preset)), encoder: "NVIDIA H.264 Encoder MFT", hardware: true, capture: "dd",
                       display: display)
        }
        yield(ScreenFrameKind.info, ScreenJSON.encode(info()))
        let arrow = Self.arrowCursor(scale: scale)
        yield(ScreenFrameKind.cursorShape, arrow.payload)

        let clock = ContinuousClock()
        var deadline = clock.now
        var index = 0
        var seq: UInt64 = 0
        var bytes = 0
        let start = ProcessInfo.processInfo.systemUptime
        while !Task.isCancelled {
            let restart = state.withLock { s -> Bool in
                defer { s.restart = false }
                return s.restart
            }
            if restart { index = 0 }
            let latestPreset = state.withLock { $0.preset }
            if latestPreset != preset {
                preset = latestPreset
                yield(ScreenFrameKind.info, ScreenJSON.encode(info()))
            }

            let now = ProcessInfo.processInfo.systemUptime
            let micros = UInt64(now * 1_000_000)
            let au = AUFrame(key: index == 0, hasConfig: index == 0, width: 1920, height: 1080, seq: seq,
                             captureMonoUS: micros, encodeDoneMonoUS: micros + 3_400, data: units[index])
            bytes += au.data.count
            yield(ScreenFrameKind.accessUnit, au.payload)

            let elapsed = now - start
            let (input, lastInput) = state.withLock { ($0.cursor, $0.lastInput) }
            let position: CursorPosition
            if aiOwns() || input == nil || now - lastInput > 4 {
                let angle = elapsed * 0.7
                position = CursorPosition(x: Int32(Double(width) * (0.5 + 0.28 * cos(angle))),
                                          y: Int32(Double(height) * (0.5 + 0.24 * sin(angle * 1.3))),
                                          visible: true, shapeID: arrow.id)
                state.withLock { $0.cursor = (position.x, position.y) }
            } else {
                position = CursorPosition(x: input!.x, y: input!.y, visible: true, shapeID: arrow.id)
            }
            yield(ScreenFrameKind.cursorPosition, position.payload)

            if seq % 60 == 59 {
                yield(ScreenFrameKind.stats, json(["fps": 60, "bitrate": bytes * 8, "dropped": 0, "encode_ms_p50": 3.6,
                                                   "encode_ms_p95": 7.1, "capture_ms_p50": 8.4]))
                yield(ScreenFrameKind.notice, json(["type": "latency", "capture_to_receive_ms": 11 + 2 * sin(elapsed)]))
                bytes = 0
            }
            index = (index + 1) % units.count
            seq += 1
            deadline += Self.frameInterval
            if deadline < clock.now { deadline = clock.now }
            try? await clock.sleep(until: deadline)
        }
    }

    private static func bitrate(_ preset: StreamPreset) -> Int {
        switch preset {
        case .sharp: 40_000_000
        case .balanced: 20_000_000
        case .saver: 8_000_000
        }
    }

    /// A Windows-style arrow (white with a black outline) at `scale` × 32 pixels, BGRA with straight alpha.
    static func arrowCursor(scale: Double) -> CursorShape {
        let size = max(Int((32 * scale).rounded()), 16)
        let unit = Double(size) / 32
        guard let space = CGColorSpace(name: CGColorSpace.sRGB),
              let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size * 4,
                                      space: space, bitmapInfo: CGBitmapInfo.byteOrder32Little.rawValue
                                          | CGImageAlphaInfo.premultipliedFirst.rawValue) else {
            return CursorShape(id: 1, width: 0, height: 0, hotX: 0, hotY: 0, bgra: Data())
        }
        context.translateBy(x: 0, y: CGFloat(size))
        context.scaleBy(x: 1, y: -1)
        let points: [(Double, Double)] = [(1, 1), (1, 22), (6, 17.5), (9.5, 25.5), (13, 24), (9.5, 16.5), (16, 16.5)]
        let path = CGMutablePath()
        path.addLines(between: points.map { CGPoint(x: $0.0 * unit, y: $0.1 * unit) })
        path.closeSubpath()
        context.addPath(path)
        context.setFillColor(CGColor(gray: 1, alpha: 1))
        context.setStrokeColor(CGColor(gray: 0, alpha: 1))
        context.setLineWidth(max(1, unit))
        context.setLineJoin(.round)
        context.drawPath(using: .fillStroke)
        guard let base = context.data else {
            return CursorShape(id: 1, width: 0, height: 0, hotX: 0, hotY: 0, bgra: Data())
        }
        var pixels = Data(bytes: base, count: size * size * 4)
        pixels.withUnsafeMutableBytes { raw in
            let bytes = raw.bindMemory(to: UInt8.self)
            for i in stride(from: 0, to: bytes.count, by: 4) {
                let alpha = Int(bytes[i + 3])
                guard alpha > 0, alpha < 255 else { continue }
                for c in 0..<3 { bytes[i + c] = UInt8(min(255, Int(bytes[i + c]) * 255 / alpha)) }
            }
        }
        return CursorShape(id: 1, width: size, height: size, hotX: Int(unit), hotY: Int(unit), bgra: pixels)
    }
}
