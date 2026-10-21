import AppKit
import DeskhandUI
import SwiftUI

enum SnapshotAppearance: String, CaseIterable {
    case light
    case dark

    var name: NSAppearance.Name {
        switch self {
        case .light: .aqua
        case .dark: .darkAqua
        }
    }
}

enum SnapshotError: Error, CustomStringConvertible {
    case noBitmap(scene: String)
    case incompleteRender(scene: String, appearance: SnapshotAppearance, coverage: Double)
    case encodingFailed(scene: String)

    var description: String {
        switch self {
        case .noBitmap(let scene):
            "\(scene): 비트맵을 만들 수 없습니다"
        case .incompleteRender(let scene, let appearance, let coverage):
            "\(scene) (\(appearance.rawValue)): 화면의 \(Int(coverage * 100))%만 그려졌습니다. "
                + "glassEffect·.glass 버튼처럼 창 서버가 합성하는 효과는 오프스크린 렌더링에 나오지 않으니 장면에서 빼세요"
        case .encodingFailed(let scene):
            "\(scene): PNG로 인코딩할 수 없습니다"
        }
    }
}

/// Renders scenes offscreen: an `NSHostingView` in a borderless sRGB window placed off every
/// display, drawn with `cacheDisplay(in:to:)` and written as sRGB, whatever the displays' color
/// spaces. Nothing is read back from the screen.
@MainActor
enum SnapshotRenderer {
    private static let offscreenOrigin = CGPoint(x: -32_000, y: -32_000)
    private static let settleInterval: TimeInterval = 0.3
    private static let requiredCoverage = 0.99

    static func pngData(for scene: SnapshotScene, appearance: SnapshotAppearance) throws -> Data {
        let window = NSWindow(
            contentRect: CGRect(origin: offscreenOrigin, size: scene.size),
            styleMask: [.borderless],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        window.colorSpace = .sRGB
        window.appearance = NSAppearance(named: appearance.name)
        defer { window.contentView = nil }

        let host = NSHostingView(rootView: SnapshotRoot(content: scene.view(), size: scene.size))
        host.frame = CGRect(origin: .zero, size: scene.size)
        window.contentView = host
        host.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date(timeIntervalSinceNow: settleInterval))
        host.layoutSubtreeIfNeeded()

        guard let cached = host.bitmapImageRepForCachingDisplay(in: host.bounds) else {
            throw SnapshotError.noBitmap(scene: scene.name)
        }
        host.cacheDisplay(in: host.bounds, to: cached)
        guard let bitmap = cached.converting(to: .sRGB, renderingIntent: .default) else {
            throw SnapshotError.noBitmap(scene: scene.name)
        }

        let coverage = opaqueCoverage(of: bitmap)
        guard coverage >= requiredCoverage else {
            throw SnapshotError.incompleteRender(scene: scene.name, appearance: appearance, coverage: coverage)
        }
        guard let png = bitmap.representation(using: .png, properties: [:]) else {
            throw SnapshotError.encodingFailed(scene: scene.name)
        }
        return png
    }

    /// Fraction of sampled pixels that are opaque. `SnapshotRoot` paints an opaque background,
    /// so anything below 1 means part of the view tree did not draw.
    private static func opaqueCoverage(of bitmap: NSBitmapImageRep) -> Double {
        let step = 8
        var opaque = 0
        var total = 0
        for y in stride(from: 0, to: bitmap.pixelsHigh, by: step) {
            for x in stride(from: 0, to: bitmap.pixelsWide, by: step) {
                total += 1
                if let alpha = bitmap.colorAt(x: x, y: y)?.alphaComponent, alpha > 0.98 {
                    opaque += 1
                }
            }
        }
        return total == 0 ? 0 : Double(opaque) / Double(total)
    }
}

/// Wraps a scene in the environment the app runs with: window background, the app accent,
/// an active (key) window and the Korean locale.
private struct SnapshotRoot: View {
    let content: AnyView
    let size: CGSize

    var body: some View {
        content
            .frame(width: size.width, height: size.height)
            .background(DS.Color.surface)
            .accentColor(DS.Color.accent)
            .environment(\.controlActiveState, .key)
            .environment(\.locale, Locale(identifier: "ko_KR"))
    }
}
