import AppKit
import SwiftUI

/// Answers the standard Edit › 찾기 › 찾기… item (⌘F) for the main window when no text view
/// takes it: it sits in the responder chain right after the window and turns the item into a
/// 찾기 request, which focuses the visible tab's search field. The item is enabled only while the
/// visible tab has a search field; the other find items stay disabled.
final class FindCommandResponder: NSResponder, NSMenuItemValidation {
    private weak var store: AppStore?

    init(store: AppStore) {
        self.store = store
        super.init()
    }

    required init?(coder: NSCoder) { nil }

    /// Puts the responder between `window` and the responder that followed it, once.
    func install(in window: NSWindow) {
        guard window.nextResponder !== self else { return }
        nextResponder = window.nextResponder
        window.nextResponder = self
    }

    @objc override func performTextFinderAction(_ sender: Any?) {
        find(sender)
    }

    @objc func performFindPanelAction(_ sender: Any?) {
        find(sender)
    }

    private func find(_ sender: Any?) {
        guard Self.isShowFind(sender), let store, canFind(store) else { return }
        store.layout.requestFind()
    }

    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        guard item.action == #selector(performTextFinderAction(_:)) || item.action == #selector(performFindPanelAction(_:)) else {
            return true
        }
        guard Self.isShowFind(item), let store else { return false }
        return canFind(store)
    }

    /// The sender is the 찾기… item (`showFindInterface`, the same tag as `showFindPanel`).
    private static func isShowFind(_ sender: Any?) -> Bool {
        (sender as? NSValidatedUserInterfaceItem)?.tag == NSTextFinder.Action.showFindInterface.rawValue
    }

    private func canFind(_ store: AppStore) -> Bool {
        store.selectedHost != nil && WorkspaceLayout.canFind(store.layout.tab)
    }
}

/// Installs a `FindCommandResponder` for `store` in the window of the view it backs.
struct FindCommandInstaller: NSViewRepresentable {
    let store: AppStore

    func makeNSView(context: Context) -> InstallerView {
        InstallerView(responder: FindCommandResponder(store: store))
    }

    func updateNSView(_ view: InstallerView, context: Context) {}

    final class InstallerView: NSView {
        let responder: FindCommandResponder

        init(responder: FindCommandResponder) {
            self.responder = responder
            super.init(frame: .zero)
        }

        required init?(coder: NSCoder) { nil }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            if let window { responder.install(in: window) }
        }
    }
}
