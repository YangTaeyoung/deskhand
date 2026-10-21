import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandClient

/// File and transfer models decode what the daemon's encoders write (local-api.md §8).
final class DaemonFileFixtureTests: XCTestCase {
    private struct TransferList: Decodable { let transfers: [Transfer] }

    private func event(_ name: String) throws -> DaemonEvent {
        try XCTUnwrap(DaemonEvent(params: Wire.decode(JSONValue.self, from: DaemonFixture.data(name))))
    }

    func testTransferListCarriesCountsWithoutFiles() throws {
        let list = try Wire.decode(TransferList.self, from: DaemonFixture.data("transfer-list"))
        let transfer = try XCTUnwrap(list.transfers.first)
        XCTAssertEqual(transfer.state, "failed")
        XCTAssertEqual(transfer.outcome, "partial")
        XCTAssertEqual([transfer.filesTotal, transfer.filesDone, transfer.filesFailed], [3, 2, 1])
        XCTAssertEqual(transfer.bytesTotal, 300_005)
        XCTAssertEqual(transfer.error?.code, "INVALID_ARGUMENT")
        XCTAssertEqual(transfer.error?.nextAction, "fix_params")
        XCTAssertEqual(transfer.rev, 3)
    }

    func testTransferGetCarriesTheFilesWithTheirPaths() throws {
        let detail = try Wire.decode(TransferDetail.self, from: DaemonFixture.data("transfer-get-failed-partial"))
        XCTAssertEqual(detail.transferID, detail.transfer.transferID)
        XCTAssertEqual(detail.files.map(\.rel), ["app.bin", "link.txt", "notes.txt"])
        XCTAssertEqual(detail.files.first?.path, "/home/pc/Build/app.bin")
        XCTAssertEqual(detail.files[1].error?.code, "INVALID_ARGUMENT")
        XCTAssertNil(detail.files[1].path)
        XCTAssertEqual(detail.filesFailed, 1)
    }

    func testTransferUpdatedCarriesTheTransferAndChangedFiles() throws {
        let events = try DaemonFixture.lines("events-transfer-updated").map { line in
            try XCTUnwrap(DaemonEvent(params: Wire.decode(JSONValue.self, from: Data(line.utf8))))
        }
        let updates = try events.map { try Wire.decode(TransferUpdate.self, from: Wire.encode(JSONValue.object($0.payload))) }
        XCTAssertEqual(updates.map(\.transfer.rev), [1, 2, 3])
        XCTAssertEqual(updates.map(\.changedFiles.count), [0, 0, 3])
        XCTAssertTrue(events.allSatisfy { $0.payload["transfer"]?["files"] == nil })
        XCTAssertEqual(updates.last?.changedFiles.map(\.state), ["done", "failed", "done"])
    }

    func testFilesWindowsCannotHoldFailOneByOne() throws {
        let update = try Wire.decode(TransferUpdate.self, from: Wire.encode(JSONValue.object(event("event-transfer-updated-names").payload)))
        let codes = Dictionary(uniqueKeysWithValues: update.changedFiles.map { ($0.rel, $0.error?.code ?? "") })
        XCTAssertEqual(codes["report.TXT"], "TRANSFER_CONFLICT")
        XCTAssertEqual(codes["CON.log"], "INVALID_ARGUMENT")
        XCTAssertEqual(codes["a?b.txt"], "INVALID_ARGUMENT")
        XCTAssertEqual(codes["notes."], "INVALID_ARGUMENT")
        XCTAssertEqual(codes["ok.txt"], "")
        XCTAssertEqual(update.transfer.state, "running")
        XCTAssertEqual(update.transfer.filesFailed, 4)
    }

    func testFilesStatReportsAMissingPathAsNotExisting() throws {
        let file = try Wire.decode(FileStat.self, from: DaemonFixture.data("files-stat-file"))
        XCTAssertTrue(file.exists)
        XCTAssertEqual(file.name, "b.txt")
        XCTAssertEqual(file.size, 3)
        XCTAssertNotNil(file.sha256)
        XCTAssertEqual(file.entry?.path, "/home/pc/listing/b.txt")

        let folder = try Wire.decode(FileStat.self, from: DaemonFixture.data("files-stat-dir"))
        XCTAssertEqual(folder.entry?.dir, true)

        let missing = try Wire.decode(FileStat.self, from: DaemonFixture.data("files-stat-missing"))
        XCTAssertFalse(missing.exists)
        XCTAssertNil(missing.entry)
        XCTAssertNil(missing.mtime)
        XCTAssertEqual(missing.name, "gone.txt")
    }
}
