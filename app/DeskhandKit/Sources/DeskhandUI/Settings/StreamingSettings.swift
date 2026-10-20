import SwiftUI

/// 스트리밍: the default quality preset, the statistics overlay and the local pointer. The
/// screen tab's menus edit the same values.
struct StreamingSettings: View {
    @Bindable var preferences: ScreenPreferences

    var body: some View {
        SettingsForm(pane: .streaming) {
            Section {
                Picker(String.ui("settings.streaming.preset", "화질"), selection: $preferences.preset) {
                    ForEach(StreamPreset.allCases) { preset in
                        SettingsLabel(preset.title, detail: preset.detail)
                            .tag(preset)
                    }
                }
                .pickerStyle(.radioGroup)
                .labelsHidden()
            } header: {
                Text(String.ui("settings.streaming.preset.header", "기본 화질"))
            } footer: {
                SettingsFootnote(String.ui("settings.streaming.preset.footer", "화면 탭의 화질 메뉴와 같은 값입니다. 바꾸면 열려 있는 화면에도 바로 적용됩니다."))
            }
            Section {
                SettingsToggle(String.ui("settings.streaming.statistics", "스트림 통계 표시"),
                               detail: String.ui("settings.streaming.statistics.detail", "원격 화면 위에 fps, 지연, 비트레이트와 인코더를 표시합니다."),
                               isOn: $preferences.showsStatistics)
                SettingsToggle(String.ui("settings.streaming.localCursor", "입력 중에도 Mac 포인터 보이기"),
                               detail: String.ui("settings.streaming.localCursor.detail", "원격 커서는 지연 없이 이 Mac에서 그립니다. 켜면 그 위에 Mac 화살표도 함께 보여, 원격 커서가 잘 보이지 않을 때 위치를 찾기 쉽습니다."),
                               isOn: $preferences.showsLocalCursor)
            } header: {
                Text(String.ui("settings.streaming.display.header", "표시"))
            }
        }
    }
}
