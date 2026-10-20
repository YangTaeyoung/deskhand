import Foundation

/// Locations of the daemon's files.
public enum Paths {
    /// `$DESKHAND_HOME`, or `~/Library/Application Support/Deskhand`.
    public static var home: URL { home(environment: Foundation.ProcessInfo.processInfo.environment) }

    /// `<home>/run/d.sock`.
    public static var socket: String { socket(home: home) }

    static func home(environment: [String: String]) -> URL {
        if let override = environment["DESKHAND_HOME"], !override.isEmpty {
            return URL(fileURLWithPath: override, isDirectory: true)
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Deskhand", isDirectory: true)
    }

    static func socket(home: URL) -> String {
        home.appendingPathComponent("run/d.sock").path
    }

    public static var daemonLog: String {
        home.appendingPathComponent("logs/daemon.log").path
    }
}

/// Client for the daemon local API: NDJSON JSON-RPC 2.0 over a unix socket.
///
/// Call `close()` when finished; the client keeps reconnecting until then.
public actor DeskhandClient {
    private enum State { case idle, connecting, ready, reconnecting, closed }

    private struct Subscription {
        let kinds: [String]?
        let hostID: String?
        let continuation: AsyncThrowingStream<DaemonEvent, Error>.Continuation

        func matches(_ event: DaemonEvent) -> Bool {
            if let kinds, !kinds.contains(event.kind) { return false }
            if let hostID, event.hostID != hostID { return false }
            return true
        }
    }

    private struct HelloInfo {
        let kind: String
        let name: String
        let version: String
    }

    public nonisolated let socketPath: String
    private let initialDelay: TimeInterval
    private let maxDelay: TimeInterval

    private var state: State = .idle
    private var connection: Connection?
    private var generation = 0
    private var nextID = 1
    private var pending: [Int: CheckedContinuation<JSONValue, Error>] = [:]
    private var hello: HelloInfo?
    private var subscriptions: [UUID: Subscription] = [:]
    private var reconnectTask: Task<Void, Never>?
    public private(set) var clientID: String?

    public init(
        socketPath: String = Paths.socket,
        reconnectInitialDelay: TimeInterval = 0.25,
        reconnectMaxDelay: TimeInterval = 5
    ) {
        self.socketPath = socketPath
        self.initialDelay = reconnectInitialDelay
        self.maxDelay = reconnectMaxDelay
    }

    // MARK: Public API

    /// Opens the socket and sends `hello`. Throws if the daemon cannot be reached; once connected, drops are retried automatically.
    public func connect(kind: String, name: String, version: String) async throws {
        guard state == .idle else {
            throw state == .closed ? ClientError.closed : ClientError.protocolViolation("already connected")
        }
        state = .connecting
        hello = HelloInfo(kind: kind, name: name, version: version)
        do {
            try await establish()
        } catch {
            discardConnection()
            if state != .closed { state = .idle }
            throw error
        }
        guard state == .connecting else { throw ClientError.closed }
        state = .ready
    }

    /// Sends a request and decodes its result. Parameters are encoded with snake_case keys.
    public nonisolated func call<P: Encodable, R: Decodable>(_ method: String, _ params: P) async throws -> R {
        let encoded = try Wire.encode(params)
        let result = try await send(method: method, params: encoded)
        let data = try JSONEncoder().encode(result)
        return try Wire.decode(R.self, from: data)
    }

    /// Event notifications matching the filters. The stream survives reconnects.
    public nonisolated func events(kinds: [String]? = nil, hostID: String? = nil) -> AsyncThrowingStream<DaemonEvent, Error> {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: DaemonEvent.self)
        let id = UUID()
        continuation.onTermination = { [weak self] _ in
            guard let self else { return }
            Task { await self.removeSubscription(id) }
        }
        Task { await self.addSubscription(id, Subscription(kinds: kinds, hostID: hostID, continuation: continuation)) }
        return stream
    }

    /// Closes the connection, fails in-flight calls and finishes event streams.
    public func close() {
        guard state != .closed else { return }
        state = .closed
        reconnectTask?.cancel()
        reconnectTask = nil
        discardConnection()
        for sub in subscriptions.values { sub.continuation.finish() }
        subscriptions = [:]
    }

    // MARK: Requests

    private func send(method: String, params: Data) async throws -> JSONValue {
        switch state {
        case .ready: break
        case .idle, .connecting: throw ClientError.notConnected
        case .reconnecting: throw ClientError.disconnected
        case .closed: throw ClientError.closed
        }
        return try await rawCall(method, params: params)
    }

