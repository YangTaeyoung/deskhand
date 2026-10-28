import Foundation

/// Monotonic idle timing; held buttons or keys prevent automatic release.
struct AutoHandoffPolicy {
    private var lastActivity: TimeInterval?
    private var holding = false
    mutating func activity(at time: TimeInterval, holding: Bool) {
        lastActivity = time
        self.holding = holding
    }
    mutating func reset() { lastActivity = nil; holding = false }
    func remaining(at time: TimeInterval) -> TimeInterval? {
        guard let lastActivity, !holding else { return nil }
        return max(0, 5 - (time - lastActivity))
    }
    func shouldRelease(at time: TimeInterval) -> Bool {
        guard let lastActivity, !holding else { return false }
        return time - lastActivity >= 5
    }
}
