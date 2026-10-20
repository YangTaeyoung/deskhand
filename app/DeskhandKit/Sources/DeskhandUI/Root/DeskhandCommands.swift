import DeskhandClient
import SwiftUI

/// Menu bar commands: add a computer (⌘N) and the 컴퓨터 menu for the selected computer.
public struct DeskhandCommands: Commands {
    let store: AppStore

    public init(store: AppStore) {
        self.store = store
    }

    public var body: some Commands {
        CommandGroup(replacing: .newItem) {
            Button(String.ui("menu.addHost", "컴퓨터 추가…")) { store.presentAddHost() }
                .keyboardShortcut("n")
                .disabled(store.phase != .ready)
        }
        CommandMenu(String.ui("menu.computer", "컴퓨터")) {
            if let host = store.selectedHost {
                Button(String.ui("action.doctor", "진단 실행")) { store.presentDoctor(for: host.hostID) }
                    .keyboardShortcut("d", modifiers: [.command, .shift])
                switch host.connection {
                case .offline:
                    Button(String.ui("action.connect", "연결")) { store.connect(host.hostID) }
                case .blocked:
                    Button(String.ui("action.repair", "다시 페어링")) { store.repair(host.hostID) }
                case .ready, .degraded, .connecting, .reconnecting:
                    Button(String.ui("action.disconnect", "연결 끊기")) { store.disconnect(host.hostID) }
                }
                Divider()
                Button(String.ui("action.remove", "제거…")) { store.pendingRemoval = host }
                    .keyboardShortcut(.delete, modifiers: [.command])
            } else {
                Button(String.ui("action.doctor", "진단 실행")) {}
                    .disabled(true)
            }
        }
    }
}
