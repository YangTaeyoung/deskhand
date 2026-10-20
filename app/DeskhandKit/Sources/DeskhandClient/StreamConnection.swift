import Foundation

/// A dedicated socket carrying binary frames after `stream.attach` (local-api.md section 4).
public final class StreamConnection: Sendable {
    public typealias Frame = (kind: UInt8, payload: Data)

    /// Frames from the daemon in arrival order; finishes when the daemon closes the stream.
    public let frames: AsyncThrowingStream<Frame, Error>
    private let connection: Connection

    private init(connection: Connection, frames: AsyncThrowingStream<Frame, Error>) {
        self.connection = connection
        self.frames = frames
    }

    /// Opens a socket, sends `hello` then `stream.attach`, and returns once the daemon accepted the attach.
    public static func attach(
        kind: String,
        params: [String: any Sendable],
        socketPath: String = Paths.socket,
        clientName: String = "Deskhand",
        clientVersion: String = "0"
    ) async throws -> StreamConnection {
        let connection = try Connection.connect(path: socketPath)
        var attachParams = params
        attachParams["kind"] = kind
        let attachData: Data
        do {
            attachData = try JSONSerialization.data(withJSONObject: attachParams)
        } catch {
            connection.close()
            throw ClientError.protocolViolation("stream.attach params are not JSON encodable")
        }
        let helloData = try Wire.encode(["client": "app", "name": clientName, "version": clientVersion])

        let (frames, continuation) = AsyncThrowingStream.makeStream(of: Frame.self)
        continuation.onTermination = { _ in connection.close() }

        try await withCheckedThrowingContinuation { (ready: CheckedContinuation<Void, Error>) in
            Task {
                var ready: CheckedContinuation<Void, Error>? = ready
                do {
                    var lines = LineBuffer()
                    var parser = FrameParser()
                    var phase = 0
                    try connection.write(Wire.request(id: 1, method: "hello", params: helloData))
                    for try await chunk in connection.chunks {
                        if phase < 2 {
                            for line in lines.append(chunk) {
                                guard phase < 2, let response = Wire.response(Wire.parse(line) ?? .null) else { continue }
                                if case .failure(let error) = response.outcome { throw error }
                                if phase == 0 {
                                    phase = 1
                                    try connection.write(Wire.request(id: 2, method: "stream.attach", params: attachData))
                                } else {
                                    phase = 2
                                    ready?.resume()
                                    ready = nil
                                    let rest = lines.takeRemainder()
                                    for f in try parser.append(rest) { continuation.yield(f) }
                                }
                            }
                        } else {
                            for f in try parser.append(chunk) { continuation.yield(f) }
                        }
                    }
                    if let pendingReady = ready { pendingReady.resume(throwing: ClientError.disconnected) }
                    continuation.finish()
                } catch {
                    if let pendingReady = ready { pendingReady.resume(throwing: error) }
                    continuation.finish(throwing: error)
                    connection.close()
                }
            }
        }
        return StreamConnection(connection: connection, frames: frames)
    }

    /// Sends one frame: u32 big-endian length (1 + payload), the kind byte and the payload.
    public func send(kind: UInt8, payload: Data) async throws {
        var frame = Data(capacity: 5 + payload.count)
        var length = UInt32(1 + payload.count).bigEndian
        withUnsafeBytes(of: &length) { frame.append(contentsOf: $0) }
        frame.append(kind)
        frame.append(payload)
        try connection.write(frame)
    }

    public func close() {
        connection.close()
    }
}
