import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// Evidence files: thumbnails downsampled off the main thread, text laid out in the daemon's key
/// order, and the byte budget dropping the least recently used first.
@MainActor
final class QAEvidenceStoreTests: XCTestCase {
    private let preview = PreviewBackend(scenario: .standard, stepDelay: .zero)

    private func evidence() async throws -> [Artifact] {
        let failed = try await preview.qaGet(runID: PreviewQASamples.failedRunID)
        let login = try await preview.qaGet(runID: PreviewQASamples.loginRunID)
        return (failed.steps ?? []).flatMap(\.evidence) + (login.steps ?? []).flatMap(\.evidence)
    }

    func testThumbnailsAreDownsampledAndTextKeepsItsKeyOrder() async throws {
        let store = QAEvidenceStore(backend: preview)
        let files = try await evidence()
        let screenshot = try XCTUnwrap(files.first { $0.kind == "screenshot" })
        let tree = try XCTUnwrap(files.first { $0.kind == "ui_tree" })
        store.load(screenshot, size: .thumbnail)
        store.load(tree, size: .full)
        XCTAssertEqual(store.content(of: screenshot, size: .thumbnail), .loading)
        try await waitUntil { if case .image = store.content(of: screenshot, size: .thumbnail) { true } else { false } }
        guard case .image(let image, let pixels) = store.content(of: screenshot, size: .thumbnail) else { return XCTFail("no thumbnail") }
        XCTAssertEqual(pixels, CGSize(width: 1600, height: 1000))
        XCTAssertEqual(max(image.size.width, image.size.height), CGFloat(QAEvidenceStore.thumbnailPixels))
        try await waitUntil { if case .text = store.content(of: tree, size: .full) { true } else { false } }
        guard case .text(let text, false) = store.content(of: tree, size: .full) else { return XCTFail("no text") }
        XCTAssertTrue(text.hasPrefix("{\n  \"window\": {"))
    }

    func testByteBudgetDropsTheLeastRecentlyUsedFirst() async throws {
        let files = try await evidence().filter { $0.kind == "screenshot" }
        XCTAssertGreaterThanOrEqual(files.count, 2)
        let thumbnail = QAEvidenceStore.thumbnailPixels * QAEvidenceStore.thumbnailPixels * 10 / 16 * 4
        let store = QAEvidenceStore(backend: preview, byteLimit: thumbnail + thumbnail / 2)
        store.load(files[0], size: .thumbnail)
        try await waitUntil { if case .image = store.content(of: files[0], size: .thumbnail) { true } else { false } }
        store.load(files[1], size: .thumbnail)
        try await waitUntil { if case .image = store.content(of: files[1], size: .thumbnail) { true } else { false } }
        XCTAssertNil(store.content(of: files[0], size: .thumbnail), "the older thumbnail is dropped")
        XCTAssertLessThanOrEqual(store.total, store.byteLimit)
    }
}
