import SwiftUI

/// A named view rendered to `<name>-light.png` and `<name>-dark.png` at `size` points.
struct SnapshotScene {
    let name: String
    let size: CGSize
    let view: @MainActor () -> AnyView

    init<Content: View>(name: String, size: CGSize, @ViewBuilder view: @escaping @MainActor () -> Content) {
        self.name = name
        self.size = size
        self.view = { AnyView(view()) }
    }
}
