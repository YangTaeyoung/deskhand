import Darwin
import Foundation

/// A client connection accepted by `MockDaemon`.
public final class MockConnection: @unchecked Sendable {
    private let fd: Int32
    private let writeLock = NSLock()
    private let stateLock = NSLock()
    private var closed = false
    public var isSubscribed = false

    public init(fd: Int32) {
        self.fd = fd
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
    }

    public func send(line: String) { sendRaw(Data((line + "\n").utf8)) }

    public func sendRaw(_ data: Data) {
        writeLock.lock()
        defer { writeLock.unlock() }
        var offset = 0
        data.withUnsafeBytes { buf in
            while offset < data.count {
                let n = write(fd, buf.baseAddress! + offset, data.count - offset)
                if n <= 0 { return }
                offset += n
            }
        }
    }

    public func shutdownConnection() {
        stateLock.lock()
        defer { stateLock.unlock() }
        if !closed {
            closed = true
            shutdown(fd, SHUT_RDWR)
        }
    }

    public func closeDescriptor() {
        stateLock.lock()
        defer { stateLock.unlock() }
        closed = true
        close(fd)
    }

    public func readChunk() -> Data? {
        var buf = [UInt8](repeating: 0, count: 65536)
        while true {
            let n = read(fd, &buf, buf.count)
            if n > 0 { return Data(buf[0..<n]) }
            if n < 0 && errno == EINTR { continue }
            return nil
        }
    }
}

public struct MockRequest: @unchecked Sendable {
    public let id: Int
    public let method: String
    public let params: [String: Any]


    public var paramsData: Data { (try? JSONSerialization.data(withJSONObject: params)) ?? Data() }
}

public enum MockReply {
    case result(String)
    case error(code: Int, message: String, data: String?)
}

/// In-process unix-socket NDJSON JSON-RPC server used by the client and UI tests.
public final class MockDaemon: @unchecked Sendable {
    public let path: String
    private let dir: String
    private let lock = NSCondition()
    private var listenFD: Int32 = -1
    private var connections: [MockConnection] = []
    private var requests: [MockRequest] = []
    private var acceptedCount = 0
    private var handler: @Sendable (MockRequest) -> MockReply? = { _ in nil }
    private var attachHandler: @Sendable (MockConnection) -> Void = { _ in }
    private var subscribeHook: @Sendable (MockRequest) -> Void = { _ in }
    private var attachReplyOverride: (@Sendable (MockConnection, Int) -> Void)?

    public init() {
        dir = "/tmp/dhc-\(UUID().uuidString.prefix(8))"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        path = dir + "/d.sock"
    }

    deinit {
        stop()
        try? FileManager.default.removeItem(atPath: dir)
    }

    public func setHandler(_ h: @escaping @Sendable (MockRequest) -> MockReply?) {
        lock.lock(); handler = h; lock.unlock()
    }

    public func setAttachHandler(_ h: @escaping @Sendable (MockConnection) -> Void) {
        lock.lock(); attachHandler = h; lock.unlock()
    }

    public func setSubscribeHook(_ h: @escaping @Sendable (MockRequest) -> Void) {
        lock.lock(); subscribeHook = h; lock.unlock()
    }

    /// Replaces the `stream.attach` success reply; the closure writes the reply (and anything after it) itself.
    public func setAttachReply(_ h: @escaping @Sendable (MockConnection, Int) -> Void) {
        lock.lock(); attachReplyOverride = h; lock.unlock()
    }