    private func rawCall(_ method: String, params: Data) async throws -> JSONValue {
        guard let connection else { throw ClientError.disconnected }
        let id = nextID
        nextID += 1
        let line = try Wire.request(id: id, method: method, params: params)
        return try await withCheckedThrowingContinuation { continuation in
            pending[id] = continuation
            do {
                try connection.write(line)
            } catch {
                pending[id] = nil
                continuation.resume(throwing: ClientError.disconnected)
            }
        }
    }

    private func rawCall(_ method: String, _ params: some Encodable) async throws -> JSONValue {
        try await rawCall(method, params: Wire.encode(params))
    }

    // MARK: Connection lifecycle

    /// Opens the socket, starts reading, sends `hello` and restores subscriptions.
    private func establish() async throws {
        guard let hello else { throw ClientError.notConnected }
        let connection = try Connection.connect(path: socketPath)
        generation += 1
        let gen = generation
        self.connection = connection
        startReader(connection, generation: gen)

        let reply = try await rawCall("hello", ["client": hello.kind, "name": hello.name, "version": hello.version])
        clientID = reply["client_id"]?.stringValue
        var snapshot: Set<UUID>
        repeat {
            snapshot = Set(subscriptions.keys)
            try await resubscribe()
        } while Set(subscriptions.keys) != snapshot
    }

    var subscriptionCount: Int { subscriptions.count }

    private func startReader(_ connection: Connection, generation gen: Int) {
        Task {
            var lines = LineBuffer()
            do {
                for try await chunk in connection.chunks {
                    for line in lines.append(chunk) { handle(line: line) }
                }
            } catch {}
            connectionEnded(generation: gen)
        }
    }

    private func handle(line: Data) {
        guard let value = Wire.parse(line) else { return }
        if let response = Wire.response(value) {
            guard let continuation = pending.removeValue(forKey: response.id) else { return }
            continuation.resume(with: response.outcome)
        } else if value["method"]?.stringValue == "event", let params = value["params"],
                  let event = DaemonEvent(params: params) {
            deliver(event)
        }
    }

    private func deliver(_ event: DaemonEvent) {
        for (id, sub) in subscriptions where sub.matches(event) {
            if case .terminated = sub.continuation.yield(event) { subscriptions[id] = nil }
        }
    }

    private func connectionEnded(generation gen: Int) {
        guard gen == generation, state != .closed else { return }
        discardConnection()
        if state == .ready {
            state = .reconnecting
            reconnectTask = Task { await reconnectLoop() }
        }
    }

    private func discardConnection() {
        connection?.close()
        connection = nil
        let waiting = pending
        pending = [:]
        for continuation in waiting.values { continuation.resume(throwing: ClientError.disconnected) }
    }

    private func reconnectLoop() async {
        var delay = initialDelay
        while state == .reconnecting && !Task.isCancelled {
            try? await Task.sleep(for: .seconds(delay))
            guard state == .reconnecting && !Task.isCancelled else { return }
            do {
                try await establish()
                guard state == .reconnecting else { return }
                state = .ready
                reconnectTask = nil
                return
            } catch {
                discardConnection()
                delay = min(delay * 2, maxDelay)
            }
        }
    }

    // MARK: Subscriptions

    private func addSubscription(_ id: UUID, _ sub: Subscription) async {
        guard state != .closed else {
            sub.continuation.finish()
            return
        }
        subscriptions[id] = sub
        if state == .ready {
            do {
                try await resubscribe()
            } catch let error as RPCErrorInfo {
                subscriptions[id] = nil
                sub.continuation.finish(throwing: error)
            } catch {}
        }
    }

    private func removeSubscription(_ id: UUID) {
        subscriptions[id] = nil
    }

    /// Issues `events.subscribe` with the union of all active stream filters; streams filter client-side.
    private func resubscribe() async throws {
        guard !subscriptions.isEmpty else { return }
        let subs = Array(subscriptions.values)
        var params: [String: JSONValue] = [:]
        if !subs.contains(where: { $0.kinds == nil }) {
            var kinds: [String] = []
            for sub in subs { for k in sub.kinds ?? [] where !kinds.contains(k) { kinds.append(k) } }
            params["kinds"] = .array(kinds.map { .string($0) })
        }
        let hosts = Set(subs.map(\.hostID))
        if hosts.count == 1, let host = hosts.first!, !host.isEmpty { params["host_id"] = .string(host) }
        _ = try await rawCall("events.subscribe", params)
    }
}
