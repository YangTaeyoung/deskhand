/// Measured quality of a computer's screen stream, shown in the workspace toolbar.
public struct StreamQuality: Equatable, Sendable {
    /// Frames shown per second.
    public var fps: Double
    /// Capture-to-display latency in milliseconds.
    public var latencyMS: Int

    public init(fps: Double, latencyMS: Int) {
        self.fps = fps
        self.latencyMS = latencyMS
    }
}
