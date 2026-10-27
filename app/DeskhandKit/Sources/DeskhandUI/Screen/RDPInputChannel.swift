import DeskhandClient
import DeskhandMedia
import Foundation
import Observation

/// Human input only. It never attaches an agent video stream or sends RDP input PDUs.
@MainActor @Observable
final class RDPInputChannel {
    private(set) var ready = false
    private(set) var failure: OperationError?
    private(set) var denied = false
    @ObservationIgnored private var stream: (any ScreenStream)?
    @ObservationIgnored private var outbox: AsyncStream<Data>.Continuation?
    @ObservationIgnored private var sequence = 0
    @ObservationIgnored private var runToken = UUID()

    func run(backend: any ScreenBackend, hostID: String, sessionID: UInt32, sid: String, connectionGeneration: Int) async {
        close()
        let token = UUID()
        runToken = token
        for attempt in 0...3 {
            guard !Task.isCancelled, token == runToken else { return }
            if attempt > 0 {
                do { try await Task.sleep(for: .milliseconds(500 * (1 << (attempt - 1)))) }
                catch { return }
                guard !Task.isCancelled, token == runToken else { return }
            }
            await attach(backend: backend, hostID: hostID, sessionID: sessionID, sid: sid,
                         connectionGeneration: connectionGeneration, token: token)
            guard !Task.isCancelled, token == runToken, !denied,
                  let failure, ["OFFLINE", "TIMEOUT"].contains(failure.code ?? "") else { return }
        }
        failure = OperationError(code: failure?.code ?? "OFFLINE",
                                 message: "입력 연결을 복구하지 못했습니다. 입력 다시 연결을 눌러 재시도하세요.")
    }

    private func attach(backend: any ScreenBackend, hostID: String, sessionID: UInt32, sid: String,
                        connectionGeneration: Int, token: UUID) async {
        failure = nil
        denied = false
        do {
            let connection = try await backend.inputAttach(hostID: hostID, sessionID: sessionID, sid: sid, connectionGeneration: connectionGeneration)
            if Task.isCancelled || token != runToken { connection.close(); return }
            stream = connection
            let (events, continuation) = AsyncStream<Data>.makeStream(bufferingPolicy: .bufferingOldest(256))
            outbox = continuation
            ready = true
            let sender = Task {
                do {
                    for await data in events {
                        try Task.checkCancellation()
                        try await connection.send(kind: ScreenFrameKind.input, payload: data)
                    }
                } catch {
                    if !Task.isCancelled, token == runToken { failure = inputFailure(error) }
                    connection.close()
                }
            }
            defer {
                if token == runToken { ready = false; outbox = nil; stream = nil }
                continuation.finish()
                sender.cancel()
                connection.close()
            }
            try await withTaskCancellationHandler {
                for try await frame in connection.frames {
                    if frame.kind == ScreenFrameKind.notice,
                       let notice = StreamNotice(json: frame.payload),
                       case .controlDenied = notice { denied = true }
                }
                if !Task.isCancelled, token == runToken, failure == nil {
                    failure = OperationError(code: "OFFLINE", message: "입력 연결이 끊겼습니다. 입력 연결을 다시 시도하세요")
                }
            } onCancel: { connection.close() }
        } catch {
            if !Task.isCancelled, token == runToken { failure = inputFailure(error) }
        }
    }

    private func inputFailure(_ error: any Error) -> OperationError {
        if let error = error as? ClientError {
            switch error {
            case .notConnected, .disconnected, .closed, .daemonUnavailable, .connectFailed:
                return OperationError(code: "OFFLINE", message: "입력 연결이 끊겨 다시 연결하는 중입니다")
            default: break
            }
        }
        return OperationError(error)
    }

    func send(_ event: InputEvent, gen: Int) {
        guard ready, let outbox else { return }
        sequence += 1
        if case .dropped = outbox.yield(event.json(seq: sequence, gen: gen)) {
            // Dropping a key-up could leave a key held. Closing makes the daemon release keys.
            ready = false
            failure = OperationError(code: "INPUT_BACKLOG", message: "입력 처리가 지연돼 연결을 멈췄습니다. 입력 연결을 다시 시도하세요")
            stream?.close()
        }
    }

    func close() {
        runToken = UUID()
        ready = false
        outbox?.finish()
        stream?.close()
        outbox = nil
        stream = nil
    }
}
