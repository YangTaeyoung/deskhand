import AppKit
import SwiftUI

/// Hosts the terminal surfaces of every tab in one view, showing the selected one. Hidden tabs
/// have the same frame as the shown one.
struct TerminalStack: NSViewRepresentable {
    let sessions: [TerminalSession]
    let selectedID: TerminalSession.ID?
    let focusRequest: Int
    let hostName: String
    /// ⌥ sends ESC before the key in every surface.
    let optionAsMeta: Bool

    func makeNSView(context: Context) -> TerminalStackView {
        TerminalStackView()
    }

    func updateNSView(_ view: TerminalStackView, context: Context) {
        view.update(sessions: sessions, selectedID: selectedID, focusRequest: focusRequest, hostName: hostName,
                    optionAsMeta: optionAsMeta)
    }

    static func dismantleNSView(_ view: TerminalStackView, coordinator: ()) {
        view.releaseSurfaces()
    }
}

/// The container behind `TerminalStack`. It paints the terminal background, surfaces sit
/// inside a small margin, and the selected one takes keyboard focus when it changes or when the
/// workspace asks for focus.
final class TerminalStackView: NSView {
    /// Space between the tab content edge and the terminal text.
    static let margin = NSEdgeInsets(top: 8, left: 12, bottom: 8, right: 4)

    private var surfaces: [TerminalSession.ID: TerminalSurfaceView] = [:]
    private var selectedID: TerminalSession.ID?
    private var focusRequest: Int?

    override var isFlipped: Bool { true }

    /// Fills the terminal background and draws the selected stand-in surface, which stays out of
    /// the view hierarchy, as an image.
    override func draw(_ dirtyRect: NSRect) {
        DS.Terminal.palette(for: effectiveAppearance).background.setFill()
        dirtyRect.fill()
        guard let selectedID, let surface = surfaces[selectedID], surface.isStandIn else { return }
        if surface.appearance?.name != effectiveAppearance.name { surface.appearance = effectiveAppearance }
        guard let image = surface.renderedImage() else { return }
        image.draw(in: surface.frame, from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        for surface in surfaces.values where surface.isStandIn {
            surface.appearance = effectiveAppearance
        }
        needsDisplay = true
    }

    /// Smallest frame given to the surfaces; while the container is smaller, the surfaces keep
    /// their last frame.
    static let minimumSurfaceSize = CGSize(width: 160, height: 64)

    private var contentRect: CGRect? {
        let m = Self.margin
        let rect = CGRect(x: m.left, y: m.top, width: bounds.width - m.left - m.right, height: bounds.height - m.top - m.bottom)
        guard rect.width >= Self.minimumSurfaceSize.width, rect.height >= Self.minimumSurfaceSize.height else { return nil }
        return rect
    }

    func update(sessions: [TerminalSession], selectedID: TerminalSession.ID?, focusRequest: Int, hostName: String,
                optionAsMeta: Bool) {
        let ids = Set(sessions.map(\.id))
        for (id, surface) in surfaces where !ids.contains(id) {
            if surface.superview === self { surface.removeFromSuperview() }
            surfaces[id] = nil
        }
        for session in sessions {
            let surface = session.surface
            if surface.isStandIn {
                if let rect = contentRect { surface.frame = rect }
                surface.appearance = effectiveAppearance
            } else if surface.superview !== self {
                addSubview(surface)
                if let rect = contentRect { surface.frame = rect }
            }
            surfaces[session.id] = surface
            surface.isHidden = session.id != selectedID
            surface.optionAsMetaKey = optionAsMeta
            surface.setAccessibilityLabel(String.ui("terminal.a11y.surface", "\(hostName)의 \(session.title) 터미널"))
        }
        let selectionChanged = selectedID != self.selectedID
        self.selectedID = selectedID
        if sessions.contains(where: \.surface.isStandIn) { needsDisplay = true }
        if selectionChanged || focusRequest != self.focusRequest {
            self.focusRequest = focusRequest
            focusSelected()
        }
    }

    /// Gives up the surfaces this view still holds, when SwiftUI removes it.
    func releaseSurfaces() {
        for surface in surfaces.values where surface.superview === self {
            surface.removeFromSuperview()
        }
        surfaces = [:]
    }

    override func layout() {
        super.layout()
        guard let rect = contentRect else { return }
        for surface in surfaces.values where (surface.superview === self || surface.isStandIn) && surface.frame != rect {
            surface.frame = rect
        }
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        register()
        focusSelected()
    }

    private func focusSelected() {
        guard let selectedID, let surface = surfaces[selectedID] else { return }
        DispatchQueue.main.async { [weak surface] in
            guard let surface, !surface.isHidden, let window = surface.window, window.firstResponder !== surface else { return }
            window.makeFirstResponder(surface)
        }
    }
}
