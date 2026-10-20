import Foundation

/// Runs an action at once and then again `interval` after each run ends, while active.
@MainActor
final class RefreshLoop {
    typealias Wait = @Sendable (Duration) async throws -> Void

    let interval: Duration
    private let wait: Wait
    private let action: @MainActor () async -> Void
    private var task: Task<Void, Never>?
    private(set) var isActive = false

    init(interval: Duration, wait: @escaping Wait = { try await Task.sleep(for: $0) }, action: @escaping @MainActor () async -> Void) {
        self.interval = interval
        self.wait = wait
        self.action = action
    }

    isolated deinit {
        task?.cancel()
    }

    /// Starts the loop with an immediate run, or stops it before its next run.
    func setActive(_ active: Bool) {
        guard active != isActive else { return }
        isActive = active
        task?.cancel()
        task = nil
        guard active else { return }
        let wait = self.wait
        let interval = self.interval
        let action = self.action
        task = Task {
            while !Task.isCancelled {
                await action()
                guard !Task.isCancelled else { return }
                do { try await wait(interval) } catch { return }
            }
        }
    }
}
