import Foundation

/// A `deskhand` command that did not finish as it should.
public enum DaemonCommandError: Error, Equatable, Sendable {
    /// The command exited with `status`; `stderr` is what it wrote to standard error, trimmed
    /// and cut to its last 1,000 characters.
    case failed(command: String, status: Int32, stderr: String)
    /// The command ran longer than `seconds` and was ended.
    case timedOut(command: String, seconds: TimeInterval)
    /// The daemon still answered on its socket after `deskhand daemon stop` returned.
    case stillRunning
}

/// Starts the daemon when its socket does not answer, and restarts it.
public struct DaemonLauncher: Sendable {
    public typealias Launch = @Sendable () async throws -> Void

    private let socketPath: String
    private let launch: Launch
    private let stop: Launch
    private let timeout: TimeInterval
    private let stopTimeout: TimeInterval
    private let pollInterval: TimeInterval
    private let logPath: String

    /// `launch` starts the daemon and `stop` stops it; by default they run `deskhand daemon start`
    /// and `deskhand daemon stop`. `timeout` bounds starting, `stopTimeout` bounds stopping and
    /// the wait for the socket to stop answering.
    public init(
        socketPath: String = Paths.socket,
        launch: Launch? = nil,
        stop: Launch? = nil,
        timeout: TimeInterval = 5,
        stopTimeout: TimeInterval = 10,
        pollInterval: TimeInterval = 0.1,
        logPath: String = Paths.daemonLog
    ) {
        self.socketPath = socketPath
        self.launch = launch ?? { try await DaemonLauncher.runCLI(["daemon", "start"], timeout: timeout) }
        self.stop = stop ?? { try await DaemonLauncher.runCLI(["daemon", "stop"], timeout: stopTimeout) }
        self.timeout = timeout
        self.stopTimeout = stopTimeout
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

    /// Stops the daemon, waits until its socket stops answering, starts it and waits until the
    /// socket answers again. Stopping and the wait after it are bounded by `stopTimeout`,
    /// starting and the wait after it by `timeout`. A command that fails or runs too long throws
    /// its `DaemonCommandError`; a daemon that keeps answering throws `.stillRunning`.
    public func restart() async throws {
        try await bounded(stop, command: "deskhand daemon stop", seconds: stopTimeout)
        let stopDeadline = Date().addingTimeInterval(stopTimeout)
        while isResponding() {
            guard Date() < stopDeadline else { throw DaemonCommandError.stillRunning }
            try await Task.sleep(for: .seconds(pollInterval))
        }
        try await bounded(launch, command: "deskhand daemon start", seconds: timeout)
        let startDeadline = Date().addingTimeInterval(timeout)
        while !isResponding() {
            guard Date() < startDeadline else { throw ClientError.daemonUnavailable(logPath: logPath) }
            try await Task.sleep(for: .seconds(pollInterval))
        }
    }

    /// Runs `body`, throwing `.timedOut` when it has not finished after `seconds`.
    private func bounded(_ body: @escaping Launch, command: String, seconds: TimeInterval) async throws {
        try await withThrowingTaskGroup(of: Bool.self) { group in
            group.addTask {
                try await body()
                return true
            }
            group.addTask {
                try await Task.sleep(for: .seconds(seconds))
                return false
            }
            defer { group.cancelAll() }
            guard let finished = try await group.next(), finished else {
                throw DaemonCommandError.timedOut(command: command, seconds: seconds)
            }
        }
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

    /// Runs `deskhand <arguments>` from the app bundle, falling back to `deskhand` on PATH.
    private static func runCLI(_ arguments: [String], timeout: TimeInterval) async throws {
        let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/deskhand")
        let command = (["deskhand"] + arguments).joined(separator: " ")
        if FileManager.default.isExecutableFile(atPath: bundled.path) {
            try await run(bundled, arguments, command: command, timeout: timeout)
        } else {
            try await run(URL(fileURLWithPath: "/usr/bin/env"), ["deskhand"] + arguments, command: command, timeout: timeout)
        }
    }

    /// Runs `executable` with `arguments` and waits for it. A non-zero exit throws `.failed` with
    /// the standard error output; after `timeout` the process is terminated and `.timedOut` is
    /// thrown. `command` names the command in the errors.
    public static func run(_ executable: URL, _ arguments: [String], command: String, timeout: TimeInterval) async throws {
        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = FileHandle.nullDevice
        let errors = Pipe()
        process.standardError = errors
        let output = OutputBuffer()
        errors.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if !data.isEmpty { output.append(data) }
        }
        let outcome: Outcome = try await withCheckedThrowingContinuation { continuation in
            let once = OnceOutcome(continuation)
            process.terminationHandler = { once.resume(.exited($0.terminationStatus)) }
            do {
                try process.run()
            } catch {
                process.terminationHandler = nil
                once.fail(error)
                return
            }
            let box = ProcessBox(process)
            Task {
                try? await Task.sleep(for: .seconds(timeout))
                if once.resume(.timedOut) { box.process.terminate() }
            }
        }
        errors.fileHandleForReading.readabilityHandler = nil
        switch outcome {
        case .timedOut:
            throw DaemonCommandError.timedOut(command: command, seconds: timeout)
        case .exited(let status):
            output.append(errors.fileHandleForReading.readDataToEndOfFile())
            guard status == 0 else { throw DaemonCommandError.failed(command: command, status: status, stderr: output.text) }
        }
    }

    private enum Outcome: Sendable {
        case exited(Int32)
        case timedOut
    }
}

private final class ProcessBox: @unchecked Sendable {
    let process: Process
    init(_ process: Process) { self.process = process }
}

/// Standard error collected from a process while it runs.
private final class OutputBuffer: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()

    func append(_ chunk: Data) {
        lock.lock()
        data.append(chunk)
        lock.unlock()
    }

    /// The output trimmed and cut to its last 1,000 characters.
    var text: String {
        lock.lock()
        defer { lock.unlock() }
        let trimmed = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        return String(trimmed.suffix(1000))
    }
}

private final class OnceOutcome<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Value, Error>?

    init(_ continuation: CheckedContinuation<Value, Error>) { self.continuation = continuation }

    /// Resumes with `value` unless already resumed; returns whether this call resumed.
    @discardableResult
    func resume(_ value: Value) -> Bool {
        lock.lock()
        let c = continuation
        continuation = nil
        lock.unlock()
        c?.resume(returning: value)
        return c != nil
    }

    func fail(_ error: any Error) {
        lock.lock()
        let c = continuation
        continuation = nil
        lock.unlock()
        c?.resume(throwing: error)
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
