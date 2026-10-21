import AppKit
import DeskhandClient
import SwiftUI

/// 일반: the daemon's status, versions and files with the log in Finder and restart, the app's
/// version, and opening the app at login.
struct GeneralSettings: View {
    let store: AppStore
    @Bindable var model: SettingsModel

    var body: some View {
        SettingsForm(store: store, pane: .general) {
            daemonSection
            appSection
        }
        .task(id: model.daemonStatus) { await model.refreshDaemonInfo() }
        .onAppear { model.refreshLoginItem() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            model.refreshLoginItem()
        }
        .confirmationDialog(String.ui("settings.daemon.restart.title", "데몬을 다시 시작할까요?"),
                            isPresented: $model.confirmingRestart, titleVisibility: .visible) {
            Button(String.ui("settings.daemon.restart.confirm", "다시 시작")) {
                Task { await model.restartDaemon() }
            }
            Button(String.ui("action.cancel", "취소"), role: .cancel) {}
        } message: {
            Text(String.ui("settings.daemon.restart.message", "실행 중인 명령과 QA 실행은 결과를 알 수 없게 되고, 디버그 세션과 터널이 닫힙니다. 전송과 터미널 셸은 PC에 다시 연결되면 이어집니다."))
        }
    }

    // MARK: Daemon

    private var daemonSection: some View {
        Section {
            LabeledContent(String.ui("settings.daemon.status", "상태")) {
                DaemonStatusLabel(status: model.daemonStatus)
            }
            if case .unavailable(let message) = model.daemonStatus {
                SettingsNotice(symbol: "exclamationmark.triangle.fill", tint: DS.Color.danger,
                               title: String.ui("settings.daemon.unavailable.title", "데몬에 연결할 수 없습니다"),
                               message: message.asSentence)
            }
            LabeledContent(String.ui("settings.daemon.version", "데몬 버전")) {
                SettingsValue(versionText)
            }
            LabeledContent(String.ui("settings.daemon.socket", "소켓")) {
                SettingsValue((info.socketPath as NSString).abbreviatingWithTildeInPath, monospaced: true)
            }
            LabeledContent(String.ui("settings.daemon.log", "로그")) {
                SettingsValue((info.logPath as NSString).abbreviatingWithTildeInPath, monospaced: true)
            }
            LabeledContent(String.ui("settings.daemon.manage", "관리")) {
                HStack(spacing: DS.Space.s) {
                    Button(String.ui("settings.daemon.openLogs", "Finder에서 로그 보기")) {
                        FinderReveal.reveal(path: info.logPath)
                    }
                    .help(String.ui("settings.daemon.openLogs.help", "Finder에서 데몬 로그 파일을 보여 줍니다"))
                    if case .unavailable = model.daemonStatus {
                        Button(String.ui("settings.daemon.start", "데몬 시작")) {
                            Task { await model.startDaemon() }
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(DS.Status.selectionFill)
                        .disabled(model.restart == .running)
                        .help(String.ui("settings.daemon.start.help", "deskhand daemon start를 실행하고 연결합니다"))
                    } else {
                        Button(String.ui("settings.daemon.restart", "다시 시작…")) {
                            model.confirmingRestart = true
                        }
                        .disabled(model.restart == .running || model.daemonStatus == .starting)
                        .help(String.ui("settings.daemon.restart.help", "데몬을 멈췄다가 다시 시작합니다"))
                    }
                }
            }
            switch model.restart {
            case .running:
                SettingsProgress(title: String.ui("settings.daemon.restarting.detail", "데몬을 멈추고 다시 시작하는 중…"))
            case .failed(let error):
                SettingsError(message: String.ui("settings.daemon.restart.failed", "다시 시작하지 못했습니다: \(error.message)"))
            case .idle:
                EmptyView()
            }
        } header: {
            Text(String.ui("settings.daemon.header", "데몬"))
        } footer: {
            SettingsFootnote(String.ui("settings.daemon.footer", "Deskhand는 이 Mac에서 실행되는 데몬을 통해 원격 PC에 연결합니다. 앱을 닫아도 데몬은 연결과 원격 작업을 이어 갑니다."))
        }
    }

    private var info: DaemonInfo { model.daemonInfo ?? DaemonInfo(version: nil, apiVersion: nil) }

    private var versionText: String {
        guard let version = info.version else { return String.ui("settings.daemon.version.unknown", "알 수 없음") }
        guard let api = info.apiVersion else { return version }
        let apiText = String(api)
        return .ui("settings.daemon.version.value", "\(version) · API \(apiText)")
    }

    // MARK: App

    private var appSection: some View {
        Section {
            LabeledContent(String.ui("settings.app.version", "앱 버전")) {
                SettingsValue(model.appVersion)
            }
            SettingsToggle(String.ui("settings.login.toggle", "로그인할 때 Deskhand 열기"),
                           detail: String.ui("settings.login.detail", "앱을 열지 않아도 CLI와 AI 클라이언트는 필요할 때 데몬을 직접 시작합니다."),
                           isOn: Binding(get: { model.launchesAtLogin }, set: { model.setLaunchesAtLogin($0) }))
            if model.loginItemStatus == .requiresApproval {
                SettingsNotice(symbol: "exclamationmark.circle.fill", tint: DS.Color.warning,
                               title: String.ui("settings.login.approval.title", "로그인 항목에서 허용해야 열립니다"),
                               message: String.ui("settings.login.approval.message", "시스템 설정 › 일반 › 로그인 항목에서 Deskhand를 켜세요.")) {
                    Button(String.ui("settings.login.approval.open", "로그인 항목 열기…")) { model.openLoginItemSettings() }
                        .controlSize(.small)
                }
            }
            if let error = model.loginItemError {
                SettingsError(message: String.ui("settings.login.failed", "로그인 항목을 바꾸지 못했습니다: \(error)"))
            }
        } header: {
            Text(String.ui("settings.app.header", "앱"))
        }
    }
}

/// The daemon's status dot and title.
struct DaemonStatusLabel: View {
    let status: DaemonStatus

    var body: some View {
        HStack(spacing: DS.Space.s) {
            StatusDot(state: status.dot)
            Text(status.title)
                .foregroundStyle(DS.Status.neutralText)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("settings.daemon.status.a11y", "데몬 상태: \(status.title)"))
    }
}
