import Darwin
import Dispatch
import Foundation
import Synchronization

struct RDPSnapshot: Sendable {
    let phase: NativeRDPConnection.Phase
    let frame: RDPFrame?
    let sessionID: UInt32?
    let finished: Bool
}

final class RDPWorker: Sendable {
    private struct Storage {
        var handle: UnsafeMutableRawPointer?
        var stopRequested = false
        var completion: RDPSnapshot?
    }

    private struct RawFrame {
        let pixels: Data
        let width: Int
        let height: Int
        let stride: Int
        let sequence: UInt64

        var frame: RDPFrame {
            RDPFrame(pixels: pixels, width: width, height: height, stride: stride, sequence: sequence)
        }
    }

    private let engine: RDPEngine
    private let storage: Mutex<Storage>

    init(engine: RDPEngine, configuration: RDPConfiguration, username: String, domain: String, password: String) throws {
        // No Swift worker/task captures credentials. Erase our temporary password
        // copy after new copies it; the caller still owns the original String.
        let handle = configuration.host.withCString { host in
            username.withCString { user in
                domain.withCString { domain in
                    withUnsafeTemporaryAllocation(of: CChar.self, capacity: password.utf8.count + 1) { passwordBytes in
                        for (index, byte) in password.utf8.enumerated() {
                            passwordBytes[index] = CChar(bitPattern: byte)
                        }
                        passwordBytes[password.utf8.count] = 0
                        defer { _ = memset_s(passwordBytes.baseAddress, passwordBytes.count, 0, passwordBytes.count) }
                        return configuration.certificateSHA256.withCString { fingerprint in
                            engine.new(host, configuration.port, user, domain, passwordBytes.baseAddress!, fingerprint, configuration.width, configuration.height)
                        }
                    }
                }
            }
        }
        guard let handle else { throw RDPFailure.sessionUnavailable }
        self.engine = engine
        storage = Mutex(Storage(handle: handle))
    }

    func start() {
        // The blocking C run loop must not occupy a cooperative Swift executor.
        let started = DispatchSemaphore(value: 0)
        DispatchQueue(label: "DeskhandRDP.run", qos: .userInitiated).async { [self] in
            started.signal()
            run()
        }
        // Only wait for the worker to start, never for the network connection.
        started.wait()
    }

    func resize(_ viewport: RDPViewport) {
        storage.withLock { state in
            guard let handle = state.handle, !state.stopRequested else { return }
            _ = engine.resize(handle, viewport.width, viewport.height, viewport.desktopScale)
        }
    }

    func stop() {
        storage.withLock { state in
            guard !state.stopRequested else { return }
            state.stopRequested = true
            if let handle = state.handle { engine.stop(handle) }
        }
    }

    private func run() {
        let handle = storage.withLock { $0.handle }
        // run alone may use the pointer outside the mutex. Its queue owns self;
        // the only free site below is reached after run returns. A stop before
        // this call is supported by the engine and still runs its cleanup path.
        let result = handle.map { engine.run($0) } ?? 0
        storage.withLock { state in
            guard let handle = state.handle else { return }
            let error = engine.error(handle)
            let failed = engine.state(handle) == 4 || result != 0
            let sessionID = engine.sessionID(handle)
            state.completion = RDPSnapshot(
                phase: state.stopRequested ? .stopped : (failed ? .failed(error == 0 ? 1 : error) : .stopped),
                frame: nil, sessionID: sessionID == .max ? nil : sessionID, finished: true
            )
            engine.free(handle)
            state.handle = nil
        }
    }

    func poll(after sequence: UInt64?) -> RDPSnapshot {
        let (snapshot, rawFrame): (RDPSnapshot, RawFrame?) = storage.withLock { state in
            if let completion = state.completion { return (completion, nil) }
            guard let handle = state.handle, !state.stopRequested else {
                return (RDPSnapshot(phase: .stopped, frame: nil, sessionID: nil, finished: false), nil)
            }
            let nativeState = engine.state(handle)
            let sessionID = engine.sessionID(handle)
            let phase: NativeRDPConnection.Phase
            switch nativeState {
            case 2: phase = .live
            case 3: phase = .stopped
            case 4: phase = .failed(engine.error(handle))
            default: phase = .connecting
            }
            return (RDPSnapshot(phase: phase, frame: nil, sessionID: sessionID == .max ? nil : sessionID, finished: false), copyLatest(handle, after: sequence))
        }
        // Normalize pixels after releasing the pointer lock so disconnect does
        // not wait for a full image traversal.
        return RDPSnapshot(phase: snapshot.phase, frame: rawFrame?.frame, sessionID: snapshot.sessionID, finished: snapshot.finished)
    }

    private func copyLatest(_ handle: UnsafeMutableRawPointer, after previous: UInt64?) -> RawFrame? {
        var width: UInt32 = 0, height: UInt32 = 0, stride: UInt32 = 0
        var sequence: UInt64 = 0
        let query = engine.copyFrame(handle, nil, 0, &width, &height, &stride, &sequence)
        guard query == -1 || query == 1, sequence != previous,
              let count = RDPFrame.byteCount(width: Int(width), height: Int(height), stride: Int(stride))
        else { return nil }
        var pixels = Data(count: count)
        let copied = pixels.withUnsafeMutableBytes { bytes in
            engine.copyFrame(handle, bytes.bindMemory(to: UInt8.self).baseAddress, count, &width, &height, &stride, &sequence)
        }
        // A resize can race the query; retry on the next tick, never allocate an
        // unbounded retry buffer or publish a partially copied frame.
        guard copied == 1, sequence != previous,
              let actualCount = RDPFrame.byteCount(width: Int(width), height: Int(height), stride: Int(stride)),
              actualCount <= count
        else { return nil }
        pixels.count = actualCount
        return RawFrame(pixels: pixels, width: Int(width), height: Int(height), stride: Int(stride), sequence: sequence)
    }
}
