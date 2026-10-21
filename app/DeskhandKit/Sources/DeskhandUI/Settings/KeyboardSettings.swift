import SwiftUI

/// 키보드: the input method, ⌘↔Ctrl and the Korean keys, system shortcuts on the remote PC,
/// and the terminal's Option key and paste confirmation. The screen tab's keyboard menu and the
/// 터미널 menu edit the same values.
struct KeyboardSettings: View {
    let store: AppStore
    @Bindable var screen: ScreenPreferences
    @Bindable var terminal: TerminalPreferences

    var body: some View {
        SettingsForm(store: store, pane: .keyboard) {
            Section {
                Picker(String.ui("settings.keyboard.mode", "입력 방식"), selection: $screen.keyboardMode) {
                    ForEach(KeyboardMode.allCases) { mode in
                        SettingsLabel(mode.title, detail: mode.detail.asSentence)
                            .tag(mode)
                    }
                }
                .pickerStyle(.radioGroup)
                .labelsHidden()
            } header: {
                Text(String.ui("settings.keyboard.mode.header", "한글 입력"))
            } footer: {
                SettingsFootnote(String.ui("settings.keyboard.mode.footer", "Windows 입력기를 쓰면 한/영 전환과 조합을 PC가 합니다. Mac 입력기를 쓰면 확정한 글자만 유니코드로 보냅니다."))
            }
            Section {
                SettingsToggle(String.ui("screen.keyboard.swap", "⌘를 Ctrl로, ⌃을 Windows 키로 보내기"),
                               detail: String.ui("settings.keyboard.swap.detail", "⌘C·⌘V처럼 Mac에서 쓰던 손 모양 그대로 Windows 단축키를 씁니다."),
                               isOn: $screen.swapCommandControl)
                SettingsToggle(String.ui("screen.keyboard.hangul", "오른쪽 ⌥을 한/영 키로 보내기"),
                               detail: String.ui("settings.keyboard.hangul.detail", "Windows 입력기의 한/영 전환에 씁니다."),
                               isOn: $screen.rightOptionHangul)
                SettingsToggle(String.ui("screen.keyboard.hanja", "오른쪽 ⌘를 한자 키로 보내기"),
                               detail: String.ui("settings.keyboard.hanja.detail", "Windows 입력기의 한자 변환에 씁니다."),
                               isOn: $screen.rightCommandHanja)
            } header: {
                Text(String.ui("settings.keyboard.keys.header", "키 바꾸기"))
            }
            Section {
                SettingsToggle(String.ui("settings.keyboard.system", "⌘Q·⌘H·⌘Tab도 원격으로 보내기"),
                               detail: String.ui("settings.keyboard.system.detail", "화면 입력 중일 때만 적용됩니다. 끄면 이 Mac의 앱 종료·가리기·앱 전환으로 동작합니다."),
                               isOn: $screen.forwardsSystemShortcuts)
                if screen.forwardsSystemShortcuts, !SystemShortcutTap.isPermitted {
                    SettingsNotice(symbol: "exclamationmark.circle.fill", tint: DS.Color.warning,
                                   title: String.ui("settings.keyboard.system.permission.title", "⌘Tab은 손쉬운 사용 권한이 있어야 보냅니다"),
                                   message: String.ui("settings.keyboard.system.permission.message", "시스템 설정 › 개인정보 보호 및 보안 › 손쉬운 사용에서 Deskhand를 켜세요. ⌘Q와 ⌘H는 권한 없이 보냅니다.")) {
                        Button(String.ui("screen.keyboard.system.openSettings", "손쉬운 사용 설정 열기…")) {
                            SystemSettingsLink.openAccessibility()
                        }
                        .controlSize(.small)
                    }
                }
            } header: {
                Text(String.ui("settings.keyboard.system.header", "시스템 단축키"))
            } footer: {
                SettingsFootnote(String.ui("settings.keyboard.system.footer", "긴급 정지(⇧⌘.), 탭 전환(⌘1–⌘7), 인스펙터(⌥⌘0), 빠른 전환(⌘K)과 설정(⌘,)은 언제나 이 Mac에서 처리합니다. 그 밖의 ⌘ 조합은 ‘⌘를 Ctrl로’ 설정에 따라 PC로 갑니다."))
            }
            Section {
                SettingsToggle(String.ui("terminal.optionAsMeta", "Option을 Meta 키로 쓰기"),
                               detail: String.ui("settings.keyboard.meta.detail", "⌥ 글자 대신 ESC를 먼저 보냅니다. Emacs와 readline 단축키에 씁니다."),
                               isOn: $terminal.optionAsMeta)
                SettingsToggle(String.ui("terminal.paste.confirmSetting", "여러 줄을 붙여 넣기 전에 묻기"),
                               detail: String.ui("settings.keyboard.paste.detail", "줄바꿈이 있는 글을 붙여 넣으면 몇 줄인지 보여 주고 묻습니다."),
                               isOn: $terminal.confirmsMultilinePaste)
            } header: {
                Text(String.ui("settings.keyboard.terminal.header", "터미널"))
            }
        }
    }
}
