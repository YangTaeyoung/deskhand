import Darwin
import Foundation
import Testing
@testable import DeskhandRDP

private let builtEngineURL: URL = {
    var root = URL(fileURLWithPath: #filePath)
    for _ in 0..<5 { root.deleteLastPathComponent() }
    return root.appendingPathComponent("build/rdp/engine/libDeskhandRDPEngine.dylib")
}()

// These tests use the actual built engine, never a replacement dylib. Unit tests
// above remain runnable when the separate native build has not been produced.
@Suite(.serialized, .enabled(if: FileManager.default.fileExists(atPath: builtEngineURL.path)))
@MainActor
struct NativeRDPEngineTests {
    @Test func cancelWhileConnectingAndReconnect() async throws {
        let listener = try SilentLoopbackListener()
        defer { listener.close() }
        let configuration = try RDPConfiguration(host: "127.0.0.1", port: listener.port, certificateSHA256: String(repeating: "a", count: 64), width: 800, height: 600)
        let connection = NativeRDPConnection(engineURL: builtEngineURL)
        for _ in 0..<8 {
            try connection.connect(configuration: configuration, username: "test-only", domain: "", password: "")
            #expect(connection.phase == .connecting)
            #expect(connection.serverSessionID == nil)
            #expect(connection.frame == nil)
            var accepted = false
            for _ in 0..<200 {
                if listener.acceptPendingConnection() { accepted = true; break }
                try await Task.sleep(for: .milliseconds(5))
            }
            #expect(accepted, "The real engine must reach TCP negotiation before cancellation")
            #expect(connection.phase == .connecting)
            connection.disconnect()
            connection.disconnect()
            #expect(connection.phase == .stopped)
        }
        // Allow cancelled polling deliveries to reach the actor; none may revive
        // the final disconnected session or publish an old session identifier.
        try await Task.sleep(for: .milliseconds(150))
        #expect(connection.phase == .stopped)
        #expect(connection.frame == nil)
        #expect(connection.serverSessionID == nil)
        #expect(connection.receivedFrames == 0)
    }

    @Test func releasingConnectionStopsWorkerWithoutRetainCycle() async throws {
        let listener = try SilentLoopbackListener()
        defer { listener.close() }
        let configuration = try RDPConfiguration(host: "127.0.0.1", port: listener.port, certificateSHA256: String(repeating: "a", count: 64))
        var connection: NativeRDPConnection? = NativeRDPConnection(engineURL: builtEngineURL)
        weak var released = connection
        try connection?.connect(configuration: configuration, username: "test-only", domain: "", password: "")
        connection = nil
        for _ in 0..<50 where released != nil {
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(released == nil)
        released = nil
    }

    @Test func stopBeforeRunAndConcurrentPollsFinishSafely() async throws {
        let listener = try SilentLoopbackListener()
        defer { listener.close() }
        let configuration = try RDPConfiguration(host: "127.0.0.1", port: listener.port, certificateSHA256: String(repeating: "a", count: 64))
        let engine = try RDPEngine(url: builtEngineURL)
        for _ in 0..<16 {
            let worker = try RDPWorker(engine: engine, configuration: configuration, username: "test-only", domain: "", password: "")
            worker.stop()
            worker.start()
            await withTaskGroup(of: Void.self) { group in
                for _ in 0..<4 {
                    group.addTask {
                        for _ in 0..<25 {
                            let snapshot = worker.poll(after: nil)
                            #expect(snapshot.phase == .stopped)
                            #expect(snapshot.frame == nil)
                            worker.stop()
                        }
                    }
                }
            }
            for _ in 0..<200 {
                if worker.poll(after: nil).finished { break }
                try await Task.sleep(for: .milliseconds(5))
            }
            #expect(worker.poll(after: nil).finished, "Run must return and free before completion")
        }
    }

    @Test func nativeFailureReachesObservableState() async throws {
        let listener = try SilentLoopbackListener()
        let configuration = try RDPConfiguration(host: "127.0.0.1", port: listener.port, certificateSHA256: String(repeating: "a", count: 64))
        listener.close()
        let connection = NativeRDPConnection(engineURL: builtEngineURL)
        defer { connection.disconnect() }
        try connection.connect(configuration: configuration, username: "test-only", domain: "", password: "")
        for _ in 0..<200 {
            if case .failed = connection.phase { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        if case .failed(let code) = connection.phase {
            #expect(code != 0)
        } else { Issue.record("Expected native connection failure") }
        #expect(connection.serverSessionID == nil)
        #expect(connection.frame == nil)
    }
}

// A real TCP endpoint that never completes RDP negotiation. This exercises abort
// during network connection without Windows credentials or a live RDP server.
private final class SilentLoopbackListener {
    let port: UInt16
    private var descriptor: Int32
    private var peers: [Int32] = []

    init() throws {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw ListenerFailure.socket }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        var length = socklen_t(MemoryLayout<sockaddr_in>.size)
        let nameResult = withUnsafeMutablePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getsockname(descriptor, $0, &length)
            }
        }
        guard result == 0, nameResult == 0, listen(descriptor, 64) == 0,
              fcntl(descriptor, F_SETFL, O_NONBLOCK) == 0 else {
            Darwin.close(descriptor)
            throw ListenerFailure.socket
        }
        self.descriptor = descriptor
        port = UInt16(bigEndian: address.sin_port)
    }

    func acceptPendingConnection() -> Bool {
        let peer = Darwin.accept(descriptor, nil, nil)
        guard peer >= 0 else { return false }
        peers.append(peer)
        return true
    }

    func close() {
        for peer in peers { Darwin.close(peer) }
        peers.removeAll()
        if descriptor >= 0 { Darwin.close(descriptor); descriptor = -1 }
    }

    deinit { close() }
    private enum ListenerFailure: Error { case socket }
}
