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
    public func ensureRunning() async throws {
        if isResponding() { return }
        do {
            try await launch()
        } catch {
            throw ClientError.daemonUnavailable(logPath: logPath)
        }
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if isResponding() { return }
            try await Task.sleep(for: .seconds(pollInterval))
        }
        if isResponding() { return }
        throw ClientError.daemonUnavailable(logPath: logPath)
    }

    private func isResponding() -> Bool {
        guard let connection = try? Connection.connect(path: socketPath) else { return false }
        connection.close()
        return true
    }

    /// Runs `deskhand daemon start` from the app bundle, falling back to `deskhand` on PATH.
    static func runDaemonStart() async throws {
        let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/deskhand")
        let process = Process()
        if FileManager.default.isExecutableFile(atPath: bundled.path) {
            process.executableURL = bundled
            process.arguments = ["daemon", "start"]
        } else {
            process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
            process.arguments = ["deskhand", "daemon", "start"]
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
