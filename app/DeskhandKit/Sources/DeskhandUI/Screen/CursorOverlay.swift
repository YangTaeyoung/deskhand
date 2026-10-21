import AppKit
import QuartzCore

/// The remote cursor drawn locally over the video: the shape the agent reported, one image
/// pixel per device pixel, with its hot spot on the given point.
@MainActor
final class CursorOverlay {
    let layer = CALayer()
    private var hotSpot = CGPoint.zero
    private var pixelSize = CGSize.zero

    init() {
        layer.isHidden = true
        layer.zPosition = 1
        layer.contentsGravity = .resize
        layer.magnificationFilter = .nearest
        layer.actions = ["position": NSNull(), "bounds": NSNull(), "contents": NSNull(), "hidden": NSNull(),
                         "anchorPoint": NSNull(), "contentsScale": NSNull()]
    }

    /// Sets the shape, its hot spot in image pixels from the top left, and the device pixel scale.
    func setImage(_ image: CGImage?, hotSpot: CGPoint, pixelSize: CGSize, backingScale: CGFloat) {
        layer.contents = image
        self.hotSpot = hotSpot
        self.pixelSize = pixelSize
        guard image != nil, pixelSize.width > 0, pixelSize.height > 0 else {
            layer.isHidden = true
            return
        }
        layer.contentsScale = backingScale
        layer.bounds = CGRect(x: 0, y: 0, width: pixelSize.width / backingScale, height: pixelSize.height / backingScale)
        layer.anchorPoint = CGPoint(x: hotSpot.x / pixelSize.width, y: 1 - hotSpot.y / pixelSize.height)
    }

    /// Places the hot spot at `point` in the host layer's coordinates (origin bottom left), or hides the cursor.
    func show(at point: CGPoint?) {
        guard let point, layer.contents != nil else {
            layer.isHidden = true
            return
        }
        layer.position = point
        layer.isHidden = false
    }
}
