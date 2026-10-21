import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// 개인정보·AI: a tinted notice that the AI scope is stored on this Mac and not applied yet, each
/// computer's saved scope (screen captures, folders, the clipboard) marked 적용 전, remote
/// clipboard writes on this Mac, which apply now, and what holds today for administrator rights.
struct PrivacySettings: View {
    let store: AppStore
    @Bindable var model: SettingsModel

    var body: some View {
        SettingsBannerForm(store: store, pane: .privacy) {
            SettingsNotice(symbol: "exclamationmark.shield.fill", tint: DS.Color.warning,
                           title: String.ui("settings.privacy.pending.title", "AI 전달 범위는 아직 적용되지 않습니다"),
                           message: String.ui("settings.privacy.pending.message", "여기서 정한 범위는 이 Mac에 저장됩니다. 데몬이 범위를 적용하기 전까지 AI 클라이언트는 연결된 PC의 화면, 파일과 클립보드를 제한 없이 씁니다. 아래 ‘이 Mac’ 항목은 지금 적용됩니다."),
                           tinted: true)
        } content: {
            scopeSection
            Section {
                @Bindable var terminal = store.terminalPreferences
                SettingsToggle(String.ui("terminal.clipboardWrite", "원격 프로그램의 클립보드 쓰기 허용하기"),
                               detail: String.ui("settings.privacy.osc52.detail", "켜면 터미널에서 실행한 프로그램이 OSC 52로 이 Mac의 클립보드를 바꿀 수 있습니다. AI가 실행한 명령도 포함됩니다."),
                               isOn: $terminal.allowsClipboardWrite)
            } header: {
                Text(String.ui("settings.privacy.mac.header", "이 Mac"))
            }
            Section {
                LabeledContent {
                    SettingsValue(String.ui("settings.privacy.elevation.value", "제한 없음"))
                } label: {
                    SettingsLabel(String.ui("settings.privacy.elevation", "AI가 실행하는 명령의 권한"),
                                  detail: String.ui("settings.privacy.elevation.detail", "지금은 AI가 실행한 명령도 PC 에이전트의 관리자 권한 토큰으로 실행됩니다. 이를 제한하는 기능은 아직 없습니다. AI는 UAC 승인 창이 뜨는 보안 데스크톱에는 입력할 수 없습니다."))
                }
                .accessibilityElement(children: .combine)
            } header: {
                Text(String.ui("settings.privacy.elevation.header", "관리자 권한"))
            }
        }
    }

    @ViewBuilder
    private var scopeSection: some View {
        if let host = model.host(model.scopeHostID) {
            let preferences = store.privacyPreferences
            let roots = store.filesPreferences.workRoots(for: host.hostID)
            let scope = preferences.scope(for: host.hostID)
            let binding = Binding(get: { preferences.scope(for: host.hostID) }, set: { preferences.setScope($0, for: host.hostID) })
            Section {
                SettingsHostPicker(hosts: store.hosts, selection: Binding(get: { host.hostID }, set: { model.scopeHostID = $0 }))
                SettingsToggle(String.ui("settings.privacy.screen", "화면 캡처 허용하기"),
                               detail: String.ui("settings.privacy.screen.detail", "적용되면, 끈 PC의 스크린샷과 화면 조작을 AI에게 막습니다."),
                               isOn: binding.allowsScreen)
                Picker(selection: binding.files) {
                    ForEach(AIFileAccess.allCases) { access in
                        Text(access.title).tag(access)
                    }
                } label: {
                    SettingsLabel(String.ui("settings.privacy.files", "파일"),
                                  detail: String.ui("settings.privacy.files.detail", "적용되면 AI가 읽고 바꿀 수 있는 폴더를 이 범위로 줄입니다."))
                }
                .pickerStyle(.menu)
                if scope.files == .workRoots {
                    workRootsRow(roots: roots, problems: scope.problems(rootCount: roots.count))
                }
                SettingsToggle(String.ui("settings.privacy.clipboard", "클립보드 허용하기"),
                               detail: String.ui("settings.privacy.clipboard.detail", "적용되면, 끈 PC의 클립보드를 AI가 읽거나 쓰지 못합니다."),
                               isOn: binding.allowsClipboard)
            } header: {
                scopeHeader
            } footer: {
                SettingsFootnote(String.ui("settings.privacy.scope.footer", "저장된 범위: \(scope.summary(rootCount: roots.count))"))
            }
        } else {
            Section {
                Text(String.ui("settings.privacy.noHosts", "PC를 추가하면 PC마다 AI에 보낼 범위를 정할 수 있습니다."))
                    .foregroundStyle(DS.Status.neutralText)
            } header: {
                scopeHeader
            }
        }
    }

    private var scopeHeader: some View {
        HStack(spacing: DS.Space.s) {
            Text(String.ui("settings.privacy.scope.header", "AI 전달 범위"))
            SettingsBadge(title: String.ui("settings.privacy.scope.pending", "적용 전"), tint: DS.Color.warning, text: DS.Status.warningText)
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private func workRootsRow(roots: [String], problems: [AIScope.Problem]) -> some View {
        if problems.contains(.noWorkRoots) {
            SettingsNotice(symbol: "exclamationmark.triangle.fill", tint: DS.Color.warning,
                           title: String.ui("settings.privacy.noRoots.title", "작업 루트가 없습니다"),
                           message: String.ui("settings.privacy.noRoots.message", "적용되면 이 PC의 어떤 폴더도 AI에게 허용되지 않습니다. 지금은 AI가 모든 폴더를 씁니다. 파일 설정에서 작업 루트를 추가하세요.")) {
                Button(String.ui("settings.privacy.editRoots", "작업 루트 편집…")) { model.pane = .files }
                    .controlSize(.small)
            }
        } else {
            LabeledContent {
                Button(String.ui("settings.privacy.editRoots", "작업 루트 편집…")) { model.pane = .files }
            } label: {
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    ForEach(roots, id: \.self) { root in
                        SettingsValue(root, monospaced: true)
                    }
                }
            }
        }
    }
}
