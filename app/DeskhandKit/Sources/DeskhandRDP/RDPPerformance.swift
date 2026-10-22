import Foundation

/// Counts pipeline stages, not physical display presentations. No pixels or credentials.
struct RDPPerformanceSample: Codable, Equatable {
    let uptime: Double
    let seconds: Double
    let width: Int
    let height: Int
    let nativeUpdates: UInt64
    let receivedFrames: Int
    let submittedFrames: Int
    let receivedFPS: Double
    let submittedFPS: Double
    let submitP50MS: Double
    let submitP95MS: Double
    let submitMaxMS: Double
    let submitGapsOver100MS: Int
    let sinceLastSubmitMS: Double
}

struct RDPPerformanceWindow {
    private var start: Double
    private var width = 0, height = 0
    private var nativeUpdates: UInt64 = 0
    private var received = 0, submitted = 0
    private var lastReceived: UInt64?, lastSubmitted: UInt64?
    private var lastSubmitTime: Double?
    private var intervals: [Double] = []
    private var maximum = 0.0, gaps = 0

    init(start: Double) { self.start = start }

    mutating func receive(sequence: UInt64, width: Int, height: Int, at time: Double) {
        if self.width != 0 && (self.width != width || self.height != height) {
            self = RDPPerformanceWindow(start: time)
        }
        self.width = width; self.height = height
        if let lastReceived, sequence == lastReceived { return }
        nativeUpdates &+= lastReceived.map { sequence > $0 ? sequence - $0 : 1 } ?? 1
        received += 1; lastReceived = sequence
    }

    mutating func submit(sequence: UInt64, at time: Double) {
        guard let lastReceived, sequence == lastReceived,
              lastSubmitted.map({ sequence > $0 }) ?? true else { return }
        if let previous = lastSubmitTime {
            let ms = max(0, (time - previous) * 1000)
            if intervals.count < 256 { intervals.append(ms) }
            maximum = max(maximum, ms)
            if ms > 100 { gaps += 1 }
        }
        submitted += 1; lastSubmitted = sequence; lastSubmitTime = time
    }

    mutating func sample(at time: Double) -> RDPPerformanceSample? {
        let seconds = time - start
        guard width > 0, seconds >= 1 else { return nil }
        let sorted = intervals.sorted()
        func percentile(_ fraction: Double) -> Double {
            guard !sorted.isEmpty else { return 0 }
            return sorted[Int((Double(sorted.count - 1) * fraction).rounded())]
        }
        let result = RDPPerformanceSample(uptime: time, seconds: seconds, width: width, height: height,
            nativeUpdates: nativeUpdates, receivedFrames: received, submittedFrames: submitted,
            receivedFPS: Double(received) / seconds, submittedFPS: Double(submitted) / seconds,
            submitP50MS: percentile(0.5), submitP95MS: percentile(0.95), submitMaxMS: maximum,
            submitGapsOver100MS: gaps, sinceLastSubmitMS: lastSubmitTime.map { max(0, (time - $0) * 1000) } ?? 0)
        start = time; nativeUpdates = 0; received = 0; submitted = 0
        intervals.removeAll(keepingCapacity: true); maximum = 0; gaps = 0
        return result
    }
}
