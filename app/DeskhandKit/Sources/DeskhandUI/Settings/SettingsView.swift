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
        case .streaming: StreamingSettings(preferences: store.screenPreferences)
        case .keyboard: KeyboardSettings(screen: store.screenPreferences, terminal: store.terminalPreferences)
        case .files: FilesSettings(store: store, model: store.settings)
        case .privacy: PrivacySettings(store: store, model: store.settings)
        }
    }
}

/// The settings window drawn for offscreen snapshots, where the system toolbar does not
/// render: the window title and the pane tabs as a strip over the pane content.
public struct SettingsWindowStandIn: View {
    let store: AppStore
    let pane: SettingsPane

    public init(store: AppStore, pane: SettingsPane) {
        self.store = store
        self.pane = pane
    }

    public var body: some View {
        VStack(spacing: 0) {
            SettingsToolbarStrip(selection: pane)
            Divider()
            SettingsPaneContent(store: store, pane: pane)
        }
        .frame(width: SettingsMetrics.width)
    }

    /// Window size of the stand-in showing `pane`.
    public static func size(_ pane: SettingsPane) -> CGSize {
        CGSize(width: SettingsMetrics.width, height: pane.height + SettingsToolbarStrip.height + 1)
    }
}

/// The settings window's title and toolbar tabs as a strip: the pane title centered over a row
/// of symbol-and-title tabs, the selected one highlighted.
struct SettingsToolbarStrip: View {
    static let height: CGFloat = 84

    let selection: SettingsPane

    var body: some View {
        VStack(spacing: DS.Space.xs) {
            Text(selection.title)
                .font(DS.Font.headline)
                .frame(height: 24)
            HStack(spacing: DS.Space.xs) {
                ForEach(SettingsPane.allCases) { pane in
                    tab(pane)
                }
            }
        }
        .frame(maxWidth: .infinity)
        .frame(height: Self.height, alignment: .top)
        .padding(.top, DS.Space.xs)
        .background(DS.Color.surfaceRaised.opacity(0.5))
    }

    private func tab(_ pane: SettingsPane) -> some View {
        let selected = pane == selection
        return VStack(spacing: 2) {
            Image(systemName: pane.symbol)
                .font(.system(size: 17, weight: .regular))
                .frame(height: 22)
            Text(pane.title)
                .font(DS.Font.caption)
                .lineLimit(1)
        }
        .foregroundStyle(selected ? AnyShapeStyle(DS.Status.accentText) : AnyShapeStyle(.secondary))
        .frame(minWidth: 64)
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .background(selected ? DS.Color.neutral.opacity(0.16) : .clear,
                    in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : [.isButton])
    }
}
