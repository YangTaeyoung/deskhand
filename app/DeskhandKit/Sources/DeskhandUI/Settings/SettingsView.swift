import SwiftUI

/// The settings window (⌘,): 일반, 연결, 스트리밍, 키보드, 파일 and 개인정보·AI as toolbar tabs.
/// Every pane edits the same values as the menus and tabs of the main window.
public struct SettingsView: View {
    let store: AppStore
    @Bindable var model: SettingsModel

    public init(store: AppStore) {
        self.store = store
        self.model = store.settings
    }

    public var body: some View {
        TabView(selection: $model.pane) {
            ForEach(SettingsPane.allCases) { pane in
                SettingsPaneContent(store: store, pane: pane)
                    .tabItem { Label(pane.title, systemImage: pane.symbol) }
                    .tag(pane)
            }
        }
        .task { await store.start() }
    }
}

/// The content of one settings pane.
public struct SettingsPaneContent: View {
    let store: AppStore
    let pane: SettingsPane

    public init(store: AppStore, pane: SettingsPane) {
        self.store = store
        self.pane = pane
    }

    public var body: some View {
        switch pane {
        case .general: GeneralSettings(store: store, model: store.settings)
        case .connection: ConnectionSettings(store: store, model: store.settings)
        case .streaming: StreamingSettings(store: store, preferences: store.screenPreferences)
        case .keyboard: KeyboardSettings(store: store, screen: store.screenPreferences, terminal: store.terminalPreferences)
        case .files: FilesSettings(store: store, model: store.settings)
        case .privacy: PrivacySettings(store: store, model: store.settings)
        }
    }
}
