import XCTest
@testable import DeskhandUI

@MainActor
final class RDPLoginMemoryTests: XCTestCase {
    final class Vault: RDPCredentialVault {
        var values: [String: RDPSavedLogin] = [:]
        var writes = 0
        var failWrites = false
        func read(key: String) throws -> RDPSavedLogin? { values[key] }
        func write(_ login: RDPSavedLogin, key: String) throws {
            if failWrites { throw RDPCredentialError.unavailable }
            writes += 1; values[key] = login
        }
        func remove(key: String) throws { values[key] = nil }
    }

    func testFailedAttemptNeverReplacesPreviouslyWorkingPassword() async throws {
        let vault = Vault()
        let memory = RDPLoginMemory(vault: vault)
        let key = RDPLoginMemory.key(hostID: "one", certificate: "AAA")
        let old = RDPSavedLogin(username: "PC\\user", password: "old-test-only")
        vault.values[key] = old
        memory.prepare(key: key, username: "wrong", password: "wrong-test-only", remember: true)
        memory.failed()
        try await memory.connected()
        XCTAssertEqual(vault.values[key], old)
        XCTAssertEqual(vault.writes, 0)
    }

    func testOnlySuccessfulConnectionPersistsAndForgetRemovesIt() async throws {
        let vault = Vault()
        let memory = RDPLoginMemory(vault: vault)
        let key = RDPLoginMemory.key(hostID: "one", certificate: "AAA")
        memory.prepare(key: key, username: "PC\\user", password: "test-only", remember: true)
        let before = try await memory.restore(key: key)
        XCTAssertNil(before)
        let stored = try await memory.connected()
        XCTAssertTrue(stored)
        let restored = try await memory.restore(key: key)
        XCTAssertEqual(restored?.username, "PC\\user")
        XCTAssertEqual(vault.writes, 1)
        let storedAgain = try await memory.connected()
        XCTAssertFalse(storedAgain)
        XCTAssertEqual(vault.writes, 1)
        try await memory.forget(key: key)
        let removed = try await memory.restore(key: key)
        XCTAssertNil(removed)
    }

    func testHostAndCertificateIsolationAndOptOut() async throws {
        let vault = Vault()
        let memory = RDPLoginMemory(vault: vault)
        let key = RDPLoginMemory.key(hostID: "one", certificate: "AAA")
        XCTAssertEqual(key, RDPLoginMemory.key(hostID: "one", certificate: "aaa"))
        XCTAssertNotEqual(key, RDPLoginMemory.key(hostID: "two", certificate: "AAA"))
        XCTAssertNotEqual(key, RDPLoginMemory.key(hostID: "one", certificate: "BBB"))
        memory.prepare(key: key, username: "user", password: "test-only", remember: false)
        try await memory.connected()
        XCTAssertEqual(vault.writes, 0)
    }

    func testWriteFailureDoesNotKeepPendingPasswordForFutureSessions() async throws {
        let vault = Vault(); vault.failWrites = true
        let memory = RDPLoginMemory(vault: vault)
        memory.prepare(key: "key", username: "user", password: "test-only", remember: true)
        do { _ = try await memory.connected(); XCTFail("expected failed write") }
        catch RDPCredentialError.unavailable {}
        vault.failWrites = false
        _ = try await memory.connected()
        XCTAssertEqual(vault.writes, 0)
    }
}
