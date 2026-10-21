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

    /// `<home>/logs`.
    public static var logs: String {
        home.appendingPathComponent("logs", isDirectory: true).path
    }

    /// `<home>/artifacts`, where screenshots, QA evidence and saved diagnostics are kept.
    public static var artifacts: String {
        home.appendingPathComponent("artifacts", isDirectory: true).path
    }
}

/// Client for the daemon local API: NDJSON JSON-RPC 2.0 over a unix socket.
///
/// Every call fails with `ClientError.timeout` when the daemon does not answer within its limit,
/// and with `CancellationError` when the calling task is cancelled; either way the client forgets
/// the call and ignores a late answer. Call `close()` when finished; the client keeps reconnecting
/// until then.
public actor DeskhandClient {
    private enum State { case idle, connecting, ready, reconnecting, closed }

    private struct Subscription {
        let kinds: [String]?
        let hostID: String?
        let continuation: AsyncThrowingStream<DaemonEvent, Error>.Continuation

        /// The kind filter applies to every event; the host filter to every event but
        /// `events.dropped`, which concerns the whole connection and carries no `host_id`.
        func matches(_ event: DaemonEvent) -> Bool {
            if let kinds, !kinds.contains(event.kind) { return false }
            if let hostID, event.hostID != hostID, event.kind != "events.dropped" { return false }
            return true
        }
    }

    private struct HelloInfo {
        let kind: String
        let name: String
        let version: String
    }

    /// The limit of a call that names none: 30 seconds.
    public static let defaultCallTimeout: Duration = .seconds(30)

    public nonisolated let socketPath: String
    private let initialDelay: TimeInterval
    private let maxDelay: TimeInterval
    private let callTimeout: Duration

    private var state: State = .idle
    private var connection: Connection?
    private var generation = 0
    private var nextID = 1
    private var pending: [Int: CheckedContinuation<JSONValue, Error>] = [:]
    private var timers: [Int: Task<Void, Never>] = [:]
    private var hello: HelloInfo?
    private var subscriptions: [UUID: Subscription] = [:]
    private var reconnectWatchers: [UUID: AsyncStream<Void>.Continuation] = [:]
    private var reconnectTask: Task<Void, Never>?
    public private(set) var clientID: String?
    /// `daemon_version` of the last `hello` reply.
    public private(set) var daemonVersion: String?
    /// `api_version` of the last `hello` reply.
    public private(set) var apiVersion: Int?

    public init(
        socketPath: String = Paths.socket,
        reconnectInitialDelay: TimeInterval = 0.25,
        reconnectMaxDelay: TimeInterval = 5,
        callTimeout: Duration = DeskhandClient.defaultCallTimeout
    ) {
        self.socketPath = socketPath
        self.initialDelay = reconnectInitialDelay
        self.maxDelay = reconnectMaxDelay
        self.callTimeout = callTimeout
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
    ///
    /// The call fails with `ClientError.timeout` after `timeout` (the client's call limit when nil)
    /// plus the `timeout_ms` of the parameters when they carry one.
    public nonisolated func call<P: Encodable, R: Decodable>(_ method: String, _ params: P, timeout: Duration? = nil) async throws -> R {
        let encoded = try Wire.encode(params)
        let result = try await send(method: method, params: encoded, timeout: timeout)
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

    /// Yields each time the connection comes back after a drop, once `hello` and the event
    /// subscription are restored. Events sent while it was down were not delivered.
    public nonisolated func reconnects() -> AsyncStream<Void> {
        let (stream, continuation) = AsyncStream.makeStream(of: Void.self)
        let id = UUID()
        continuation.onTermination = { [weak self] _ in
            guard let self else { return }
            Task { await self.removeReconnectWatcher(id) }
        }
        Task { await self.addReconnectWatcher(id, continuation) }
        return stream
    }

    private func addReconnectWatcher(_ id: UUID, _ continuation: AsyncStream<Void>.Continuation) {
        guard state != .closed else { return continuation.finish() }
        reconnectWatchers[id] = continuation
    }

    private func removeReconnectWatcher(_ id: UUID) {
        reconnectWatchers[id] = nil
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
        for watcher in reconnectWatchers.values { watcher.finish() }
        reconnectWatchers = [:]
    }

    // MARK: Requests

    private func send(method: String, params: Data, timeout: Duration?) async throws -> JSONValue {
        try Task.checkCancellation()
        switch state {
        case .ready: break
        case .idle, .connecting: throw ClientError.notConnected
        case .reconnecting: throw ClientError.disconnected
        case .closed: throw ClientError.closed
        }
        return try await rawCall(method, params: params, limit: (timeout ?? callTimeout) + Self.ownTimeout(of: params))
    }

    /// The `timeout_ms` member of encoded parameters, or zero.
    static func ownTimeout(of params: Data) -> Duration {
        guard let milliseconds = Wire.parse(params)?["timeout_ms"]?.intValue, milliseconds > 0 else { return .zero }
        return .milliseconds(milliseconds)
    }

    /// Writes the request and waits for its answer, at most `limit`.
    private func rawCall(_ method: String, params: Data, limit: Duration) async throws -> JSONValue {
        guard let connection else { throw ClientError.disconnected }
        let id = nextID
        nextID += 1
        let line = try Wire.request(id: id, method: method, params: params)
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                pending[id] = continuation
                timers[id] = Task { [weak self] in
                    try? await Task.sleep(for: limit)
                    guard !Task.isCancelled else { return }
                    await self?.finish(id, with: .failure(ClientError.timeout(method: method)))
                }
                do {
                    try connection.write(line)
                } catch {
                    finish(id, with: .failure(ClientError.disconnected))
                }
            }
        } onCancel: {
            Task { await self.finish(id, with: .failure(CancellationError())) }
        }
    }

    private func rawCall(_ method: String, _ params: some Encodable) async throws -> JSONValue {
        try await rawCall(method, params: Wire.encode(params), limit: callTimeout)
    }

    /// Resumes the call `id` with `outcome` and stops its timer, unless it already ended.
    private func finish(_ id: Int, with outcome: Result<JSONValue, Error>) {
        timers.removeValue(forKey: id)?.cancel()
        guard let continuation = pending.removeValue(forKey: id) else { return }
        continuation.resume(with: outcome)
    }

    var pendingCount: Int { pending.count }

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
        daemonVersion = reply["daemon_version"]?.stringValue
        apiVersion = reply["api_version"]?.intValue
        var snapshot: Set<UUID>
        repeat {
            snapshot = Set(subscriptions.keys)
            try await resubscribe()
        } while Set(subscriptions.keys) != snapshot
    }

    var subscriptionCount: Int { subscriptions.count }
    var reconnectWatcherCount: Int { reconnectWatchers.count }

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
            finish(response.id, with: response.outcome.mapError { $0 })
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
        for timer in timers.values { timer.cancel() }
        timers = [:]
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
                for watcher in reconnectWatchers.values { watcher.yield() }
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
