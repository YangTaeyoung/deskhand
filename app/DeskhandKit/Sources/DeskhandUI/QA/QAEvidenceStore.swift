import AppKit
import DeskhandClient
import Foundation
import ImageIO
import Observation

/// An evidence file as the QA tab has read it.
public enum QAEvidenceContent: Equatable {
    case loading
    /// An image and the pixel size of the file it came from.
    case image(NSImage, pixels: CGSize)
    /// Text with JSON laid out one member per line; `truncated` when the file was longer than read.
    case text(String, truncated: Bool)
    case failed(OperationError)

    public static func == (lhs: QAEvidenceContent, rhs: QAEvidenceContent) -> Bool {
        switch (lhs, rhs) {
        case (.loading, .loading): true
        case (.image(let a, let pa), .image(let b, let pb)): a === b && pa == pb
        case (.text(let a, let ta), .text(let b, let tb)): a == b && ta == tb
        case (.failed(let a), .failed(let b)): a == b
        default: false
        }
    }
}

/// Evidence files of QA runs: thumbnails downsampled from the image files, full images and texts
/// for the viewer, read and decoded off the main thread and kept until they take more than
/// `byteLimit`, the least recently used dropped first.
@MainActor
@Observable
public final class QAEvidenceStore {
    public enum Size: Hashable, Sendable {
        case thumbnail
        case full
    }

    struct Key: Hashable {
        var path: String
        var size: Size
    }

    /// Longest side of a thumbnail in pixels: twice the thumbnail's points.
    public nonisolated static let thumbnailPixels = 480
    /// Longest side of an image the viewer shows.
    public nonisolated static let fullPixels = 3200
    /// Largest image file read.
    public nonisolated static let imageLimit = 32 << 20
    /// Largest part of a text file read.
    public nonisolated static let textLimit = 512 << 10
    /// Decoded bytes kept by default.
    public static let defaultByteLimit = 192 << 20

    private var contents: [Key: QAEvidenceContent] = [:]
    @ObservationIgnored private var costs: [Key: Int] = [:]
    @ObservationIgnored private var order: [Key] = []
    @ObservationIgnored private(set) var total = 0
    @ObservationIgnored private var loading: Set<Key> = []
    @ObservationIgnored private let backend: any QABackend
    @ObservationIgnored let byteLimit: Int

    public init(backend: any QABackend, byteLimit: Int = QAEvidenceStore.defaultByteLimit) {
        self.backend = backend
        self.byteLimit = byteLimit
    }

    nonisolated static func isImage(_ artifact: Artifact) -> Bool {
        artifact.kind == "screenshot" || artifact.mime.hasPrefix("image/")
    }

    public func content(of artifact: Artifact, size: Size) -> QAEvidenceContent? {
        let key = Key(path: artifact.path, size: Self.isImage(artifact) ? size : .full)
        return contents[key]
    }

    /// Reads and decodes an evidence file once for `size`; a failed read is tried again.
    public func load(_ artifact: Artifact, size: Size) {
        let image = Self.isImage(artifact)
        let key = Key(path: artifact.path, size: image ? size : .full)
        if let content = contents[key] {
            guard case .failed = content else {
                touch(key)
                return
            }
        }
        guard !loading.contains(key) else { return }
        loading.insert(key)
        contents[key] = .loading
        let backend = backend
        let path = artifact.path
        let json = artifact.mime.contains("json") || path.hasSuffix(".json")
        Task { [weak self] in
            let content: QAEvidenceContent
            let cost: Int
            do {
                let data = try await backend.evidenceData(path: path, maxBytes: (image ? Self.imageLimit : Self.textLimit) + 1)
                if image {
                    let pixels = key.size == .thumbnail ? Self.thumbnailPixels : Self.fullPixels
                    let decoded = await Task.detached(priority: .userInitiated) { Self.decodeImage(data, maxPixels: pixels) }.value
                    if let decoded {
                        content = .image(NSImage(cgImage: decoded.image, size: .zero), pixels: decoded.pixels)
                        cost = decoded.image.width * decoded.image.height * 4
                    } else {
                        content = .failed(OperationError(message: .ui("qa.evidence.badImage", "이미지를 읽을 수 없습니다")))
                        cost = 0
                    }
                } else {
                    let text = await Task.detached(priority: .userInitiated) { Self.text(data, limit: Self.textLimit, json: json) }.value
                    content = .text(text.text, truncated: text.truncated)
                    cost = text.text.utf8.count * 2
                }
            } catch {
                content = .failed(OperationError(error))
                cost = 0
            }
            self?.store(content, for: key, cost: cost)
        }
    }

    private func store(_ content: QAEvidenceContent, for key: Key, cost: Int) {
        loading.remove(key)
        contents[key] = content
        costs[key] = cost
        total += cost
        touch(key)
        evict()
    }

    private func touch(_ key: Key) {
        if let index = order.firstIndex(of: key) { order.remove(at: index) }
        order.append(key)
    }

    /// Drops the least recently used contents until the rest fit in `byteLimit`; the newest stays.
    private func evict() {
        while total > byteLimit, order.count > 1 {
            let key = order.removeFirst()
            total -= costs[key] ?? 0
            costs[key] = nil
            contents[key] = nil
        }
    }

    /// A decoded image and the pixel size of its file.
    struct Decoded: @unchecked Sendable {
        let image: CGImage
        let pixels: CGSize
    }

    /// The image of `data` with its longest side at most `maxPixels`.
    nonisolated static func decodeImage(_ data: Data, maxPixels: Int) -> Decoded? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = properties[kCGImagePropertyPixelWidth] as? Int,
              let height = properties[kCGImagePropertyPixelHeight] as? Int else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: min(maxPixels, max(width, height)),
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return Decoded(image: image, pixels: CGSize(width: width, height: height))
    }

    /// The text of `data` up to `limit` bytes; JSON that was read whole is laid out by `QAFormat.prettyJSON`.
    nonisolated static func text(_ data: Data, limit: Int, json: Bool) -> (text: String, truncated: Bool) {
        let truncated = data.count > limit
        let text = String(decoding: truncated ? data.prefix(limit) : data, as: UTF8.self)
        if json, !truncated, let pretty = QAFormat.prettyJSON(text) { return (pretty, false) }
        return (text, truncated)
    }

    /// Puts contents in place for both sizes, keyed by path. Snapshot scenes use it.
    public func seed(_ seeded: [String: QAEvidenceContent]) {
        contents = [:]
        for (path, content) in seeded {
            contents[Key(path: path, size: .full)] = content
            contents[Key(path: path, size: .thumbnail)] = content
        }
    }
}
