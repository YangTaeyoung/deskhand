import Foundation

/// Default to one remote pixel per window point, including on Retina screens.
/// Do not supersample by backingScale: that multiplies decoding/copying work.
/// Preserve aspect ratio within a 4K pixel budget for unusually large windows.
public struct RDPViewport: Equatable, Sendable {
    public let width: UInt32
    public let height: UInt32
    public let desktopScale: UInt32

    public init?(width: Double, height: Double, backingScale: Double) {
        guard width.isFinite, height.isFinite, backingScale.isFinite,
              width > 0, height > 0, backingScale > 0 else { return nil }
        let maxSide = max(width, height)
        let ratio = min(1, 3840 / maxSide, sqrt(8_294_400 / maxSide / min(width, height)))
        let w = max(200, min(3840, floor(width * ratio)))
        let h = max(200, min(3840, floor(height * ratio)))
        self.width = UInt32(w) & ~1
        self.height = UInt32(h)
        desktopScale = 100
    }
}
