import AppKit
import DeskhandClient
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
        XCTAssertTrue(FileManager.default.fileExists(atPath: scratch.appendingPathComponent("shop-2026-10-20.log").path))
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
        XCTAssertEqual(model.notice?.tone, .error)
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
}
