import Foundation
import Testing
@testable import DeskhandRDP

@MainActor
struct NativeRDPConnectionTests {
    @Test func startsIdleAndDisconnectIsIdempotent() {
        let connection = NativeRDPConnection()
        #expect(connection.phase == .idle)
        #expect(connection.frame == nil)
        #expect(connection.serverSessionID == nil)
        #expect(connection.receivedFrames == 0)
        connection.disconnect()
        connection.disconnect()
        #expect(connection.phase == .stopped)
    }

    @Test func missingLibraryFailsGenericallyAndCanBeRetried() throws {
        let connection = NativeRDPConnection(engineURL: URL(fileURLWithPath: "/nonexistent/DeskhandRDPEngine.dylib"))
        let configuration = try RDPConfiguration(host: "localhost", port: 3389, certificateSHA256: String(repeating: "a", count: 64))
        for _ in 0..<2 {
            do {
                try connection.connect(configuration: configuration, username: "user", domain: "", password: "secret-sentinel")
                Issue.record("Missing engine must fail")
            } catch {
                #expect(!String(describing: error).contains("secret-sentinel"))
                #expect(!error.localizedDescription.contains("secret-sentinel"))
            }
            if case .failed = connection.phase {} else { Issue.record("Expected failed phase") }
            #expect(connection.frame == nil)
            #expect(connection.serverSessionID == nil)
            #expect(connection.receivedFrames == 0)
            connection.disconnect()
        }
    }

    @Test func rejectsNonFileEngineURL() throws {
        let connection = NativeRDPConnection(engineURL: URL(string: "https://example.com/engine.dylib"))
        let configuration = try RDPConfiguration(host: "localhost", port: 3389, certificateSHA256: String(repeating: "a", count: 64))
        #expect(throws: (any Error).self) {
            try connection.connect(configuration: configuration, username: "user", domain: "", password: "secret-sentinel")
        }
    }

    @Test func rejectsLibraryWithoutRequiredSymbols() throws {
        let connection = NativeRDPConnection(engineURL: URL(fileURLWithPath: "/usr/lib/libSystem.B.dylib"))
        let configuration = try RDPConfiguration(host: "localhost", port: 3389, certificateSHA256: String(repeating: "a", count: 64))
        #expect(throws: (any Error).self) {
            try connection.connect(configuration: configuration, username: "user", domain: "", password: "test-only")
        }
        if case .failed = connection.phase {} else { Issue.record("Expected failed phase") }
    }

    @Test func rejectsCStringTruncationBeforeLoadingEngine() throws {
        let configuration = try RDPConfiguration(host: "localhost", port: 3389, certificateSHA256: String(repeating: "a", count: 64))
        for (username, domain, password) in [("", "", ""), ("user\u{0}suffix", "", "test-only"), ("user", "domain\u{0}suffix", "test-only"), ("user", "", "test\u{0}suffix")] {
            let connection = NativeRDPConnection(engineURL: URL(fileURLWithPath: "/nonexistent/engine.dylib"))
            do {
                try connection.connect(configuration: configuration, username: username, domain: domain, password: password)
                Issue.record("Invalid C string must fail")
            } catch RDPFailure.invalidCredentials {
                // The engine path is deliberately absent: validation precedes loading.
            } catch {
                Issue.record("Expected invalid credentials")
            }
        }
    }
}
