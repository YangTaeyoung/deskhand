import Darwin
import Foundation

/// A JSON document node.
public enum JSONValue: Codable, Sendable, Equatable {
    case null
    case bool(Bool)
    case int(Int)
    case double(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let v = try? c.decode(Bool.self) { self = .bool(v) }
        else if let v = try? c.decode(Int.self) { self = .int(v) }
        else if let v = try? c.decode(Double.self) { self = .double(v) }
        else if let v = try? c.decode(String.self) { self = .string(v) }
        else if let v = try? c.decode([JSONValue].self) { self = .array(v) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let v): try c.encode(v)
        case .int(let v): try c.encode(v)
        case .double(let v): try c.encode(v)
        case .string(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .object(let v): try c.encode(v)
        }
    }

    public subscript(key: String) -> JSONValue? {
        if case .object(let o) = self { return o[key] }
        return nil
    }

    public var stringValue: String? {
        if case .string(let s) = self { return s }
        return nil
    }

    public var intValue: Int? {
        switch self {
        case .int(let n): return n
        case .double(let d): return Int(exactly: d)
        default: return nil
        }
    }

    public var boolValue: Bool? {
        if case .bool(let b) = self { return b }
        return nil
    }
}

/// Empty request parameters (`{}`).
public struct NoParams: Encodable, Sendable {
    public init() {}
}

/// Result type for methods whose result is ignored.
public struct Empty: Decodable, Sendable {
    public init() {}
    public init(from decoder: Decoder) throws {}
}

