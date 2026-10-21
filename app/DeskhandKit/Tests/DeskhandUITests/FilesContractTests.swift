import DeskhandClient
import DeskhandTestSupport
import Foundation
import XCTest
@testable import DeskhandUI

/// `files.list` paging, `files.stat` and `files.trash` as the daemon answers them (local-api.md
/// §3 파일, §8), against `MockDaemon` replying with JSON the daemon's encoders wrote.
@MainActor
final class FilesContractTests: XCTestCase {
    private func connected(_ daemon: MockDaemon) async throws -> (LiveBackend, DeskhandClient) {
        try daemon.start()
        let client = DeskhandClient(socketPath: daemon.path)
        try await client.connect(kind: "app", name: "test", version: "0")
        return (LiveBackend(client: client), client)
    }

    private func object(_ name: String) throws -> [String: Any] {
        try XCTUnwrap(JSONSerialization.jsonObject(with: DaemonFixture.data(name)) as? [String: Any])
    }

    private func json(_ object: Any) throws -> String {
        String(decoding: try JSONSerialization.data(withJSONObject: object, options: [.withoutEscapingSlashes]), as: UTF8.self)
    }

    func testPagesFollowTheCursorAndThePaneShowsTheFolderAsThePCSpellsIt() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let first = try DaemonFixture.line("files-list-page1")
        let second = try DaemonFixture.line("files-list-page2")
        let cursor = try XCTUnwrap(object("files-list-page1")["next_cursor"] as? String)
        daemon.setHandler { request in
            guard request.method == "files.list" else { return nil }
            return (request.params["cursor"] as? String) == cursor ? .result(second) : .result(first)
        }
        let (backend, client) = try await connected(daemon)
        let page = try await backend.filesList(hostID: "pc", path: "/HOME/PC/LISTING", cursor: nil, limit: 2)
        XCTAssertEqual(page.path, "/home/pc/listing")
        XCTAssertEqual(page.entries.map(\.name), ["Sub", "A.log"])
        XCTAssertEqual(page.total, 3)
        XCTAssertFalse(page.truncated)

        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: "pc", style: .posix), path: "/")
        await browser.open("/HOME/PC/LISTING")
        XCTAssertEqual(browser.path, "/home/pc/listing")
        await browser.loadMore()
        XCTAssertEqual(browser.items.map(\.name), ["Sub", "A.log", "b.txt"])
        XCTAssertEqual(browser.total, 3)
        XCTAssertFalse(browser.hasMore)
        XCTAssertEqual(daemon.requests(method: "files.list").last?.params["cursor"] as? String, cursor)
        await client.close()
    }

    func testStatOfAMissingPathIsNotAnError() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        let missing = try DaemonFixture.line("files-stat-missing")
        let file = try DaemonFixture.line("files-stat-file")
        daemon.setHandler { request in
            guard request.method == "files.stat" else { return nil }
            return (request.params["path"] as? String)?.hasSuffix("gone.txt") == true ? .result(missing) : .result(file)
        }
        let (backend, client) = try await connected(daemon)
        let gone = try await backend.filesStat(hostID: "pc", path: "/home/pc/listing/gone.txt")
        XCTAssertFalse(gone.exists)
        XCTAssertNil(gone.entry)
        let found = try await backend.filesStat(hostID: "pc", path: "/home/pc/listing/b.txt")
        XCTAssertEqual(found.entry?.size, 3)
        await client.close()
    }

    func testTrashThatMovedNothingThrowsAndARecycleBinRefusalSaysWhy() async throws {
        let daemon = MockDaemon()
        defer { daemon.stop() }
        var nothing = try object("files-trash-unsupported")
        nothing["status"] = "succeeded"
        nothing["action"] = ["executed": true, "outcome": "done"]
        nothing["error"] = NSNull()
        nothing["trashed"] = false
        var moved = nothing
        moved["trashed"] = true
        let replies = [
            "gone.txt": try json(nothing), "b.txt": try json(moved), "locked.txt": try DaemonFixture.line("files-trash-unsupported"),
        ]
        daemon.setHandler { request in
            guard request.method == "files.trash", let path = request.params["path"] as? String else { return nil }
            return replies.first { path.hasSuffix($0.key) }.map { .result($0.value) }
        }
        let (backend, client) = try await connected(daemon)
        try await backend.filesTrash(hostID: "pc", path: "/home/pc/b.txt")
        do {
            try await backend.filesTrash(hostID: "pc", path: "/home/pc/gone.txt")
            XCTFail("trashed:false is not a success")
        } catch {
            let failure = OperationError(error)
            XCTAssertEqual(failure.code, "NOT_FOUND")
            XCTAssertEqual(failure.message, "원격 PC에 그 항목이 없어 휴지통으로 옮긴 것이 없습니다")
        }
        do {
            try await backend.filesTrash(hostID: "pc", path: "/home/pc/locked.txt")
            XCTFail("a refusal throws")
        } catch {
            let failure = FilesModel.trashFailure(OperationError(error))
            XCTAssertEqual(failure.code, "CAPABILITY_UNSUPPORTED")
            XCTAssertEqual(failure.message, "원격 PC의 휴지통이 이 항목을 받지 않습니다")
        }
        await client.close()
    }

    func testTrashingAnItemThatIsGoneShowsTheReasonInsteadOfSuccess() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let dev = PreviewFixtures.devPCID
        let store = AppStore.seeded(backend: backend, defaults: UserDefaults(suiteName: "files-contract-\(UUID().uuidString)")!,
                                    hosts: PreviewFixtures.hosts(), selectedHostID: dev)
        let model = store.files(for: try XCTUnwrap(store.host(dev)))
        let downloads = "C:\\Users\\taeyoung\\Downloads"
        await model.open(.remote, path: downloads)
        let gone = FileItem(name: "이미 지운 파일.txt", path: downloads + "\\이미 지운 파일.txt", isDirectory: false, size: 10)
        await model.trash([gone])
        XCTAssertNotEqual(model.notice?.tone, .success)
        let error = try XCTUnwrap(model.paneErrors[.remote])
        XCTAssertEqual(error.message, "‘이미 지운 파일.txt’ 항목을 휴지통으로 옮기지 못했습니다")
        XCTAssertEqual(error.code, "NOT_FOUND")
        XCTAssertTrue(error.detail?.contains("휴지통으로 옮긴 것이 없습니다") == true)

        model.showInfo(gone, side: .remote)
        try await waitUntil { model.info?.isLoading == false }
        XCTAssertEqual(model.info?.missing, true)
        XCTAssertNil(model.info?.stat)
    }
}
