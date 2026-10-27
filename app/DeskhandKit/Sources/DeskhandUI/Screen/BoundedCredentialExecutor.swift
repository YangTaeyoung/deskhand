import Foundation

/// A stalled OS keychain call may outlive the UI timeout. Keep its admission
/// until it actually returns, so retries cannot accumulate blocked work.
final class BoundedCredentialExecutor: @unchecked Sendable {
    private let queue = DispatchQueue(label: "Deskhand.credentials")
    private let lock = NSLock()
    private var busy = false

    private func acquire() -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard !busy else { return false }
        busy = true
        return true
    }

    private func release() {
        lock.lock(); busy = false; lock.unlock()
    }

    func perform<Value: Sendable>(timeout: TimeInterval = 2,
                                 _ operation: @escaping @Sendable () throws -> Value) async throws -> Value {
        try Task.checkCancellation()
        guard acquire() else { throw RDPCredentialError.unavailable }
        let value: Value = try await withCheckedThrowingContinuation { continuation in
            let reply = CredentialReply(continuation)
            queue.async {
                let result = Result { try operation() }
                self.release()
                reply.finish(result)
            }
            DispatchQueue.global().asyncAfter(deadline: .now() + timeout) {
                reply.finish(.failure(RDPCredentialError.unavailable))
            }
        }
        try Task.checkCancellation()
        return value
    }
}

private final class CredentialReply<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Value, any Error>?

    init(_ continuation: CheckedContinuation<Value, any Error>) { self.continuation = continuation }

    func finish(_ result: Result<Value, any Error>) {
        lock.lock()
        let current = continuation
        continuation = nil
        lock.unlock()
        current?.resume(with: result)
    }
}
