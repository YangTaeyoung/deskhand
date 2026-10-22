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

    func testFailedAttemptNeverReplacesPreviouslyWorkingPassword() throws {
        let vault = Vault()
        let memory = RDPLoginMemory(vault: vault)
        let key = RDPLoginMemory.key(hostID: "one", certificate: "AAA")
        let old = RDPSavedLogin(username: "PC\\user", password: "old-test-only")
        vault.values[key] = old
        memory.prepare(key: key, username: "wrong", password: "wrong-test-only", remember: true)
        memory.failed()
        try memory.connected()
        XCTAssertEqual(vault.values[key], old)
        XCTAssertEqual(vault.writes, 0)
    }

    func testOnlySuccessfulConnectionPersistsAndForgetRemovesIt() throws {
        let vault = Vault()
        let memory = RDPLoginMemory(vault: vault)
        let key = RDPLoginMemory.key(hostID: "one", certificate: "AAA")
        memory.prepare(key: key, username: "PC\\user", password: "test-only", remember: true)
        XCTAssertNil(try memory.restore(key: key))
        XCTAssertTrue(try memory.connected())
        XCTAssertEqual(try memory.restore(key: key)?.username, "PC\\user")
        XCTAssertEqual(vault.writes, 1)
        XCTAssertFalse(try memory.connected())
        XCTAssertEqual(vault.writes, 1)
        try memory.forget(key: key)
        XCTAssertNil(try memory.restore(key: key))
    }

    func testHostAndCertificateIsolationAndOptOut() throws {
        let vault = Vault()
        let memory = RDPLoginMemory(vault: vault)
        let key = RDPLoginMemory.key(hostID: "one", certificate: "AAA")
        XCTAssertEqual(key, RDPLoginMemory.key(hostID: "one", certificate: "aaa"))
        XCTAssertNotEqual(key, RDPLoginMemory.key(hostID: "two", certificate: "AAA"))
        XCTAssertNotEqual(key, RDPLoginMemory.key(hostID: "one", certificate: "BBB"))
        memory.prepare(key: key, username: "user", password: "test-only", remember: false)
        try memory.connected()
        XCTAssertEqual(vault.writes, 0)
    }

    func testWriteFailureDoesNotKeepPendingPasswordForFutureSessions() {
        let vault = Vault(); vault.failWrites = true
        let memory = RDPLoginMemory(vault: vault)
        memory.prepare(key: "key", username: "user", password: "test-only", remember: true)
        XCTAssertThrowsError(try memory.connected())
        vault.failWrites = false
        XCTAssertNoThrow(try memory.connected())
        XCTAssertEqual(vault.writes, 0)
    }
}
