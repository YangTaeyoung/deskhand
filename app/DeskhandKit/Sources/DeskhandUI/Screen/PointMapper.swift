import CoreGraphics

/// Maps between a view showing the remote display aspect-fit (letterboxed or pillarboxed) and
/// the remote display's pixels. View coordinates have their origin at the top left.
public struct PointMapper: Equatable, Sendable {
    /// The remote display size in its own pixels (input coordinates, protocol.md §6).
    public let remote: CGSize
    /// The view size in points.
    public let view: CGSize
    /// Device pixels per view point.
    public let backingScale: CGFloat

    /// The rect the video occupies in the view, its origin on a device pixel and its size a
    /// whole number of device pixels.
    public let videoRect: CGRect

    public init(remote: CGSize, view: CGSize, backingScale: CGFloat) {
        self.remote = remote
        self.view = view
        self.backingScale = max(backingScale, 1)
        guard remote.width > 0, remote.height > 0, view.width > 0, view.height > 0 else {
            videoRect = .zero
            return
        }
        let size = view.width * remote.height > view.height * remote.width
            ? CGSize(width: remote.width * view.height / remote.height, height: view.height)
            : CGSize(width: view.width, height: remote.height * view.width / remote.width)
        let scale = self.backingScale
        func pixels(_ value: CGFloat, _ rule: FloatingPointRoundingRule) -> CGFloat {
            (value * scale + 1e-6).rounded(rule) / scale
        }
        videoRect = CGRect(x: pixels((view.width - size.width) / 2, .down), y: pixels((view.height - size.height) / 2, .down),
                           width: pixels(size.width, .toNearestOrAwayFromZero),
                           height: pixels(size.height, .toNearestOrAwayFromZero))
    }

    /// View points per remote pixel.
    public var scale: CGFloat {
        remote.width > 0 ? videoRect.width / remote.width : 0
    }

    /// The remote pixel under `viewPoint`, or nil in the letterbox or outside the view.
    public func remotePoint(from viewPoint: CGPoint) -> CGPoint? {
        guard !videoRect.isEmpty,
              viewPoint.x >= videoRect.minX, viewPoint.x < videoRect.maxX,
              viewPoint.y >= videoRect.minY, viewPoint.y < videoRect.maxY else { return nil }
        return clampedRemotePoint(from: viewPoint)
    }

    /// The remote pixel nearest to `viewPoint`, clamped to the display's edges.
    public func clampedRemotePoint(from viewPoint: CGPoint) -> CGPoint {
        guard !videoRect.isEmpty else { return .zero }
        let x = (viewPoint.x - videoRect.minX) / videoRect.width * remote.width
        let y = (viewPoint.y - videoRect.minY) / videoRect.height * remote.height
        return CGPoint(x: Self.pixel(x, limit: remote.width), y: Self.pixel(y, limit: remote.height))
    }

    /// The view point of the top-left corner of a remote pixel.
    public func viewPoint(fromRemote point: CGPoint) -> CGPoint {
        guard remote.width > 0, remote.height > 0 else { return .zero }
        return CGPoint(x: videoRect.minX + point.x / remote.width * videoRect.width,
                       y: videoRect.minY + point.y / remote.height * videoRect.height)
    }

    /// The view size of a cursor image drawn one image pixel per device pixel.
    public func cursorSize(pixels: CGSize) -> CGSize {
        CGSize(width: pixels.width / backingScale, height: pixels.height / backingScale)
    }

    /// `value` rounded down to a whole pixel in `0..<limit`.
    private static func pixel(_ value: CGFloat, limit: CGFloat) -> CGFloat {
        min(max((value + 1e-6).rounded(.down), 0), max(limit - 1, 0))
    }
}
