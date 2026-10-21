import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// 파일: the download folder, each computer's work roots, what a transfer does with a name that
/// exists and whether moves ask first, and how long finished transfers stay in the queue.
struct FilesSettings: View {
    let store: AppStore
    @Bindable var model: SettingsModel
    @State private var draft = ""
    @State private var adding = false
    @State private var selectedRoot: String?
    @State private var problem: WorkRootProblem?
    @FocusState private var draftFocused: Bool

    private var preferences: FilesPreferences { store.filesPreferences }

    var body: some View {
        @Bindable var preferences = store.filesPreferences
        SettingsForm(store: store, pane: .files) {
            downloadSection
            rootsSection
            Section {
                Picker(String.ui("settings.files.conflict", "같은 이름이 있을 때"), selection: $preferences.conflictPolicy) {
                    ForEach(ConflictPolicy.allCases, id: \.self) { policy in
                        Text(policy.settingsTitle).tag(policy)
                    }
                }
                .pickerStyle(.menu)
                SettingsToggle(String.ui("settings.files.confirmMoves", "메뉴와 툴바로 옮기기 전에 묻기"),
                               detail: String.ui("settings.files.confirmMoves.detail", "옮기기는 확인을 마친 뒤 원본을 지웁니다. ⌘를 누른 채 끌어 옮길 때는 묻지 않습니다."),
                               isOn: $preferences.confirmMoves)
            } header: {
                Text(String.ui("settings.files.transfers.header", "전송"))
            }
            Section {
                Picker(selection: $preferences.finishedTransferRetention) {
                    ForEach(TransferRetention.allCases) { retention in
                        Text(retention.title).tag(retention)
                    }
                } label: {
                    SettingsLabel(String.ui("settings.files.retention", "끝난 전송을 큐에 남겨 두기"),
                                  detail: String.ui("settings.files.retention.detail", "기간이 지난 전송은 전송 큐에서만 사라집니다. 받거나 보낸 파일은 그대로 남습니다."))
                }
                .pickerStyle(.menu)
                LabeledContent {
                    Button(String.ui("settings.files.artifacts.reveal", "Finder에서 보기")) {
                        FinderReveal.reveal(path: Paths.artifacts)
                    }
                    .help(String.ui("settings.files.artifacts.help", "데몬이 산출물을 보관하는 폴더를 엽니다"))
                } label: {
                    SettingsLabel(String.ui("settings.files.artifacts", "산출물"),
                                  detail: String.ui("settings.files.artifacts.detail", "스크린샷, QA 증거와 저장한 진단 결과는 데몬 폴더에 보관됩니다."))
                }
            } header: {
                Text(String.ui("settings.files.retention.header", "보존"))
            }
        }
    }

    // MARK: Download folder

    private var downloadSection: some View {
        Section {
            LabeledContent(String.ui("settings.files.download", "받을 폴더")) {
                HStack(spacing: DS.Space.s) {
                    SettingsValue((preferences.downloadFolder as NSString).abbreviatingWithTildeInPath, monospaced: true)
                    Button(String.ui("settings.files.download.choose", "변경…"), action: chooseDownloadFolder)
                        .help(String.ui("settings.files.download.choose.help", "받은 로그와 크래시 덤프를 저장할 폴더를 고릅니다"))
                    if preferences.customDownloadFolder != nil {
                        Button(String.ui("settings.files.download.reset", "기본 폴더")) {
                            preferences.customDownloadFolder = nil
                        }
                        .help(String.ui("settings.files.download.reset.help", "~/Downloads/Deskhand로 되돌립니다"))
                    }
                }
            }
        } header: {
            Text(String.ui("settings.files.download.header", "다운로드"))
        } footer: {
            SettingsFootnote(String.ui("settings.files.download.footer", "로그 탭에서 받는 로그 파일과 크래시 덤프를 이 폴더에 저장합니다. 같은 이름이 있으면 새 이름으로 저장합니다."))
        }
    }

