import Foundation

/// A JSON-RPC error returned by the daemon.
public struct RPCErrorInfo: Error, Sendable, Equatable {
    public let message: String
    public let code: String
    public let retryable: Bool
    public let nextAction: String?

    public init(message: String, code: String, retryable: Bool = false, nextAction: String? = nil) {
        self.message = message
        self.code = code
        self.retryable = retryable
        self.nextAction = nextAction
    }
}

extension RPCErrorInfo: LocalizedError {
    public var errorDescription: String? { message }
}

/// Client-side failures that are not daemon errors.
public enum ClientError: Error, Sendable, Equatable {
    case notConnected
    case disconnected
    case closed
    case socketPathTooLong(String)
    case connectFailed(errno: Int32)
    case protocolViolation(String)
    case daemonUnavailable(logPath: String)
    /// The daemon did not answer `method` within the call's limit.
    case timeout(method: String)
}
