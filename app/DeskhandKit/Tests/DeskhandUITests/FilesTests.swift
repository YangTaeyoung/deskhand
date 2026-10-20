import AppKit
import DeskhandClient
import DeskhandTestSupport
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

@MainActor
final class FilesTests: XCTestCase {
    private let dev = PreviewFixtures.devPCID
    private let downloads = "C:\\Users\\taeyoung\\Downloads"
    private var scratch: URL!

    override func setUp() async throws {
        scratch = FileManager.default.temporaryDirectory.appendingPathComponent("deskhand-files-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: scratch)
    }

    private func makeStore(_ backend: GatedBackend) -> AppStore {
        let defaults = UserDefaults(suiteName: "files-tests-\(UUID().uuidString)")!
        return AppStore.seeded(backend: backend, defaults: defaults, hosts: PreviewFixtures.hosts(), selectedHostID: dev)
    }

    private func model(_ store: AppStore) throws -> FilesModel {
        store.files(for: try XCTUnwrap(store.host(dev)))
    }

    private func localFile(_ name: String, bytes: Int = 2_048) throws -> URL {
        let url = scratch.appendingPathComponent(name)
        try Data(repeating: 7, count: bytes).write(to: url)
        return url
    }

    private func pasteboard(with urls: [URL]) -> NSPasteboard {
        let pasteboard = NSPasteboard(name: NSPasteboard.Name("deskhand-files-test-\(UUID().uuidString)"))
        pasteboard.clearContents()
        pasteboard.writeObjects(urls.map { $0 as NSURL })
        return pasteboard
    }

    // MARK: Brief tests

    func testLargeDirectoryPaging() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: dev, style: .windows),
                                  path: PreviewFixtures.largeFolder)
        await browser.open(PreviewFixtures.largeFolder)

        XCTAssertEqual(browser.rows.count, 500)
        XCTAssertEqual(browser.total, 3000)
        XCTAssertTrue(browser.hasMore)
        XCTAssertEqual(browser.rows.first?.name, "capture-0001.png")

        for expected in stride(from: 1000, through: 3000, by: 500) {
            await browser.loadMore()
            XCTAssertEqual(browser.rows.count, expected)
        }
        XCTAssertFalse(browser.hasMore)
        XCTAssertEqual(browser.rows.last?.name, "capture-3000.png")
        XCTAssertEqual(Set(browser.rows.map(\.path)).count, 3000)
        XCTAssertEqual(backend.filesListCalls.map(\.cursor), [nil, "500", "1000", "1500", "2000", "2500"])
        XCTAssertEqual(Set(backend.filesListCalls.map(\.limit)), [FileBrowser.pageSize])
    }

    func testDropStartsPushTransfer() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let url = try localFile("빌드 결과 1188.zip")

        let payload = try XCTUnwrap(FileDropPayload(pasteboard: pasteboard(with: [url])))
        guard case .local(let urls) = payload else { return XCTFail("expected files of this Mac, got \(payload)") }
        XCTAssertEqual(urls.map(\.path), [url.path])

        let accepted = await model.handleDrop(payload, on: .remote, into: nil, move: false)
        XCTAssertTrue(accepted)
        XCTAssertEqual(backend.transferRequests, [
            TransferRequest(hostID: dev, direction: .push, local: url.path,
                            remote: downloads + "\\빌드 결과 1188.zip", onConflict: .fail),
        ])
        XCTAssertEqual(model.transfers.visible.first?.remote, downloads + "\\빌드 결과 1188.zip")
        XCTAssertEqual(model.transfers.visible.first?.transferDirection, .push)

        let folder = try XCTUnwrap(model.remote.item(named: "로그 모음"))
        await model.handleDrop(payload, on: .remote, into: folder, move: true)
        XCTAssertEqual(backend.transferRequests.last, TransferRequest(
            hostID: dev, direction: .movePush, local: url.path,
            remote: downloads + "\\로그 모음\\빌드 결과 1188.zip", onConflict: .fail))

        let rejected = await model.handleDrop(payload, on: .local, into: nil, move: false)
        XCTAssertFalse(rejected)
        XCTAssertEqual(backend.transferRequests.count, 2)
    }

    func testMoveShowsRetainedSource() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.start(host: try XCTUnwrap(store.host(dev)))
        try await waitUntil { model.transfers.loaded }
        await model.open(.local, path: scratch.path)
        await model.open(.remote, path: "C:\\Shop\\logs")
        let log = try XCTUnwrap(model.remote.item(named: "shop-2026-10-20.log"))

        await model.receive([RemoteDragItem(hostID: dev, item: log)], to: scratch.path, move: true)
        XCTAssertEqual(backend.transferRequests.last?.direction, .movePull)
        try await waitUntil { model.transfers.visible.first { $0.remote == log.path }?.transferState == .done }

        let transfer = try XCTUnwrap(model.transfers.visible.first { $0.remote == log.path })
        let row = TransferPresentation(transfer, hostName: "개발 PC")
        XCTAssertEqual(row.outcome, .copiedSourceRetained)
        XCTAssertEqual(row.headline, "복사됨 · 원본 남음")
        XCTAssertEqual(row.tone, .warning)
        let explanation = try XCTUnwrap(row.explanation)
        XCTAssertTrue(explanation.contains("원본을 지우지 못했습니다"), explanation)
        XCTAssertTrue(explanation.contains("원본은 개발 PC에 그대로 있습니다"), explanation)
        XCTAssertTrue(explanation.contains("Shop.exe"), explanation)
        XCTAssertEqual(row.actions.first, .showSource)
        XCTAssertEqual(TransferSummary([transfer]).failed, 1)

        await model.reload(.remote)
        XCTAssertNotNil(model.remote.item(named: "shop-2026-10-20.log"))
        let copy = scratch.appendingPathComponent("shop-2026-10-20.log").path
        XCTAssertFalse(FileManager.default.fileExists(atPath: copy))
        try await waitUntil { model.local.item(named: "shop-2026-10-20.log") != nil }
        XCTAssertEqual(model.local.item(named: "shop-2026-10-20.log")?.isPreviewOnly, true)
        XCTAssertEqual(model.localItem(at: copy)?.size, log.size)
    }

    // MARK: Listing

    func testOnePageListingIsRevealedInChunks() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        backend.listInOnePage(true)
        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: dev, style: .windows),
                                  path: PreviewFixtures.largeFolder)
        await browser.open(PreviewFixtures.largeFolder)
        XCTAssertEqual(browser.items.count, 3000)
        XCTAssertEqual(browser.rows.count, 500)
        XCTAssertTrue(browser.hasMore)
        await browser.loadMore()
        XCTAssertEqual(browser.rows.count, 1000)
        XCTAssertEqual(backend.filesListCalls.count, 1)
    }

    func testSearchCoversPagesNotLoadedYet() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: dev, style: .windows),
                                  path: PreviewFixtures.largeFolder)
        await browser.open(PreviewFixtures.largeFolder)
        browser.query = "capture-29"
        try await waitUntil { browser.nextCursor == nil && !browser.isLoadingMore }
        XCTAssertEqual(browser.matchCount, 100)
        XCTAssertEqual(browser.rows.first?.name, "capture-2900.png")
    }

    func testSortKeepsFoldersFirst() {
        let items = [
            FileItem(name: "b.log", path: "C:\\b.log", isDirectory: false, size: 10),
            FileItem(name: "a", path: "C:\\a", isDirectory: true),
            FileItem(name: "c.zip", path: "C:\\c.zip", isDirectory: false, size: 99),
            FileItem(name: "file 10.txt", path: "C:\\file 10.txt", isDirectory: false, size: 1),
            FileItem(name: "file 9.txt", path: "C:\\file 9.txt", isDirectory: false, size: 1),
        ]
        XCTAssertEqual(FileSort(key: .name).sorted(items).map(\.name), ["a", "b.log", "c.zip", "file 9.txt", "file 10.txt"])
        XCTAssertEqual(FileSort(key: .name, ascending: false).sorted(items).map(\.name), ["a", "file 10.txt", "file 9.txt", "c.zip", "b.log"])
        XCTAssertEqual(FileSort(key: .size, ascending: false).sorted(items).map(\.name), ["a", "c.zip", "b.log", "file 9.txt", "file 10.txt"])
        let mixed = ["Zeta.txt", "가나.txt", "_temp", "alpha.txt", "Ångström.txt", "10월 보고서", "9월 보고서", "나다.txt",
                     "🚀 출시.md", "日本語.txt"].map {
            FileItem(name: $0, path: "C:\\" + $0, isDirectory: false)
        }
        XCTAssertEqual(FileSort.standard.sorted(mixed).map(\.name),
                       ["_temp", "🚀 출시.md", "9월 보고서", "10월 보고서", "가나.txt", "나다.txt", "日本語.txt", "alpha.txt",
                        "Ångström.txt", "Zeta.txt"])
    }

    func testFolderErrorIsShownWithItsCode() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: dev, style: .windows), path: "C:\\")
        await browser.open(PreviewFixtures.deniedFolder)
        guard case .failed(let error) = browser.phase else { return XCTFail("expected a failure, got \(browser.phase)") }
        XCTAssertEqual(error.code, "PERMISSION_DENIED")
        XCTAssertEqual(browser.path, PreviewFixtures.deniedFolder)
        XCTAssertEqual(browser.parentPath, "C:\\Windows\\System32")
    }

    // MARK: Conflicts

    func testConflictAsksBeforeTransferring() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let taken = try localFile("report.html")
        let alsoTaken = try localFile("Deskhand-Setup-0.4.2.msi")
        let free = try localFile("새 보고서.html")

        await model.send([taken, free, alsoTaken], to: downloads, move: false)
        XCTAssertEqual(backend.transferRequests.map(\.local), [free.path])
        let prompt = try XCTUnwrap(model.conflict)
        XCTAssertEqual(prompt.items.map(\.candidate.name), ["report.html", "Deskhand-Setup-0.4.2.msi"])
        XCTAssertEqual(prompt.current?.existing.size, 48_210)

        await model.resolveConflict(.rename, applyToAll: false)
        XCTAssertEqual(backend.transferRequests.last?.onConflict, .rename)
        XCTAssertEqual(backend.transferRequests.last?.local, taken.path)
        XCTAssertEqual(model.conflict?.items.map(\.candidate.name), ["Deskhand-Setup-0.4.2.msi"])
        XCTAssertEqual(model.conflict?.id, prompt.id)

        await model.resolveConflict(.fail, applyToAll: false)
        XCTAssertNil(model.conflict)
        XCTAssertEqual(backend.transferRequests.count, 2)
    }

    func testConflictDecisionAppliesToAll() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let first = try localFile("report.html")
        let second = try localFile("Shop-build-1187.zip")
        await model.send([first, second], to: downloads, move: false)
        await model.resolveConflict(.replace, applyToAll: true)
        XCTAssertNil(model.conflict)
        XCTAssertEqual(backend.transferRequests.map(\.onConflict), [.replace, .replace])
    }

    func testPreferredPolicySkipsTheSheet() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        store.filesPreferences.conflictPolicy = .skip
        let model = try model(store)
        await model.open(.remote, path: downloads)
        await model.send([try localFile("report.html")], to: downloads, move: false)
        XCTAssertNil(model.conflict)
        XCTAssertEqual(backend.transferRequests.map(\.onConflict), [.skip])
    }

    // MARK: Remote actions

    func testPermanentDeleteWaitsForConfirmation() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let item = try XCTUnwrap(model.remote.item(named: "report.html"))

        model.requestPermanentDelete([item])
        let request = try XCTUnwrap(model.pendingDeletion)
        XCTAssertEqual(request.items, [item])
        XCTAssertTrue(backend.fileCalls.isEmpty)

        model.pendingDeletion = nil
        await model.confirmPermanentDelete(request)
        XCTAssertEqual(backend.fileCalls, [FileCall(method: "files.delete", path: item.path)])
        XCTAssertNil(model.remote.item(named: "report.html"))
        XCTAssertEqual(model.notice?.tone, .success)
    }

    func testTrashAndRenameReloadTheFolder() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let report = try XCTUnwrap(model.remote.item(named: "report.html"))
        await model.rename(report, to: "보고서.html")
        XCTAssertEqual(backend.fileCalls.last, FileCall(method: "files.rename", path: report.path, to: downloads + "\\보고서.html"))
        let renamed = try XCTUnwrap(model.remote.item(named: "보고서.html"))
        XCTAssertEqual(model.remote.selection, [renamed.path])

        await model.trash([renamed])
        XCTAssertEqual(backend.fileCalls.last, FileCall(method: "files.trash", path: renamed.path))
        XCTAssertNil(model.remote.item(named: "보고서.html"))
    }

    func testRenameRefusesWindowsReservedAndTakenNames() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let report = try XCTUnwrap(model.remote.item(named: "report.html"))
        XCTAssertNotNil(model.renameProblem(report, to: "CON.txt"))
        XCTAssertNotNil(model.renameProblem(report, to: "a|b"))
        XCTAssertNotNil(model.renameProblem(report, to: "끝에 점."))
        XCTAssertNotNil(model.renameProblem(report, to: "deskhand-setup-0.4.2.MSI"))
        XCTAssertNil(model.renameProblem(report, to: "Report.html"))
        await model.rename(report, to: "NUL")
        XCTAssertTrue(backend.fileCalls.isEmpty)
        XCTAssertEqual(model.paneErrors[.remote]?.tone, .error)
        XCTAssertNil(model.notice)
    }

    func testNewFolderPicksFreeNameAndStartsRenaming() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        await model.newFolder()
        await model.newFolder()
        XCTAssertEqual(backend.fileCalls.map(\.path), [downloads + "\\새 폴더", downloads + "\\새 폴더 2"])
        XCTAssertEqual(model.remote.renameRequest, downloads + "\\새 폴더 2")
    }

    func testOpenLogSwitchesToLogsTab() throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        let log = FileItem(name: "shop.log", path: "C:\\Shop\\logs\\shop.log", isDirectory: false, size: 10)
        model.openLog(log)
        XCTAssertEqual(store.layout.tab, .logs)
        XCTAssertEqual(store.logSelection, LogSelection(hostID: dev, path: log.path))
    }

    // MARK: Paths and presentation

    func testWindowsPaths() {
        let crumbs = FilePath.crumbs("C:\\Users\\taeyoung\\Downloads", style: .windows)
        XCTAssertEqual(crumbs.map(\.title), ["C:", "Users", "taeyoung", "Downloads"])
        XCTAssertEqual(crumbs.map(\.path), ["C:\\", "C:\\Users", "C:\\Users\\taeyoung", "C:\\Users\\taeyoung\\Downloads"])
        XCTAssertEqual(FilePath.parent("C:\\Users", style: .windows), "C:\\")
        XCTAssertNil(FilePath.parent("C:\\", style: .windows))
        XCTAssertEqual(FilePath.join("C:\\", "Users", style: .windows), "C:\\Users")
        XCTAssertEqual(FilePath.normalized("c:/Users/", style: .windows), "C:\\Users")
        XCTAssertEqual(FilePath.parent("/Users/taeyoung", style: .posix), "/Users")
        XCTAssertTrue(PathStyle.windows.sameName("Report.HTML", "report.html"))
        XCTAssertFalse(PathStyle.posix.sameName("Report.HTML", "report.html"))
    }

    func testPartialTransferExplainsFailures() throws {
        let partial = try XCTUnwrap(PreviewFixtures.transfers().first { $0.transferID == "t-preview-partial" })
        let row = TransferPresentation(partial, hostName: "개발 PC")
        XCTAssertEqual(row.headline, "일부 완료")
        XCTAssertEqual(row.tone, .warning)
        XCTAssertEqual(row.explanation, "24개 중 22개를 보냈고 2개는 실패했습니다. 실패한 파일은 아래 목록에 이유가 있습니다.")
        let running = try XCTUnwrap(PreviewFixtures.transfers().first { $0.transferID == "t-preview-running" })
        let progress = TransferPresentation(running, hostName: "개발 PC")
        XCTAssertEqual(progress.route, "이 Mac → 개발 PC")
        XCTAssertNotNil(progress.rateText)
        XCTAssertEqual(progress.etaText?.hasSuffix("남음"), true)
        XCTAssertEqual(progress.actions, [.pause, .cancel])
    }

    func testColumnWidthsPersist() {
        let defaults = UserDefaults(suiteName: "files-columns-\(UUID().uuidString)")!
        let preferences = FilesPreferences(defaults: defaults)
        preferences.setColumnWidth(212, .modified, side: .remote)
        preferences.setColumn(.kind, hidden: true, side: .local)
        let reopened = FilesPreferences(defaults: defaults)
        XCTAssertEqual(reopened.columnWidth(.modified, side: .remote), 212)
        XCTAssertNil(reopened.columnWidth(.modified, side: .local))
        XCTAssertTrue(reopened.isColumnHidden(.kind, side: .local))
        XCTAssertFalse(reopened.isColumnHidden(.kind, side: .remote))
        XCTAssertEqual(reopened.conflictPolicy, .fail)
    }

    // MARK: Fix round 1

    func testLiveFilesListSendsCursorAndLimitAndPages() async throws {
        let daemon = MockDaemon()
        try daemon.start()
        defer { daemon.stop() }
        daemon.setHandler { request in
            guard request.method == "files.list" else { return nil }
            let cursor = request.params["cursor"] as? String
            func entry(_ n: Int) -> String {
                #"{"name":"f\#(n).txt","path":"C:\\d\\f\#(n).txt","dir":false,"size":\#(n),"mtime":"2026-10-20T10:00:00.000Z","link":false}"#
            }
            if cursor == nil {
                return .result(#"{"entries":[\#(entry(1)),\#(entry(2))],"next_cursor":"c-2","total":3}"#)
            }
            return .result(#"{"entries":[\#(entry(3))],"total":3,"truncated":true}"#)
        }
        let client = DeskhandClient(socketPath: daemon.path)
        try await client.connect(kind: "app", name: "test", version: "0")
        defer { Task { await client.close() } }
        let backend = LiveBackend(client: client)

        let first = try await backend.filesList(hostID: dev, path: "C:\\d", cursor: nil, limit: 2)
        XCTAssertEqual(first.entries.map(\.name), ["f1.txt", "f2.txt"])
        XCTAssertEqual(first.nextCursor, "c-2")
        XCTAssertEqual(first.total, 3)
        XCTAssertFalse(first.truncated)
        let request = try XCTUnwrap(daemon.requests(method: "files.list").first)
        XCTAssertEqual(request.params["host_id"] as? String, dev)
        XCTAssertEqual(request.params["path"] as? String, "C:\\d")
        XCTAssertEqual(request.params["limit"] as? Int, 2)
        XCTAssertNil(request.params["cursor"])

        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: dev, style: .windows), path: "C:\\d")
        await browser.open("C:\\d")
        XCTAssertEqual(browser.items.count, 2)
        XCTAssertTrue(browser.hasMore)
        await browser.loadMore()
        XCTAssertEqual(browser.items.map(\.name), ["f1.txt", "f2.txt", "f3.txt"])
        XCTAssertTrue(browser.truncated)
        XCTAssertFalse(browser.hasMore)
        let pages = daemon.requests(method: "files.list").dropFirst()
        XCTAssertEqual(pages.map { $0.params["cursor"] as? String }, [nil, "c-2"])
        XCTAssertEqual(pages.map { $0.params["limit"] as? Int }, [500, 500])

        _ = try await backend.filesList(hostID: dev, path: "C:\\d", cursor: nil, limit: 90_000)
        XCTAssertEqual(daemon.requests(method: "files.list").last?.params["limit"] as? Int, 5000)
    }

    func testReloadKeepsRevealedRowsAndSelection() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let browser = FileBrowser(side: .remote, source: RemoteFileSource(backend: backend, hostID: dev, style: .windows),
                                  path: PreviewFixtures.largeFolder)
        await browser.open(PreviewFixtures.largeFolder)
        await browser.loadMore()
        await browser.loadMore()
        XCTAssertEqual(browser.rows.count, 1500)
        let deep = PreviewFixtures.largeFolder + "\\capture-1400.png"
        browser.selection = [deep]
        await browser.reload()
        XCTAssertEqual(browser.rows.count, 1500)
        XCTAssertEqual(browser.selection, [deep])
        XCTAssertEqual(browser.phase, .loaded)
    }

    func testConflictPromptsQueuePerRequest() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        await model.send([try localFile("report.html")], to: downloads, move: false)
        await model.send([try localFile("Shop-build-1187.zip")], to: downloads, move: false)
        XCTAssertEqual(model.conflicts.count, 2)
        XCTAssertEqual(model.conflict?.items.map(\.candidate.name), ["report.html"])

        await model.resolveConflict(.fail, applyToAll: false)
        XCTAssertEqual(model.conflict?.items.map(\.candidate.name), ["Shop-build-1187.zip"])
        await model.resolveConflict(.skip, applyToAll: false)
        XCTAssertNil(model.conflict)
        XCTAssertEqual(backend.transferRequests.map(\.onConflict), [.skip])
    }

    func testMenuMoveWaitsForConfirmation() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        let model = try model(store)
        await model.open(.remote, path: downloads)
        let url = try localFile("이동할 빌드.zip")

        await model.requestSend([url], to: downloads, move: true)
        let request = try XCTUnwrap(model.pendingMove)
        XCTAssertEqual(request.direction, .movePush)
        XCTAssertTrue(backend.transferRequests.isEmpty)

        await model.confirmMove(request, dontAskAgain: true)
        XCTAssertNil(model.pendingMove)
        XCTAssertEqual(backend.transferRequests.map(\.direction), [.movePush])
        XCTAssertFalse(store.filesPreferences.confirmMoves)

        await model.requestSend([try localFile("다음 빌드.zip")], to: downloads, move: true)
        XCTAssertNil(model.pendingMove)
        XCTAssertEqual(backend.transferRequests.map(\.direction), [.movePush, .movePush])

        await model.requestSend([try localFile("복사.zip")], to: downloads, move: false)
        XCTAssertEqual(backend.transferRequests.last?.direction, .push)
    }

    private func transfer(_ state: TransferState, bytes: Int, rev: Int = 1, id: String = "t-rev") -> Transfer {
        Transfer(transferID: id, hostID: dev, direction: "push", local: "/a/b.zip", remote: "C:\\b.zip", onConflict: "fail",
                 state: state.rawValue, bytesDone: bytes, bytesTotal: 100, filesDone: 0, filesTotal: 1, rateBps: 10, rev: rev)
    }

    func testTransferUpdatesApplyByRev() throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let model = try model(makeStore(backend))
        let queue = model.transfers
        queue.apply(transfer(.running, bytes: 50, rev: 4))
        queue.apply(transfer(.paused, bytes: 50, rev: 5))
        queue.apply(transfer(.running, bytes: 50, rev: 6))
        XCTAssertEqual(queue.transfer("t-rev")?.transferState, .running, "resume from paused with the same bytes applies")
        XCTAssertEqual(queue.transfer("t-rev")?.rev, 6)

        queue.apply(transfer(.paused, bytes: 50, rev: 5))
        XCTAssertEqual(queue.transfer("t-rev")?.transferState, .running, "an older rev arriving late is ignored")
        queue.apply(transfer(.cancelled, bytes: 70, rev: 6))
        XCTAssertEqual(queue.transfer("t-rev")?.transferState, .running, "an equal rev is ignored")

        queue.apply(transfer(.failed, bytes: 80, rev: 7))
        queue.apply(transfer(.queued, bytes: 0, rev: 8))
        XCTAssertEqual(queue.transfer("t-rev")?.transferState, .queued, "a retry restarting at 0 bytes applies")
        XCTAssertEqual(queue.transfer("t-rev")?.bytesDone, 0)
    }

    func testPreviewTransferRevsIncrease() async throws {
        let base = PreviewBackend(stepDelay: .zero)
        let url = try localFile("rev.bin", bytes: 4_096)
        var revs: [Int] = []
        let events = base.events(kinds: [EventKind.transferUpdated], hostID: dev)
        let id = try await base.transferStart(TransferRequest(hostID: dev, direction: .push, local: url.path,
                                                              remote: downloads + "\\rev.bin", onConflict: .fail))
        for try await event in events {
            guard let value = event.payload["transfer"], let t = try? LiveBackend.decode(Transfer.self, from: value),
                  t.transferID == id else { continue }
            revs.append(t.rev)
            if t.transferState.isFinished { break }
        }
        XCTAssertGreaterThan(revs.count, 2)
        XCTAssertEqual(revs, revs.sorted())
        XCTAssertEqual(Set(revs).count, revs.count)
        let paused = try await base.transferPause(transferID: id)
        let stored = try await base.transferGet(transferID: id)
        XCTAssertEqual(paused.rev, stored.rev)
        XCTAssertEqual(stored.rev, revs.last)
    }

    func testUndecodableTransferEventIsCounted() throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let model = try model(makeStore(backend))
        model.transfers.handle(DaemonEvent(seq: 9, hostID: dev, kind: EventKind.transferUpdated,
                                           payload: ["transfer": .object(["transfer_id": .string("t")])]))
        XCTAssertEqual(model.transfers.undecodedEvents, 1)
        XCTAssertTrue(model.transfers.transfers.isEmpty)
    }

    func testTransferErrorShowsMessageAndFixByCode() throws {
        var failed = transfer(.failed, bytes: 0)
        failed.outcome = TransferOutcome.failed.rawValue
        failed.error = EnvelopeError(code: "DISK_FULL", message: "대상 디스크 공간이 부족합니다")
        let row = TransferPresentation(failed, hostName: "개발 PC")
        XCTAssertEqual(row.explanation, "대상 디스크 공간이 부족합니다")
        XCTAssertEqual(row.fix, OperationError(code: "DISK_FULL", message: "").fix)
        XCTAssertNotNil(row.fix)
        let partial = try XCTUnwrap(PreviewFixtures.transfers().first { $0.transferID == "t-preview-partial" })
        XCTAssertEqual(TransferPresentation(partial, hostName: "개발 PC").fix, OperationError(code: "PERMISSION_DENIED", message: "").fix)
    }

    func testLaunchReportsProgressAndInlineError() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let model = try model(makeStore(backend))
        await model.open(.remote, path: "C:\\ProgramData\\Shop\\CrashDumps")
        let dump = try XCTUnwrap(model.remote.rows.first)
        await model.launch(dump)
        XCTAssertNil(model.busyMessage)
        XCTAssertEqual(model.paneErrors[.remote]?.code, "INVALID_ARGUMENT")
        XCTAssertNil(model.notice)

        await model.open(.remote, path: "C:\\Shop\\bin")
        let exe = try XCTUnwrap(model.remote.item(named: "Shop.exe"))
        await model.launch(exe)
        XCTAssertNil(model.paneErrors[.remote])
        XCTAssertEqual(model.notice?.tone, .success)
        XCTAssertTrue(model.notice?.message.contains("PID") == true)
    }

    func testRemoteReloadsWhenHostReturns() async throws {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = makeStore(backend)
        var host = try XCTUnwrap(store.host(dev))
        let model = store.files(for: host)
        await model.start(host: host)
        try await waitUntil { model.remote.phase == .loaded }
        let calls = backend.filesListCalls.count
        host.state = ConnectionState.reconnecting.rawValue
        await model.start(host: host)
        host.state = ConnectionState.ready.rawValue
        await model.start(host: host)
        XCTAssertGreaterThan(backend.filesListCalls.count, calls)
    }
}
