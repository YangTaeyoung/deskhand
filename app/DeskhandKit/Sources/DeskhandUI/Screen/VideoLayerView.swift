import AVFoundation
import AppKit
import DeskhandMedia
import SwiftUI

/// Layer-hosting view whose root layer holds the display layer and the cursor layer.
@MainActor
final class VideoHostView: NSView {
    let root = CALayer()

    override init(frame: NSRect) {
        super.init(frame: frame)
        root.masksToBounds = true
        layer = root
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported")
    }
}

/// The remote screen: the display layer in the aspect-fit video rect, the remote cursor drawn
/// over it, and an `InputCapture` on top. While input is captured the cursor follows the local
/// pointer; otherwise it sits where the agent reports it.
@MainActor
final class ScreenSurfaceView: NSView {
    let capture = InputCapture(frame: .zero)
    private let host = VideoHostView(frame: .zero)
    private let cursor = CursorOverlay()
    private weak var displayLayer: CALayer?
    private var mapper: PointMapper?
    private var localPointer: CGPoint?
    private var cursorPosition: CursorPosition?
    private var cursorImage: ScreenSession.CursorImage?

    /// The remote display size in its own pixels.
    var remoteSize: CGSize = .zero {
        didSet { if remoteSize != oldValue { needsLayout = true } }
    }

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        addSubview(host)
        addSubview(capture)
        host.root.addSublayer(cursor.layer)
        capture.onPointer = { [weak self] point in
            self?.localPointer = point
            self?.refreshCursor()
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    func attach(displayLayer: CALayer) {
        guard displayLayer !== self.displayLayer else { return }
        self.displayLayer?.removeFromSuperlayer()
        displayLayer.removeFromSuperlayer()
        host.root.insertSublayer(displayLayer, below: cursor.layer)
        self.displayLayer = displayLayer
        needsLayout = true
    }

    override func layout() {
        super.layout()
        host.frame = bounds
        capture.frame = bounds
        let mapper = PointMapper(remote: remoteSize, view: bounds.size, backingScale: window?.backingScaleFactor ?? 2)
        self.mapper = mapper
        capture.mapper = mapper
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        displayLayer?.frame = layerRect(mapper.videoRect)
        CATransaction.commit()
        applyCursorImage()
    }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        needsLayout = true
    }

    /// `rect` in top-left view coordinates converted to the host layer's bottom-left coordinates.
    private func layerRect(_ rect: CGRect) -> CGRect {
        CGRect(x: rect.minX, y: bounds.height - rect.maxY, width: rect.width, height: rect.height)
    }

    /// The remote cursor's position and shape; nil hides it.
    func setCursor(position: CursorPosition?, image: ScreenSession.CursorImage?) {
        cursorPosition = position
        if image != cursorImage {
            cursorImage = image
            applyCursorImage()
        } else {
            refreshCursor()
        }
    }

    private func applyCursorImage() {
        cursor.setImage(cursorImage?.image, hotSpot: cursorImage?.hotSpot ?? .zero, pixelSize: cursorImage?.pixelSize ?? .zero,
                        backingScale: window?.backingScaleFactor ?? 2)
        refreshCursor()
    }

    /// Moves the cursor to the local pointer while captured, otherwise to the agent's position.
    func refreshCursor() {
        let visible = cursorImage != nil && cursorPosition?.visible != false
        capture.drawsRemoteCursor = visible
        guard visible, let mapper else {
            cursor.show(at: nil)
            return
        }
        var point: CGPoint?
        if capture.isCaptured, let localPointer {
            point = localPointer
        } else if let position = cursorPosition {
            let remote = CGPoint(x: CGFloat(position.x), y: CGFloat(position.y))
            if remote.x >= 0, remote.y >= 0, remote.x < mapper.remote.width, remote.y < mapper.remote.height {
                point = mapper.viewPoint(fromRemote: remote)
            }
        }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        cursor.show(at: point.map { CGPoint(x: $0.x, y: bounds.height - $0.y) })
        CATransaction.commit()
    }
}

/// SwiftUI host of a `ScreenSurfaceView` for a `ScreenSession`.
struct VideoLayerView: NSViewRepresentable {
    let session: ScreenSession
    let owner: ControlOwner
    let inputEnabled: Bool
    let display: Int
    let keyMap: KeyMap
    let keyboardMode: KeyboardMode
    let forwardsSystemShortcuts: Bool
    let remoteSize: CGSize
    let accessibilityLabel: String
    let accessibilityHelp: String
    let onTakeoverRequest: () -> Void

    func makeNSView(context: Context) -> ScreenSurfaceView {
        let view = ScreenSurfaceView(frame: .zero)
        session.attach(surface: view)
        return view
    }

    func updateNSView(_ view: ScreenSurfaceView, context: Context) {
        view.remoteSize = remoteSize
        let capture = view.capture
        capture.display = display
        capture.keyMap = keyMap
        capture.keyboardMode = keyboardMode
        capture.forwardsSystemShortcuts = forwardsSystemShortcuts
        capture.onTakeoverRequest = onTakeoverRequest
        capture.isInputEnabled = inputEnabled
        capture.owner = owner
        capture.setAccessibilityLabel(accessibilityLabel)
        capture.setAccessibilityHelp(accessibilityHelp)
    }

    static func dismantleNSView(_ view: ScreenSurfaceView, coordinator: ()) {
        view.capture.endCapture()
    }
}
