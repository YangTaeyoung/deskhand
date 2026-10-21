/// Measured quality of a computer's screen stream, shown in the workspace toolbar.
public struct StreamQuality: Equatable, Hashable, Sendable {
    /// Frames received per second.
    public var fps: Double
    /// Capture-to-display latency in milliseconds, when the daemon reports its measurement.
    public var latencyMS: Int?
    /// Received bits per second.
    public var bitsPerSecond: Double?

    public init(fps: Double, latencyMS: Int?, bitsPerSecond: Double? = nil) {
        self.fps = fps
        self.latencyMS = latencyMS
        self.bitsPerSecond = bitsPerSecond
    }

    /// Megabits per second with one decimal (`12.4`).
    public var megabitsText: String? {
        bitsPerSecond.map { DS.Format.decimal($0 / 1_000_000) }
    }
}