enum Wire {
    static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(type, from: data)
    }

    static func encode<T: Encodable>(_ value: T) throws -> Data {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        return try encoder.encode(value)
    }

    static func parse(_ line: Data) -> JSONValue? {
        try? JSONDecoder().decode(JSONValue.self, from: line)
    }

    static func request(id: Int, method: String, params: Data) throws -> Data {
        var data = Data(#"{"jsonrpc":"2.0","id":"#.utf8)
        data.append(Data(String(id).utf8))
        data.append(Data(#","method":"#.utf8))
        data.append(try JSONEncoder().encode(method))
        data.append(Data(#","params":"#.utf8))
        data.append(params)
        data.append(Data("}\n".utf8))
        return data
    }

    /// Maps a response object's `error` member to `RPCErrorInfo`.
    static func rpcError(_ error: JSONValue) -> RPCErrorInfo {
        let message = error["message"]?.stringValue ?? "daemon error"
        guard let data = error["data"], case .object = data else {
            return RPCErrorInfo(message: message, code: "INTERNAL")
        }
        return RPCErrorInfo(
            message: message,
            code: data["code"]?.stringValue ?? "INTERNAL",
            retryable: data["retryable"]?.boolValue ?? false,
            nextAction: data["next_action"]?.stringValue)
    }

    /// Splits a response object into its id and outcome; nil when it is not a response.
    static func response(_ value: JSONValue) -> (id: Int, outcome: Result<JSONValue, RPCErrorInfo>)? {
        guard let id = value["id"]?.intValue else { return nil }
        if let error = value["error"], error != .null { return (id, .failure(rpcError(error))) }
        if let result = value["result"] { return (id, .success(result)) }
        return nil
    }
}

/// Accumulates bytes and yields complete newline-terminated lines.
struct LineBuffer {
    private var buffer = Data()

    mutating func append(_ data: Data) -> [Data] {
        buffer.append(data)
        var lines: [Data] = []
        while let nl = buffer.firstIndex(of: 0x0A) {
            let line = buffer.subdata(in: buffer.startIndex..<nl)
            buffer.removeSubrange(buffer.startIndex...nl)
            if !line.isEmpty { lines.append(line) }
        }
        return lines
    }

    /// Bytes received after the last newline.
    mutating func takeRemainder() -> Data {
        defer { buffer = Data() }
        return buffer
    }
}

/// Accumulates bytes and yields complete binary frames: u32 BE length (= 1 + payload) and a kind byte.
struct FrameParser {
    static let maxFrameLength = 64 << 20
    private var buffer = Data()

    mutating func append(_ data: Data) throws -> [(kind: UInt8, payload: Data)] {
        buffer.append(data)
        var frames: [(kind: UInt8, payload: Data)] = []
        while buffer.count >= 4 {
            let s = buffer.startIndex
            let length = Int(UInt32(buffer[s]) << 24 | UInt32(buffer[s + 1]) << 16 | UInt32(buffer[s + 2]) << 8 | UInt32(buffer[s + 3]))
            guard length >= 1, length <= Self.maxFrameLength else {
                throw ClientError.protocolViolation("invalid frame length \(length)")
            }
            guard buffer.count >= 4 + length else { break }
            let kind = buffer[s + 4]
            let payload = buffer.subdata(in: (s + 5)..<(s + 4 + length))
            buffer.removeSubrange(s..<(s + 4 + length))
            frames.append((kind, payload))
        }
        return frames
    }
}

/// A connected unix-domain stream socket with a background reader.
final class Connection: @unchecked Sendable {
    /// Chunks as they arrive; finishes at EOF and fails on a read error.
    let chunks: AsyncThrowingStream<Data, Error>

    private let fd: Int32
    private let continuation: AsyncThrowingStream<Data, Error>.Continuation
    private let writeLock = NSLock()
    private let stateLock = NSLock()
    private var closing = false
    private var readerDone = false
    private var fdClosed = false

    private init(fd: Int32) {
        self.fd = fd
        (chunks, continuation) = AsyncThrowingStream.makeStream(of: Data.self)
    }

    static func connect(path: String) throws -> Connection {
        var addr = sockaddr_un()
        let bytes = Array(path.utf8CString)
        guard bytes.count <= MemoryLayout.size(ofValue: addr.sun_path) else {
            throw ClientError.socketPathTooLong(path)
        }
        addr.sun_family = sa_family_t(AF_UNIX)
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &addr.sun_path) { raw in
            for (i, b) in bytes.enumerated() { raw[i] = UInt8(bitPattern: b) }
        }
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw ClientError.connectFailed(errno: errno) }
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        _ = fcntl(fd, F_SETFD, FD_CLOEXEC)
        let rc = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard rc == 0 else {
            let code = errno
            Darwin.close(fd)
            throw ClientError.connectFailed(errno: code)
        }
        let connection = Connection(fd: fd)
        connection.startReader()
        return connection
    }

    private func startReader() {
        let fd = self.fd
        DispatchQueue.global(qos: .userInitiated).async { [self] in
            var buffer = [UInt8](repeating: 0, count: 64 * 1024)
            var failure: Error?
            while true {
                let n = read(fd, &buffer, buffer.count)
                if n > 0 {
                    continuation.yield(Data(buffer[0..<n]))
                } else if n == 0 {
                    break
                } else if errno == EINTR {
                    continue
                } else {
                    failure = ClientError.connectFailed(errno: errno)
                    break
                }
            }
            stateLock.lock()
            closing = true
            readerDone = true
            stateLock.unlock()
            continuation.finish(throwing: failure)
            closeDescriptorIfIdle()
        }
    }

    func write(_ data: Data) throws {
        writeLock.lock()
        defer { writeLock.unlock() }
        stateLock.lock()
        let isClosing = closing
        stateLock.unlock()
        if isClosing { throw ClientError.disconnected }
        var offset = 0
        try data.withUnsafeBytes { raw in
            while offset < raw.count {
                let n = Darwin.write(fd, raw.baseAddress! + offset, raw.count - offset)
                if n > 0 {
                    offset += n
                } else if n < 0 && errno == EINTR {
                    continue
                } else {
                    throw ClientError.disconnected
                }
            }
        }
    }

    /// Shuts the socket down; the descriptor is released once the reader and writers are done.
    func close() {
        stateLock.lock()
        let first = !closing
        closing = true
        stateLock.unlock()
        if first { shutdown(fd, SHUT_RDWR) }
        closeDescriptorIfIdle()
    }

    private func closeDescriptorIfIdle() {
        writeLock.lock()
        defer { writeLock.unlock() }
        stateLock.lock()
        defer { stateLock.unlock() }
        if closing && readerDone && !fdClosed {
            fdClosed = true
            Darwin.close(fd)
        }
    }
}
