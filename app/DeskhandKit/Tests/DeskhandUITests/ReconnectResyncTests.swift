@testable import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandUI

/// The debugger tab, the store and the transfer queue read their state again when the real
/// client's socket drops and comes back, against `MockDaemon`.
@MainActor
final class ReconnectResyncTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private static let host = #"{"host_id":"windows-dev","name":"개발 PC","os":"windows","arch":"amd64","address":"a","port":1,"ssh_alias":"","state":"connected","caps":{},"displays":[],"owner":"none","connection_generation":1}"#
    private static let session = #"{"debug_id":"d-1","host_id":"windows-dev","kind":"launch","name":"Shop Admin","ports":{"inspect":52011},"state":"running","created_at":"2026-10-20T10:00:00.000Z","rev":1,"targets":[{"id":"t-page","role":"renderer","type":"page","url":"app://shop/index.html","title":"결제","attached":true,"paused":false,"gen":0}]}"#

    private func connected(_ daemon: MockDaemon) async throws -> (LiveBackend, DeskhandClient) {
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path, reconnectInitialDelay: 0.02, reconnectMaxDelay: 0.1)
        try await client.connect(kind: "app", name: "test", version: "0")
        return (LiveBackend(client: client), client)
    }

    private func handle(_ daemon: MockDaemon) {
        let host = Self.host
        let session = Self.session
        daemon.setHandler { request in
            switch request.method {
            case "host.list": .result(#"{"hosts":[\#(host)]}"#)
            case "debug.list": .result(#"{"sessions":[\#(session)]}"#)
            case "debug.console":
                if request.params["cursor"] != nil {
                    .result(#"{"entries":[{"seq":3,"time":"2026-10-20T10:01:00.000Z","target_id":"t-page","source":"console","level":"log","text":"다시 연결 후"}],"next_cursor":3,"first_seq":1,"has_more":false,"has_earlier":false,"gap":false,"dropped":0}"#)
                } else {
                    .result(#"{"entries":[{"seq":1,"time":"2026-10-20T10:00:00.000Z","target_id":"t-page","source":"console","level":"log","text":"처음"},{"seq":2,"time":"2026-10-20T10:00:01.000Z","target_id":"t-page","source":"console","level":"log","text":"둘째"}],"next_cursor":2,"first_seq":1,"has_more":false,"has_earlier":false,"gap":false,"dropped":0}"#)
                }
            case "transfer.list": .result(#"{"transfers":[]}"#)
            case "timeline.list": .result(#"{"entries":[]}"#)
            default: nil
            }
        }
    }

    /// Waits until the client has its reconnect watcher, then drops the socket.
    private func drop(_ daemon: MockDaemon, client: DeskhandClient) async throws {
        try await waitUntilAsync { await client.reconnectWatcherCount > 0 }
        daemon.dropConnections()
    }

    func testTheDebuggerReloadsSessionsAndCatchesUpTheConsoleAfterTheSocketComesBack() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        handle(daemon)
        let (backend, client) = try await connected(daemon)
        let model = DebuggerModel(hostID: dev, backend: backend, defaults: UserDefaults(suiteName: "resync-\(UUID())")!)
        model.start()
        try await waitUntil { model.selected?.console.entries.count == 2 }
        XCTAssertEqual(daemon.requests(method: "debug.list").count, 1)
        try await drop(daemon, client: client)
        try await waitUntil(timeout: .seconds(5)) { daemon.requests(method: "debug.list").count == 2 }
        try await waitUntil(timeout: .seconds(5)) { model.selected?.console.entries.map(\.seq) == [1, 2, 3] }
        XCTAssertEqual(daemon.requests(method: "debug.console").last?.params["cursor"] as? Int, 2)
        model.stop()
        await client.close()
    }

    func testTheStoreReloadsTheComputersAfterTheSocketComesBack() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        handle(daemon)
        let (backend, client) = try await connected(daemon)
        let store = AppStore(backend: backend, defaults: UserDefaults(suiteName: "resync-store-\(UUID())")!)
        await store.start()
        try await waitUntil { store.hosts.map(\.hostID) == [self.dev] }
        let lists = daemon.requests(method: "host.list").count
        try await drop(daemon, client: client)
        try await waitUntil(timeout: .seconds(5)) { daemon.requests(method: "host.list").count == lists + 1 }
        await client.close()
    }

    func testTheTransferQueueReloadsAfterTheSocketComesBack() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        handle(daemon)
        let (backend, client) = try await connected(daemon)
        let queue = TransferQueue(hostID: dev, backend: backend)
        queue.start()
        try await waitUntil { queue.loaded }
        XCTAssertEqual(daemon.requests(method: "transfer.list").count, 1)
        try await drop(daemon, client: client)
        try await waitUntil(timeout: .seconds(5)) { daemon.requests(method: "transfer.list").count == 2 }
        queue.stop()
        await client.close()
    }
}

/// Waits until an async `condition` holds, checking every few milliseconds.
func waitUntilAsync(timeout: Duration = .seconds(3), _ condition: @Sendable () async -> Bool,
                    file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = ContinuousClock.now + timeout
    while !(await condition()) {
        guard ContinuousClock.now < deadline else {
            XCTFail("condition not met", file: file, line: line)
            throw CancellationError()
        }
        try await Task.sleep(for: .milliseconds(5))
    }
}
