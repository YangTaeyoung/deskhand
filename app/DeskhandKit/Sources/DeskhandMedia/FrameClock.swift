import Foundation

/// Receive-side measurements of a screen stream over a sliding window: frames and bits per
/// second, the time between receiving an access unit and handing it to the display layer, and
/// the daemon's latest capture-to-receive latency.
public struct FrameClock: Sendable {
    public struct Snapshot: Sendable, Equatable {
        /// Access units received in the last window, per second.
        public var fps: Double
        /// Access unit bytes received in the last window, in bits per second.
        public var bitsPerSecond: Double
        /// Median receive-to-enqueue time in the last window; nil without frames.
        public var appMS: Double?
        /// The daemon's latest capture-to-receive latency; nil when none arrived recently.
        public var captureToReceiveMS: Double?

        public init(fps: Double, bitsPerSecond: Double, appMS: Double?, captureToReceiveMS: Double?) {
            self.fps = fps
            self.bitsPerSecond = bitsPerSecond
            self.appMS = appMS
            self.captureToReceiveMS = captureToReceiveMS
        }

        /// Capture-to-display latency: the daemon's capture-to-receive time plus the app's
        /// receive-to-enqueue time; nil without a daemon measurement.
        public var latencyMS: Double? {
            captureToReceiveMS.map { $0 + (appMS ?? 0) }
        }
    }

    private struct Sample {
        let time: TimeInterval
        let bytes: Int
        let appMS: Double
    }

    public let window: TimeInterval
    public let latencyLifetime: TimeInterval
    private var samples: [Sample] = []
    private var latency: (ms: Double, time: TimeInterval)?

    public init(window: TimeInterval = 1, latencyLifetime: TimeInterval = 3) {
        self.window = window
        self.latencyLifetime = latencyLifetime
    }

    /// Records an access unit of `bytes` received at `receivedAt` and enqueued at `enqueuedAt`
    /// (seconds on one monotonic clock).
    public mutating func frame(bytes: Int, receivedAt: TimeInterval, enqueuedAt: TimeInterval) {
        samples.append(Sample(time: receivedAt, bytes: bytes, appMS: max(0, enqueuedAt - receivedAt) * 1000))
        prune(before: receivedAt - window)
    }

    /// Records a daemon latency notice received at `time`.
    public mutating func latency(_ ms: Double, at time: TimeInterval) {
        latency = (ms, time)
    }

    public mutating func snapshot(at now: TimeInterval) -> Snapshot {
        prune(before: now - window)
        let bytes = samples.reduce(0) { $0 + $1.bytes }
        let processing = samples.map(\.appMS).sorted()
        let median = processing.isEmpty ? nil : processing[processing.count / 2]
        let daemon = latency.flatMap { now - $0.time <= latencyLifetime ? $0.ms : nil }
        return Snapshot(fps: Double(samples.count) / window, bitsPerSecond: Double(bytes * 8) / window,
                        appMS: median, captureToReceiveMS: daemon)
    }

    public mutating func reset() {
        samples.removeAll()
        latency = nil
    }

    private mutating func prune(before cutoff: TimeInterval) {
        if let index = samples.firstIndex(where: { $0.time > cutoff }) {
            samples.removeFirst(index)
        } else {
            samples.removeAll()
        }
    }
}
