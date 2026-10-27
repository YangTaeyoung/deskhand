import Foundation
import Synchronization
import XCTest
@testable import DeskhandUI

@MainActor
final class BoundedCredentialExecutorTests: XCTestCase {
    func testKeychainWorkRunsOffTheMainThread() async throws {
        let executor = BoundedCredentialExecutor()
        let wasMain = try await executor.perform { Thread.isMainThread }
        XCTAssertFalse(wasMain)
    }

    func testTimeoutReturnsWithoutQueuingAnotherBlockedOperation() async throws {
        let executor = BoundedCredentialExecutor()
        let release = DispatchSemaphore(value: 0)
        let returned = Mutex(false)
        let started = Mutex(0)
        do {
            _ = try await executor.perform(timeout: 0.03) {
                started.withLock { $0 += 1 }
                release.wait()
                returned.withLock { $0 = true }
                return "late-test-value"
            }
            XCTFail("expected timeout")
        } catch RDPCredentialError.unavailable {}
        do {
            _ = try await executor.perform { started.withLock { $0 += 1 }; return "must-not-run" }
            XCTFail("expected inflight refusal")
        } catch RDPCredentialError.unavailable {}
        XCTAssertEqual(started.withLock { $0 }, 1)
        release.signal()
        let end = ContinuousClock.now + .seconds(2)
        while !returned.withLock({ $0 }) {
            guard ContinuousClock.now < end else { return XCTFail("blocked operation did not finish") }
            try await Task.sleep(for: .milliseconds(1))
        }
        // The native operation finished, so a new request can be admitted.
        var accepted = false
        while !accepted, ContinuousClock.now < end {
            do {
                let value = try await executor.perform { "fresh-test-value" }
                XCTAssertEqual(value, "fresh-test-value")
                accepted = true
            } catch RDPCredentialError.unavailable {
                try await Task.sleep(for: .milliseconds(1))
            }
        }
        XCTAssertTrue(accepted)
    }
}
