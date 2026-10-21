import DeskhandUI
import SwiftUI

@main
struct DeskhandApp: App {
    @State private var store = AppLaunch.makeStore()

    var body: some Scene {
        Window("Deskhand", id: "main") {
            RootView(store: store)
        }
        .defaultSize(width: 1280, height: 800)
        .commands {
            DeskhandCommands(store: store)
        }
        Settings {
            SettingsView(store: store)
        }
    }
}
