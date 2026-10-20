import Foundation

/// Starts the daemon when its socket does not answer.
public struct DaemonLauncher: Sendable {
    public typealias Launch = @Sendable () async throws -> Void

    private let socketPath: String
    private let launch: Launch
    private let timeout: TimeInterval
    private let pollInterval: TimeInterval
    private let logPath: String

    public init(
        socketPath: String = Paths.socket,
        launch: Launch? = nil,
        timeout: TimeInterval = 5,
        pollInterval: TimeInterval = 0.1,
        logPath: String = Paths.daemonLog
    ) {
        self.socketPath = socketPath
        self.launch = launch ?? { try await DaemonLauncher.runDaemonStart() }
        self.timeout = timeout
        self.pollInterval = pollInterval
        self.logPath = logPath
    }

    /// Uses the default socket and the bundled CLI.
    public static func ensureRunning() async throws {
        try await DaemonLauncher().ensureRunning()
    }

    /// Returns once the socket accepts connections, starting the daemon first when needed.
    /// The whole call, including waiting for the start command, is bounded by `timeout`.
    public func ensureRunning() async throws {
        if isResponding() { return }
        let deadline = Date().addingTimeInterval(timeout)
        let launched = await runLaunch(budget: timeout)
        guard launched else { throw ClientError.daemonUnavailable(logPath: logPath) }
        while Date() < deadline {
            if isResponding() { return }
            try await Task.sleep(for: .seconds(pollInterval))
        }
        if isResponding() { return }
        throw ClientError.daemonUnavailable(logPath: logPath)
    }

    /// Runs the start command; false when it failed, true when it finished or the budget ran out first.
    private func runLaunch(budget: TimeInterval) async -> Bool {
        let launch = self.launch
        return await withCheckedContinuation { continuation in
            let once = Once(continuation)
            Task {
                do {
                    try await launch()
                    once.resume(true)
                } catch {
                    once.resume(false)
                }
            }
            Task {
                try? await Task.sleep(for: .seconds(budget))
                once.resume(true)
            }
        }
    }

    private func isResponding() -> Bool {
        guard let connection = try? Connection.connect(path: socketPath) else { return false }
        connection.close()
        return true
    }

    /// Runs `deskhand daemon start` from the app bundle, falling back to `deskhand` on PATH.
    static func runDaemonStart() async throws {
        try await runCLI(["daemon", "start"])
    }

    /// Runs `deskhand daemon stop` from the app bundle, falling back to `deskhand` on PATH.
    /// Returns once the command has finished; throws when it fails.
    public static func stopDaemon() async throws {
        try await runCLI(["daemon", "stop"])
    }

    private static func runCLI(_ arguments: [String]) async throws {
        let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/deskhand")
        let process = Process()
        if FileManager.default.isExecutableFile(atPath: bundled.path) {
            process.executableURL = bundled
            process.arguments = arguments
        } else {
            process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
            process.arguments = ["deskhand"] + arguments
        }
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        let status: Int32 = try await withCheckedThrowingContinuation { continuation in
            process.terminationHandler = { continuation.resume(returning: $0.terminationStatus) }
            do {
                try process.run()
            } catch {
                process.terminationHandler = nil
                continuation.resume(throwing: error)
            }
        }
        if status != 0 { throw ClientError.daemonUnavailable(logPath: Paths.daemonLog) }
    }
}

private final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Bool, Never>?

    init(_ continuation: CheckedContinuation<Bool, Never>) { self.continuation = continuation }

    func resume(_ value: Bool) {
        lock.lock()
        let c = continuation
        continuation = nil
        lock.unlock()
        c?.resume(returning: value)
    }
}
