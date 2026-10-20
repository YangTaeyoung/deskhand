import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// 개인정보·AI: what of each computer reaches AI clients (screen captures, folders, the
/// clipboard), remote clipboard writes on this Mac, and the fixed rule that AI clients never
/// act elevated. The scope is kept on this Mac; the notice at the top says the daemon does not
/// apply it yet.
struct PrivacySettings: View {
    let store: AppStore
    @Bindable var model: SettingsModel

    var body: some View {
        SettingsForm(pane: .privacy) {
            Section {
                SettingsNotice(symbol: "exclamationmark.shield.fill", tint: DS.Color.warning,
                               title: String.ui("settings.privacy.pending.title", "AI 전달 범위는 아직 적용되지 않습니다"),
                               message: String.ui("settings.privacy.pending.message", "여기서 정한 범위는 이 Mac에 저장됩니다. 데몬이 AI 요청에 범위를 적용하기 전까지 AI 클라이언트는 연결된 PC의 화면, 파일과 클립보드를 제한 없이 씁니다. 아래 ‘이 Mac’과 ‘관리자 권한’ 항목은 지금 적용됩니다."))
            }
            scopeSection
            Section {
                @Bindable var terminal = store.terminalPreferences
                SettingsToggle(String.ui("terminal.clipboardWrite", "원격 프로그램의 클립보드 쓰기 허용"),
                               detail: String.ui("settings.privacy.osc52.detail", "켜면 터미널에서 실행한 프로그램이 OSC 52로 이 Mac의 클립보드를 바꿀 수 있습니다. AI가 실행한 명령도 포함됩니다."),
                               isOn: $terminal.allowsClipboardWrite)
            } header: {
                Text(String.ui("settings.privacy.mac.header", "이 Mac"))
            }
            Section {
                LabeledContent {
                    Label(String.ui("settings.privacy.elevation.value", "사람만"), systemImage: "lock.fill")
                        .foregroundStyle(.secondary)
                } label: {
                    SettingsLabel(String.ui("settings.privacy.elevation", "UAC 승인과 보안 데스크톱"),
                                  detail: String.ui("settings.privacy.elevation.detail", "UAC 승인 창, 보안 데스크톱과 잠금 화면은 AI가 조작하지 않습니다. Deskhand는 이런 보호를 우회하지 않으며, 권한 승인이 필요한 단계는 사람이 PC에서 직접 승인합니다. 이 규칙은 바꿀 수 없습니다."))
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
                SettingsToggle(String.ui("settings.privacy.screen", "화면 캡처"),
                               detail: String.ui("settings.privacy.screen.detail", "끄면 AI는 이 PC의 스크린샷을 받지 못하고 화면을 보며 조작할 수 없습니다."),
                               isOn: binding.allowsScreen)
                Picker(selection: binding.files) {
                    ForEach(AIFileAccess.allCases) { access in
                        Text(access.title).tag(access)
                    }
                } label: {
                    SettingsLabel(String.ui("settings.privacy.files", "파일"),
                                  detail: String.ui("settings.privacy.files.detail", "AI가 읽고 바꿀 수 있는 폴더입니다."))
                }
                .pickerStyle(.menu)
                if scope.files == .workRoots {
                    workRootsRow(roots: roots, problems: scope.problems(rootCount: roots.count))
                }
                SettingsToggle(String.ui("settings.privacy.clipboard", "클립보드"),
                               detail: String.ui("settings.privacy.clipboard.detail", "AI가 이 PC의 클립보드를 읽고 쓸 수 있습니다."),
                               isOn: binding.allowsClipboard)
            } header: {
                Text(String.ui("settings.privacy.scope.header", "AI 전달 범위"))
            } footer: {
                SettingsFootnote(String.ui("settings.privacy.scope.footer", "지금 범위: \(scope.summary(rootCount: roots.count))"))
            }
        } else {
            Section {
                Text(String.ui("settings.privacy.noHosts", "컴퓨터를 추가하면 컴퓨터마다 AI에 보낼 범위를 정할 수 있습니다."))
                    .foregroundStyle(.secondary)
            } header: {
                Text(String.ui("settings.privacy.scope.header", "AI 전달 범위"))
            }
        }
    }

    @ViewBuilder
    private func workRootsRow(roots: [String], problems: [AIScope.Problem]) -> some View {
        if problems.contains(.noWorkRoots) {
            SettingsNotice(symbol: "exclamationmark.triangle.fill", tint: DS.Color.warning,
                           title: String.ui("settings.privacy.noRoots.title", "작업 루트가 없습니다"),
                           message: String.ui("settings.privacy.noRoots.message", "이대로면 AI는 이 PC의 어떤 폴더도 쓸 수 없습니다. 파일 설정에서 작업 루트를 추가하세요.")) {
                Button(String.ui("settings.privacy.editRoots", "작업 루트 편집…")) { model.pane = .files }
                    .controlSize(.small)
            }
        } else {
            LabeledContent {
                Button(String.ui("settings.privacy.editRoots", "작업 루트 편집…")) { model.pane = .files }
            } label: {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(roots, id: \.self) { root in
                        SettingsPath(path: root)
                    }
                }
            }
        }
    }
}