    public func start() throws {
        unlink(path)
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        precondition(fd >= 0)
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8CString)
        withUnsafeMutableBytes(of: &addr.sun_path) { raw in
            for (i, b) in bytes.enumerated() { raw[i] = UInt8(bitPattern: b) }
        }
        let rc = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard rc == 0, listen(fd, 16) == 0 else {
            close(fd)
            throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
        }
        lock.lock(); listenFD = fd; lock.unlock()
        let t = Thread { [self] in acceptLoop(fd) }
        t.start()
    }

    public func stop() {
        lock.lock()
        let fd = listenFD
        listenFD = -1
        let conns = connections
        connections = []
        lock.unlock()
        if fd >= 0 {
            shutdown(fd, SHUT_RDWR)
            close(fd)
        }
        unlink(path)
        for c in conns { c.shutdownConnection() }
    }

    private func acceptLoop(_ fd: Int32) {
        while true {
            let cfd = accept(fd, nil, nil)
            if cfd < 0 { return }
            let conn = MockConnection(fd: cfd)
            lock.lock()
            connections.append(conn)
            acceptedCount += 1
            lock.broadcast()
            lock.unlock()
            Thread { [self] in serve(conn) }.start()
        }
    }

    private func serve(_ conn: MockConnection) {
        var buffer = Data()
        while let chunk = conn.readChunk() {
            buffer.append(chunk)
            while let nl = buffer.firstIndex(of: 0x0A) {
                let line = buffer.subdata(in: buffer.startIndex..<nl)
                buffer.removeSubrange(buffer.startIndex...nl)
                if handle(line, conn) { return }
            }
        }
        conn.closeDescriptor()
    }

    /// Returns true when the connection switched to raw mode.
    private func handle(_ line: Data, _ conn: MockConnection) -> Bool {
        guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
              let id = obj["id"] as? Int, let method = obj["method"] as? String else { return false }
        let req = MockRequest(id: id, method: method, params: obj["params"] as? [String: Any] ?? [:])
        lock.lock()
        requests.append(req)
        let h = handler
        let attach = attachHandler
        let hook = subscribeHook
        let attachOverride = attachReplyOverride
        lock.broadcast()
        lock.unlock()

        switch method {
        case "hello":
            reply(conn, id, .result(#"{"client_id":"c-test","daemon_version":"0.4.2","api_version":1}"#))
        case "events.subscribe":
            conn.isSubscribed = true
            hook(req)
            reply(conn, id, .result("{}"))
        case "stream.attach":
            if let attachOverride {
                attachOverride(conn, id)
            } else {
                reply(conn, id, .result(#"{"ok":true}"#))
            }
            attach(conn)
            return false
        default:
            if let r = h(req) { reply(conn, id, r) } else { reply(conn, id, .result("{}")) }
        }
        return false
    }

    private func reply(_ conn: MockConnection, _ id: Int, _ r: MockReply) {
        switch r {
        case .result(let json):
            conn.send(line: #"{"jsonrpc":"2.0","id":\#(id),"result":\#(json)}"#)
        case .error(let code, let message, let data):
            let msg = String(data: try! JSONSerialization.data(withJSONObject: [message], options: []), encoding: .utf8)!
            let m = String(msg.dropFirst().dropLast())
            let d = data.map { #","data":\#($0)"# } ?? ""
            conn.send(line: #"{"jsonrpc":"2.0","id":\#(id),"error":{"code":\#(code),"message":\#(m)\#(d)}}"#)
        }
    }

    /// Sends an `event` notification to every subscribed connection.
    public func emit(_ params: String) {
        lock.lock(); let conns = connections; lock.unlock()
        for c in conns where c.isSubscribed {
            c.send(line: #"{"jsonrpc":"2.0","method":"event","params":\#(params)}"#)
        }
    }

    public func requests(method: String) -> [MockRequest] {
        lock.lock(); defer { lock.unlock() }
        return requests.filter { $0.method == method }
    }

    /// Blocks until `count` requests with `method` have arrived or the timeout passes.
    @discardableResult
    public func waitForRequests(method: String, count: Int, timeout: TimeInterval = 5) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        lock.lock(); defer { lock.unlock() }
        while requests.filter({ $0.method == method }).count < count {
            if !lock.wait(until: deadline) { return false }
        }
        return true
    }

    /// Closes all client connections but keeps listening.
    public func dropConnections() {
        lock.lock(); let conns = connections; connections = []; lock.unlock()
        for c in conns { c.shutdownConnection() }
    }
}
