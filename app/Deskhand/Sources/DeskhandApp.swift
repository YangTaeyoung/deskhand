import DeskhandUI
import SwiftUI

@main
struct DeskhandApp: App {
    var body: some Scene {
        WindowGroup {
            DS.Color.surface
                .frame(minWidth: 960, minHeight: 600)
        }
        Settings {
            DS.Color.surface
                .frame(width: 520, height: 360)
        }
    }
}
