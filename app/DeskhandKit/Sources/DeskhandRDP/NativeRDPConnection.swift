import Foundation
import Observation
import OSLog

@MainActor @Observable
public final class NativeRDPConnection {
    public enum Phase: Equatable, Sendable {
        case idle, connecting, live, stopped, failed(UInt32)
    }

    public private(set) var phase: Phase = .idle
    public private(set) var frame: RDPFrame?
    public private(set) var serverSessionID: UInt32?
    public private(set) var receivedFrames: UInt64 = 0

    @ObservationIgnored private var performance = RDPPerformanceWindow(start: ProcessInfo.processInfo.systemUptime)
    @ObservationIgnored private let performanceLogger = Logger(subsystem: "io.github.yangtaeyoung.Deskhand", category: "RDPPerformance")
    @ObservationIgnored private let engineURL: URL?
    @ObservationIgnored private var worker: RDPWorker?
    @ObservationIgnored private var polling: Task<Void, Never>?
    @ObservationIgnored private var generation = UUID()

    public init(engineURL: URL? = nil) { self.engineURL = engineURL }

    public func connect(configuration: RDPConfiguration, username: String, domain: String, password: String) throws {
        disconnect()
        receivedFrames = 0
        performance = RDPPerformanceWindow(start: ProcessInfo.processInfo.systemUptime)
        phase = .connecting
        do {
            guard !username.isEmpty,
                  !username.utf8.contains(0), !domain.utf8.contains(0), !password.utf8.contains(0)
            else { throw RDPFailure.invalidCredentials }
            let engine = try RDPEngine(url: engineURL)
            let worker = try RDPWorker(engine: engine, configuration: configuration, username: username, domain: domain, password: password)
            self.worker = worker
            let generation = generation
            worker.start()
            polling = Task.detached(priority: .userInitiated) { [weak self, worker] in
                var lastSequence: UInt64?
                while !Task.isCancelled {
                    let snapshot = worker.poll(after: lastSequence)
                    guard !Task.isCancelled else { break }
                    if let frame = snapshot.frame { lastSequence = frame.sequence }
                    // Await one delivery before polling again: there is no frame
                    // stream buffer and no growing queue of MainActor tasks.
                    let accepted = await self?.receive(snapshot, generation: generation) ?? false
                    guard accepted, !snapshot.finished else { break }
                    do { try await Task.sleep(for: .milliseconds(16)) }
                    catch { break }
                }
                worker.stop()
            }
        } catch {
            phase = .failed(1)
            throw error
        }
    }

    /// Called after a unique frame is assigned to the layer; not a display fence.
    public func frameSubmitted(sequence: UInt64) {
        performance.submit(sequence: sequence, at: ProcessInfo.processInfo.systemUptime)
    }

    public func resize(to viewport: RDPViewport) {
        worker?.resize(viewport)
    }

    public func disconnect() {
        generation = UUID()
        polling?.cancel()
        polling = nil
        worker?.stop()
        worker = nil
        frame = nil
        serverSessionID = nil
        phase = .stopped
    }

    private func receive(_ snapshot: RDPSnapshot, generation: UUID) -> Bool {
        guard self.generation == generation else { return false }
        if snapshot.phase != phase, case .failed(let code) = snapshot.phase {
            performanceLogger.error("RDP_CONNECT_FAILED code=\(code, privacy: .public)")
        }
        phase = snapshot.phase
        serverSessionID = snapshot.sessionID
        if let frame = snapshot.frame {
            self.frame = frame
            receivedFrames &+= 1
            performance.receive(sequence: frame.sequence, width: frame.width, height: frame.height,
                                at: ProcessInfo.processInfo.systemUptime)
        }
        if let sample = performance.sample(at: ProcessInfo.processInfo.systemUptime),
           let data = try? JSONEncoder().encode(sample), let json = String(data: data, encoding: .utf8) {
            performanceLogger.info("RDP_PERF \(json, privacy: .public)")
        }
        if snapshot.finished {
            worker = nil
            polling = nil
        }
        return true
    }

    deinit {
        polling?.cancel()
        worker?.stop()
    }
}