    /// Asks for the folder in a sheet on the settings window, or in its own panel without one.
    private func chooseDownloadFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = String.ui("settings.files.download.prompt", "선택")
        panel.directoryURL = URL(fileURLWithPath: preferences.downloadFolder).deletingLastPathComponent()
        let preferences = self.preferences
        let apply: (NSApplication.ModalResponse) -> Void = { response in
            guard response == .OK, let url = panel.url else { return }
            preferences.customDownloadFolder = url.path == FilesPreferences.defaultDownloadFolder ? nil : url.path
        }
        if let window = NSApp.keyWindow {
            panel.beginSheetModal(for: window, completionHandler: apply)
        } else {
            apply(panel.runModal())
        }
    }

    // MARK: Work roots

    @ViewBuilder
    private var rootsSection: some View {
        if let host = model.host(model.scopeHostID) {
            let roots = preferences.workRoots(for: host.hostID)
            let style = PathStyle(os: host.os)
            Section {
                SettingsHostPicker(hosts: store.hosts, selection: Binding(get: { host.hostID }, set: {
                    model.scopeHostID = $0
                    cancelAdding()
                }))
                VStack(alignment: .leading, spacing: 0) {
                    List(selection: $selectedRoot) {
                        ForEach(roots, id: \.self) { root in
                            Label {
                                Text(root).font(DS.Font.mono)
                            } icon: {
                                Image(systemName: "folder")
                            }
                            .tag(root)
                        }
                    }
                    .listStyle(.bordered(alternatesRowBackgrounds: false))
                    .frame(height: 88)
                    .overlay {
                        if roots.isEmpty {
                            Text(String.ui("settings.files.roots.empty", "작업 루트가 없습니다"))
                                .font(DS.Font.caption)
                                .foregroundStyle(DS.Status.neutralText)
                        }
                    }
                    .accessibilityLabel(String.ui("settings.files.roots.list", "작업 루트 목록"))
                    HStack(spacing: 0) {
                        Button {
                            adding = true
                            draftFocused = true
                        } label: {
                            Image(systemName: "plus")
                                .frame(width: 24, height: 20)
                        }
                        .help(String.ui("settings.files.roots.add.help", "작업 루트 추가"))
                        .accessibilityLabel(String.ui("settings.files.roots.add.help", "작업 루트 추가"))
                        Divider().frame(height: 16)
                        Button {
                            if let selectedRoot { preferences.removeWorkRoot(selectedRoot, for: host.hostID) }
                            selectedRoot = nil
                        } label: {
                            Image(systemName: "minus")
                                .frame(width: 24, height: 20)
                        }
                        .disabled(selectedRoot.map { !roots.contains($0) } ?? true)
                        .help(String.ui("settings.files.roots.remove", "선택한 작업 루트 빼기"))
                        .accessibilityLabel(String.ui("settings.files.roots.remove", "선택한 작업 루트 빼기"))
                        Spacer(minLength: 0)
                    }
                    .buttonStyle(.borderless)
                    .padding(.top, DS.Space.xs)
                }
                if adding {
                    addRow(host: host, style: style)
                }
            } header: {
                Text(String.ui("settings.files.roots.header", "작업 루트"))
            } footer: {
                SettingsFootnote(String.ui("settings.files.roots.footer", "작업 루트는 파일 탭의 ‘자주 쓰는 위치’에 나타나고, 개인정보·AI에서 AI가 쓸 수 있는 폴더로 고를 수 있습니다."))
            }
        } else {
            Section {
                Text(String.ui("settings.files.roots.noHosts", "PC를 추가하면 PC마다 작업 루트를 정할 수 있습니다."))
                    .foregroundStyle(DS.Status.neutralText)
            } header: {
                Text(String.ui("settings.files.roots.header", "작업 루트"))
            }
        }
    }

    private func addRow(host: Host, style: PathStyle) -> some View {
        let example = style == .windows ? "C:\\Work\\Project" : "/srv/project"
        return VStack(alignment: .leading, spacing: DS.Space.xs) {
            HStack(spacing: DS.Space.s) {
                TextField(String.ui("settings.files.roots.field", "새 작업 루트"), text: $draft,
                          prompt: Text(String.ui("settings.files.roots.prompt", "예: \(example)")))
                    .font(DS.Font.mono)
                    .textFieldStyle(.roundedBorder)
                    .labelsHidden()
                    .focused($draftFocused)
                    .onSubmit { add(host: host, style: style) }
                    .onChange(of: draft) { problem = nil }
                    .onExitCommand { cancelAdding() }
                Button(String.ui("action.cancel", "취소")) { cancelAdding() }
                Button(String.ui("settings.files.roots.add", "추가")) { add(host: host, style: style) }
                    .disabled(draft.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            if let problem {
                SettingsError(message: problem.message)
            }
        }
    }

    private func add(host: Host, style: PathStyle) {
        problem = preferences.addWorkRoot(draft, for: host.hostID, style: style)
        if problem == nil { cancelAdding() }
    }

    private func cancelAdding() {
        adding = false
        draft = ""
        problem = nil
    }
}

extension ConflictPolicy {
    /// Name in the settings window.
    var settingsTitle: String {
        switch self {
        case .fail: .ui("settings.files.conflict.ask", "매번 묻기")
        case .replace: .ui("conflict.replace", "바꾸기")
        case .skip: .ui("conflict.skip", "건너뛰기")
        case .rename: .ui("conflict.keepBoth", "둘 다 유지")
        }
    }
}
