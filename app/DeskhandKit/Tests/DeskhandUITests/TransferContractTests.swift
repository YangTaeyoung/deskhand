import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandUI

/// The transfer queue over the daemon's transfer contract (local-api.md §8), against
/// `MockDaemon` answering with JSON the daemon's encoders wrote.
@MainActor
final class TransferContractTests: XCTestCase {
    private func connected(_ daemon: MockDaemon) async throws -> (LiveBackend, DeskhandClient) {
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path, reconnectInitialDelay: 0.02, reconnectMaxDelay: 0.1)
        try await client.connect(kind: "app", name: "test", version: "0")
        return (LiveBackend(client: client), client)
    }

    /// The fixture `name` with its top-level members replaced by `changes`.
    private func fixture(_ name: String, _ changes: [String: Any] = [:]) throws -> [String: Any] {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: DaemonFixture.data(name)) as? [String: Any])
        for (key, value) in changes { object[key] = value }
        return object
    }

    private func json(_ object: Any) throws -> String {
        String(decoding: try JSONSerialization.data(withJSONObject: object, options: [.withoutEscapingSlashes]), as: UTF8.self)
    }

    private func event(_ object: [String: Any]) throws -> DaemonEvent {
        let value = try JSONDecoder().decode(JSONValue.self, from: Data(json(object).utf8))
        guard case .object(let payload) = value else { throw DecodingError.typeMismatch(JSONValue.self, .init(codingPath: [], debugDescription: "")) }
        return DaemonEvent(seq: payload["seq"]?.intValue ?? 0, hostID: payload["host_id"]?.stringValue,
                           kind: payload["kind"]?.stringValue ?? "", payload: payload)
    }

    func testTheListShowsCountsAndTheFilesAreReadWhenOpened() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let list = try DaemonFixture.line("transfer-list")
        let detail = try DaemonFixture.line("transfer-get-failed-partial")
        daemon.setHandler { request in
            switch request.method {
            case "transfer.list": .result(list)
            case "transfer.get": .result(detail)
            default: nil
            }
        }
        let (backend, client) = try await connected(daemon)
        let queue = TransferQueue(hostID: "pc", backend: backend)
        await queue.reload()
        let transfer = try XCTUnwrap(queue.transfers.first)
        XCTAssertEqual(transfer.filesTotal, 3)
        XCTAssertEqual(transfer.filesFailed, 1)
        XCTAssertNil(queue.files[transfer.transferID])
        XCTAssertTrue(daemon.requests(method: "transfer.get").isEmpty)

        queue.toggleFiles(transfer.transferID)
        try await waitUntil { queue.files[transfer.transferID]?.count == 3 }
        XCTAssertEqual(daemon.requests(method: "transfer.get").first?.params["transfer_id"] as? String, transfer.transferID)
        XCTAssertEqual(queue.files[transfer.transferID]?.first { $0.rel == "link.txt" }?.error?.code, "INVALID_ARGUMENT")
        let row = TransferPresentation(transfer, hostName: "pc")
        XCTAssertEqual(row.headline, "일부 완료")
        XCTAssertEqual(row.explanation, "3개 중 2개를 보냈고 1개는 실패했습니다. 실패한 파일은 아래 목록에 이유가 있습니다.")
        await client.close()
    }

    func testChangedFilesApplyByRelAndASkippedRevReadsTheFilesAgain() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let detail = try DaemonFixture.line("transfer-get-running-names")
        daemon.setHandler { request in
            request.method == "transfer.get" ? .result(detail) : nil
        }
        let (backend, client) = try await connected(daemon)
        let queue = TransferQueue(hostID: "windows-dev", backend: backend)
        let names = try fixture("event-transfer-updated-names")
        queue.handle(try event(names))
        let id = try XCTUnwrap(queue.transfers.first?.transferID)
        XCTAssertNil(queue.files[id], "an event alone does not read the files")

        queue.expand(id)
        try await waitUntil { queue.files[id]?.count == 6 }
        XCTAssertEqual(daemon.requests(method: "transfer.get").count, 1)

        var transfer = try XCTUnwrap(names["transfer"] as? [String: Any])
        transfer["rev"] = 5
        let done: [String: Any] = ["rel": "ok.txt", "size": 1200, "bytes_done": 1200, "state": "done", "outcome": "written",
                                   "path": "C:\\Users\\me\\Build\\ok.txt"]
        queue.handle(try event(["seq": 42, "ts": "2026-10-21T03:04:02.000Z", "kind": "transfer.updated", "host_id": "windows-dev",
                                "transfer": transfer, "changed_files": [done]]))
        XCTAssertEqual(queue.files[id]?.first { $0.rel == "ok.txt" }?.state, "done")
        XCTAssertEqual(queue.files[id]?.first { $0.rel == "ok.txt" }?.path, "C:\\Users\\me\\Build\\ok.txt")
        XCTAssertEqual(queue.files[id]?.first { $0.rel == "report.TXT" }?.error?.code, "TRANSFER_CONFLICT")
        XCTAssertEqual(queue.files[id]?.count, 6)
        XCTAssertEqual(daemon.requests(method: "transfer.get").count, 1, "the next rev needs no read")

        transfer["rev"] = 7
        queue.handle(try event(["seq": 44, "ts": "2026-10-21T03:04:03.000Z", "kind": "transfer.updated", "host_id": "windows-dev",
                                "transfer": transfer]))
        try await waitUntil { daemon.requests(method: "transfer.get").count >= 2 }
        await client.close()
    }

    func testAMoveRemovingItsSourceOffersNoPauseOrCancelAndExplainsTheRefusal() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let list = try DaemonFixture.line("transfer-list-verifying")
        let refusal = try fixture("transfer-cancel-removing")
        let data = try json(refusal["data"] as Any)
        let message = try XCTUnwrap(refusal["message"] as? String)
        daemon.setHandler { request in
            switch request.method {
            case "transfer.list": .result(list)
            case "transfer.cancel": .error(code: -32000, message: message, data: data)
            case "transfer.get": .error(code: -32000, message: "없음", data: #"{"code":"NOT_FOUND","retryable":false}"#)
            default: nil
            }
        }
        let (backend, client) = try await connected(daemon)
        let queue = TransferQueue(hostID: "windows-dev", backend: backend)
        await queue.reload()
        let transfer = try XCTUnwrap(queue.transfers.first)
        let row = TransferPresentation(transfer, hostName: "개발 PC")
        XCTAssertEqual(row.headline, "원본 정리 중")
        XCTAssertEqual(row.actions, [])
        XCTAssertEqual(row.explanation, "받은 파일을 모두 확인했습니다. 개발 PC의 원본을 휴지통으로 옮기는 중이라 일시정지하거나 취소할 수 없습니다.")

        queue.perform(.cancel, on: transfer.transferID)
        try await waitUntil { queue.actionErrors[transfer.transferID] != nil && !queue.isRunning(transfer.transferID) }
        XCTAssertEqual(queue.actionErrors[transfer.transferID]?.message, "원본을 정리하는 중이라 취소할 수 없습니다. 정리가 끝나면 이동이 끝납니다.")
        XCTAssertEqual(queue.actionErrors[transfer.transferID]?.code, "INVALID_ARGUMENT")
        try await waitUntil { daemon.requests(method: "transfer.get").count == 1 }
        await client.close()
    }

    func testFinishedTransfersTheDaemonPrunedLeaveTheQueue() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        daemon.setHandler { request in
            request.method == "transfer.list" ? .result(#"{"transfers":[]}"#) : nil
        }
        let (backend, client) = try await connected(daemon)
        let queue = TransferQueue(hostID: "h", backend: backend)
        func transfer(_ id: String, _ state: TransferState) -> Transfer {
            Transfer(transferID: id, hostID: "h", direction: "push", local: "/a", remote: "C:\\a", onConflict: "fail", state: state.rawValue,
                     bytesDone: 0, bytesTotal: 1, filesDone: 0, filesTotal: 1, rateBps: 0, rev: 3)
        }
        queue.seed([transfer("t-old", .done), transfer("t-live", .running)], files: ["t-old": []])
        await queue.reload()
        XCTAssertEqual(queue.transfers.map(\.transferID), ["t-live"])
        XCTAssertNil(queue.files["t-old"])
        await client.close()
    }

    func testTransferStartReturnsTheTransferAndItsJob() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let started = try DaemonFixture.line("transfer-start")
        let refused = try DaemonFixture.line("transfer-start-refused")
        let envelope = try fixture("transfer-start")
        let transferID = try XCTUnwrap(envelope["transfer_id"] as? String)
        let jobID = try XCTUnwrap(envelope["job_id"] as? String)
        daemon.setHandler { request in
            guard request.method == "transfer.start" else { return nil }
            return (request.params["local"] as? String) == "/missing" ? .result(refused) : .result(started)
        }
        let (backend, client) = try await connected(daemon)
        let result = try await backend.transferStart(TransferRequest(hostID: "pc", direction: .push, local: "/Users/me/Build",
                                                                     remote: "/home/pc/Build", onConflict: .fail))
        XCTAssertEqual(result, TransferStarted(transferID: transferID, jobID: jobID))
        XCTAssertTrue(transferID.hasPrefix("t-") && jobID.hasPrefix("j-"))
        do {
            _ = try await backend.transferStart(TransferRequest(hostID: "pc", direction: .push, local: "/missing", remote: "/home/pc/Build",
                                                                onConflict: .fail))
            XCTFail("a transfer that did not start throws")
        } catch {
            XCTAssertEqual(OperationError(error).code, "NOT_FOUND")
        }
        await client.close()
    }

    func testAFinishedRenamedPullReadsWhereItsFileWent() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let renamed: [String: Any] = [
            "transfer_id": "t-r", "host_id": "h", "direction": "pull", "local": "/Users/me/Downloads/a.log", "remote": "C:\\a.log",
            "on_conflict": "rename", "state": "done", "outcome": "renamed", "bytes_done": 5, "bytes_total": 5, "files_done": 1,
            "files_failed": 0, "files_total": 1, "rate_bps": 0, "rev": 6, "updated_at": "2026-10-21T03:04:01.767Z",
            "files": [["rel": "a.log", "size": 5, "bytes_done": 5, "state": "done", "outcome": "renamed",
                       "path": "/Users/me/Downloads/a (1).log"]],
        ]
        let detail = try json(renamed)
        daemon.setHandler { request in request.method == "transfer.get" ? .result(detail) : nil }
        let (backend, client) = try await connected(daemon)
        let queue = TransferQueue(hostID: "h", backend: backend)
        var light = renamed
        light["files"] = nil
        queue.handle(try event(["seq": 7, "ts": "2026-10-21T03:04:01.767Z", "kind": "transfer.updated", "host_id": "h", "transfer": light]))
        let transfer = try XCTUnwrap(queue.transfer("t-r"))
        try await waitUntil { queue.files["t-r"] != nil }
        XCTAssertEqual(queue.writtenPath(of: transfer), "/Users/me/Downloads/a (1).log")
        await client.close()
    }

    func testPreviewTransfersFollowTheDaemonShapes() async throws {
        let base = PreviewBackend(stepDelay: .zero)
        let dev = PreviewFixtures.devPCID
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent("transfer-shapes-\(UUID().uuidString)/Build")
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder.deletingLastPathComponent()) }
        for name in ["app.bin", "aux.txt", "bad?.txt"] {
            try Data(repeating: 7, count: 2_048).write(to: folder.appendingPathComponent(name))
        }
        let events = base.events(kinds: [EventKind.transferUpdated], hostID: dev)
        let started = try await base.transferStart(TransferRequest(hostID: dev, direction: .push, local: folder.path,
                                                                   remote: "C:\\Users\\taeyoung\\Downloads\\Build", onConflict: .fail))
        XCTAssertNotNil(started.jobID)
        var changed: [TransferFile] = []
        var last: Transfer?
        for try await event in events {
            XCTAssertNil(event.payload["transfer"]?["files"], "events carry the transfer without its files")
            let update = try LiveBackend.decode(TransferUpdate.self, from: .object(event.payload))
            guard update.transfer.transferID == started.transferID else { continue }
            changed += update.changedFiles
            last = update.transfer
            if update.transfer.transferState.isFinished { break }
        }
        let finished = try XCTUnwrap(last)
        XCTAssertEqual(finished.transferState, .failed)
        XCTAssertEqual(finished.transferOutcome, .partial)
        XCTAssertEqual([finished.filesTotal, finished.filesDone, finished.filesFailed], [3, 1, 2])
        XCTAssertEqual(changed.last { $0.rel == "aux.txt" }?.error?.code, "INVALID_ARGUMENT")
        XCTAssertTrue(changed.last { $0.rel == "bad?.txt" }?.error?.message?.contains("허용하지 않는 문자 '?'") == true)
        XCTAssertEqual(changed.last { $0.rel == "app.bin" }?.path, "C:\\Users\\taeyoung\\Downloads\\Build\\app.bin")
        let listed = try await base.transferList(hostID: dev)
        XCTAssertEqual(listed.first { $0.transferID == started.transferID }?.filesFailed, 2)
        let detail = try await base.transferGet(transferID: started.transferID)
        XCTAssertEqual(detail.files.count, 3)
    }
}
