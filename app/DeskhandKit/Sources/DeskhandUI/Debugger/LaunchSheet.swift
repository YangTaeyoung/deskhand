import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// What the launch sheet holds before it launches; a recent launch also brings its breakpoints
/// and exception setting.
public struct LaunchDraft: Equatable, Sendable {
    public var path: String
    public var arguments: String
    public var cwd: String
    public var pauseOnStart: Bool
    public var breakpoints: [BreakpointSpec]
    public var pauseOnExceptions: ExceptionPauseMode?

    public init(path: String = "", arguments: String = "", cwd: String = "", pauseOnStart: Bool = false, breakpoints: [BreakpointSpec] = [],
                pauseOnExceptions: ExceptionPauseMode? = nil) {
        self.path = path
        self.arguments = arguments
        self.cwd = cwd
        self.pauseOnStart = pauseOnStart
        self.breakpoints = breakpoints
        self.pauseOnExceptions = pauseOnExceptions
    }

    /// The draft of a recent launch.
    public init(_ recent: RecentLaunch) {
        self.init(path: recent.path, arguments: ArgumentSplitter.join(recent.args), cwd: recent.cwd ?? "", pauseOnStart: recent.pauseOnStart,
                  breakpoints: recent.breakpoints, pauseOnExceptions: recent.pauseOnExceptions)
    }

    /// The launch request for `hostID`.
    public func request(hostID: String) -> DebugLaunchRequest {
        let path = path.trimmingCharacters(in: .whitespaces)
        let folder = cwd.trimmingCharacters(in: .whitespaces)
        return DebugLaunchRequest(hostID: hostID, path: path, args: ArgumentSplitter.split(arguments), cwd: folder.isEmpty ? nil : folder,
                                  pauseOnStart: pauseOnStart, breakpoints: breakpoints, pauseOnExceptions: pauseOnExceptions)
    }
}

/// Launches an Electron app for debugging: its remote path (typed, picked from the computer's
/// folders or from recent launches), arguments, working folder and whether to stop on the main
/// process's first line. It stays open while the launch runs and shows why one failed.
public struct LaunchSheet: View {
    let model: DebuggerModel
    let host: Host
    let files: any FilesBackend
    let dismiss: () -> Void
    @State private var draft: LaunchDraft
    @State private var browser: FileBrowser?
    @State private var error: OperationError?
    private let pickerChoice: String?
    @FocusState private var pathFocused: Bool

    public init(model: DebuggerModel, host: Host, files: any FilesBackend, draft: LaunchDraft = LaunchDraft(), browser: FileBrowser? = nil,
                pickerChoice: String? = nil, error: OperationError? = nil, dismiss: @escaping () -> Void) {
        self.model = model
        self.host = host
        self.files = files
        self.dismiss = dismiss
        self.pickerChoice = pickerChoice
        _draft = State(initialValue: draft)
        _browser = State(initialValue: browser)
        _error = State(initialValue: error)
    }

    private var launching: Bool { model.starting?.kind == .launch }

    private var trimmedPath: String { draft.path.trimmingCharacters(in: .whitespaces) }

    private var looksExecutable: Bool { trimmedPath.isEmpty || trimmedPath.lowercased().hasSuffix(".exe") }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            header
            if let browser {
                RemoteFilePicker(browser: browser, host: host, chosen: pickerChoice) { path in
                    draft.path = path
                    self.browser = nil
                } cancel: {
                    self.browser = nil
                }
            } else {
                form
                buttons
            }
        }
        .padding(DS.Space.xl)
        .frame(width: 580)
    }

    private var header: some View {
        HStack(alignment: .top, spacing: DS.Space.m) {
            Image(systemName: "play.rectangle")
                .font(.system(size: 26))
                .foregroundStyle(DS.Color.accent)
                .frame(width: 36)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(browser == nil ? String.ui("debugger.launch.title", "Electron 앱 디버그 실행")
                                    : String.ui("debugger.picker.title", "실행 파일 고르기"))
                    .font(DS.Font.title)
                    .accessibilityAddTraits(.isHeader)
                Text(browser == nil
                     ? String.ui("debugger.launch.subtitle", "\(host.name)에서 디버그 포트를 연 채로 앱을 실행합니다.\n메인 프로세스와 렌더러에 자동으로 연결합니다.")
                     : String.ui("debugger.picker.subtitle", "\(host.name)의 폴더에서 Electron 앱의 .exe 파일을 고르세요."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.m) {
                GridRow {
                    label(String.ui("debugger.launch.path", "실행 파일"))
                    VStack(alignment: .leading, spacing: DS.Space.xs) {
                        HStack(spacing: DS.Space.s) {
                            TextField(String.ui("debugger.launch.path.prompt", "C:\\Program Files\\앱\\앱.exe"), text: $draft.path)
                                .font(DS.Font.mono)
                                .textFieldStyle(.roundedBorder)
                                .focused($pathFocused)
                                .accessibilityLabel(String.ui("debugger.launch.path", "실행 파일"))
                            Button(String.ui("debugger.launch.browse", "찾아보기…")) { openPicker() }
                            if !model.recentLaunches.isEmpty {
                                Menu {
                                    ForEach(model.recentLaunches) { recent in
                                        Button(FilePath.lastComponent(recent.path, style: .windows) + "  " + recent.path) {
                                            draft = LaunchDraft(recent)
                                        }
                                    }
                                } label: {
                                    Image(systemName: "clock.arrow.circlepath")
                                }
                                .menuStyle(.button)
                                .fixedSize()
                                .help(String.ui("debugger.launch.recent.help", "최근에 실행한 앱으로 채웁니다"))
                                .accessibilityLabel(String.ui("debugger.recent", "최근 실행"))
                            }
                        }
                        if !looksExecutable {
                            Label(String.ui("debugger.launch.notExe", "Windows 실행 파일(.exe) 경로가 아닙니다."), systemImage: "exclamationmark.triangle.fill")
                                .font(DS.Font.caption)
                                .foregroundStyle(DS.Status.warningText)
                        }
                    }
                }
                GridRow {
                    label(String.ui("debugger.launch.args", "인수"))
                    TextField(String.ui("debugger.launch.args.prompt", "예: --env=staging --lang=ko"), text: $draft.arguments)
                        .font(DS.Font.mono)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityLabel(String.ui("debugger.launch.args", "인수"))
                }
                GridRow {
                    label(String.ui("debugger.launch.cwd", "작업 폴더"))
                    TextField(cwdPrompt, text: $draft.cwd)
                        .font(DS.Font.mono)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityLabel(String.ui("debugger.launch.cwd", "작업 폴더"))
                }
                GridRow {
                    Color.clear.frame(width: 1, height: 1)
                    VStack(alignment: .leading, spacing: DS.Space.xs) {
                        Toggle(String.ui("debugger.launch.pauseOnStart", "메인 프로세스 첫 줄에서 멈춤"), isOn: $draft.pauseOnStart)
                        Text(String.ui("debugger.launch.pauseOnStart.hint", "--inspect 대신 --inspect-brk로 실행해 main.js가 시작하기 전에 멈추고, 그 전에 중단점을 겁니다. 앱 시작 코드를 디버그할 때 켜세요."))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                            .padding(.leading, DS.Space.l + DS.Space.xs)
                    }
                }
                GridRow {
                    label(String.ui("debugger.launch.exceptions", "예외"))
                    Picker(String.ui("debugger.exceptions", "예외에서 멈춤"), selection: Binding(
                        get: { draft.pauseOnExceptions ?? .uncaught }, set: { draft.pauseOnExceptions = $0 })) {
                        ForEach(ExceptionPauseMode.allCases, id: \.self) { mode in
                            Text(DebugFormat.exceptionMode(mode)).tag(mode)
                        }
                    }
                    .labelsHidden()
                    .fixedSize()
                    .help(String.ui("debugger.exceptions.help", "예외가 던져질 때 멈출지 고릅니다"))
                }
                if !draft.breakpoints.isEmpty {
                    GridRow {
                        label(String.ui("debugger.breakpoints", "중단점"))
                        HStack(spacing: DS.Space.s) {
                            BreakpointMarker(enabled: true, resolved: true, width: 20, height: 14)
                            let count = String(draft.breakpoints.count)
                            Text(draft.pauseOnStart
                                 ? String.ui("debugger.launch.breakpoints", "최근 실행의 중단점 \(count)개를 앱이 시작하기 전에 겁니다")
                                 : String.ui("debugger.launch.breakpointsOnAttach", "최근 실행의 중단점 \(count)개를 연결되는 대로 겁니다"))
                                .font(DS.Font.body)
                                .fixedSize(horizontal: false, vertical: true)
                            Spacer(minLength: DS.Space.s)
                            Button(String.ui("debugger.launch.breakpoints.clear", "빼기")) { draft.breakpoints = [] }
                                .controlSize(.small)
                        }
                        .help(draft.breakpoints.map { ScriptURL.fileName($0.pattern) + ":" + String($0.line) }.joined(separator: "\n"))
                    }
                }
            }
            .disabled(launching)
            if launching {
                StatusNotice(symbol: nil, tint: DS.Color.accent,
                             title: String.ui("debugger.launch.running", "\(launchName) 실행하는 중…"),
                             message: String.ui("debugger.launch.running.detail", "디버그 포트가 열리고 메인·렌더러 대상이 나타나기를 기다립니다. 포트마다 최대 20초 기다립니다."))
            } else if let error {
                StatusNotice(symbol: "exclamationmark.octagon.fill", tint: DS.Color.danger,
                             title: String.ui("debugger.launch.failed", "앱을 디버그 모드로 실행하지 못했습니다"),
                             message: [error.message.asSentence, Self.fix(error)].compactMap { $0 }.joined(separator: "\n"), code: error.code)
            } else {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                    Image(systemName: "info.circle.fill")
                        .foregroundStyle(DS.Color.accent)
                        .accessibilityHidden(true)
                    Text(draft.pauseOnStart
                         ? String.ui("debugger.launch.noteBrk", "빈 포트 두 개를 골라 --inspect-brk와 --remote-debugging-port를 붙여 실행합니다. 메인 프로세스에 먼저 연결해 중단점을 건 뒤 렌더러를 기다립니다. 세션을 닫으면 Deskhand가 실행한 이 앱도 종료됩니다.")
                         : String.ui("debugger.launch.note", "빈 포트 두 개를 골라 --inspect와 --remote-debugging-port를 붙여 실행합니다. 연결되기 전에 실행된 코드에서는 멈추지 않습니다. 세션을 닫으면 Deskhand가 실행한 이 앱도 종료됩니다."))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(DS.Space.m)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(DS.Color.accent.opacity(0.07), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
            }
        }
    }

    /// What to do after a failed launch.
    static func fix(_ error: OperationError) -> String? {
        switch error.code {
        case "EXITED_EARLY": .ui("debugger.fix.exitedEarly", "이미 실행 중인 같은 앱이 있으면 새로 띄운 프로세스가 기존 창에 넘기고 바로 끝납니다. 같은 앱을 모두 닫고 다시 실행하거나 ‘실행 중인 앱에 연결…’을 쓰세요.")
        case "TIMEOUT": .ui("debugger.fix.timeout", "Electron이나 Node로 만든 앱인지 확인하세요. WebView2 앱은 이 방법으로 디버그할 수 없습니다.")
        case "NOT_FOUND": .ui("debugger.fix.notFound", "경로를 확인하거나 ‘찾아보기…’로 실행 파일을 고르세요.")
        default: error.fix
        }
    }

    private var launchName: String {
        FilePath.lastComponent(trimmedPath, style: .windows)
    }

    private var cwdPrompt: String {
        if let parent = FilePath.parent(trimmedPath, style: .windows), !trimmedPath.isEmpty {
            return .ui("debugger.launch.cwd.default", "비워 두면 \(parent)")
        }
        return .ui("debugger.launch.cwd.prompt", "실행 파일이 있는 폴더")
    }

    private func label(_ text: String) -> some View {
        Text(text)
            .font(DS.Font.body)
            .foregroundStyle(.secondary)
            .gridColumnAlignment(.trailing)
    }

    private var buttons: some View {
        HStack {
            Spacer()
            Button(String.ui("action.cancel", "취소"), action: dismiss)
                .keyboardShortcut(.cancelAction)
            Button {
                launch()
            } label: {
                Text(String.ui("debugger.launch.run", "실행"))
                    .frame(minWidth: 56)
            }
            .keyboardShortcut(.defaultAction)
            .disabled(trimmedPath.isEmpty || launching)
        }
        .controlSize(.large)
    }

    private func openPicker() {
        let source = RemoteFileSource(backend: files, hostID: host.hostID, style: .windows)
        let start = FilePath.parent(trimmedPath, style: .windows) ?? RemoteFileSource.startPath(for: host)
        let picker = FileBrowser(side: .remote, source: source, path: start)
        browser = picker
        Task { await picker.open(start) }
    }

    private func launch() {
        let request = draft.request(hostID: host.hostID)
        error = nil
        Task {
            if let failure = await model.launch(request) {
                error = failure
            } else {
                dismiss()
            }
        }
    }
}

/// A folder browser of the remote computer for picking an executable: the path with up and
/// places, folders and files (other than `.exe` dimmed), and the chosen file.
struct RemoteFilePicker: View {
    @Bindable var browser: FileBrowser
    let host: Host
    let select: (String) -> Void
    let cancel: () -> Void
    @State private var chosen: String?

    init(browser: FileBrowser, host: Host, chosen: String? = nil, select: @escaping (String) -> Void, cancel: @escaping () -> Void) {
        self.browser = browser
        self.host = host
        self.select = select
        self.cancel = cancel
        _chosen = State(initialValue: chosen)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            HStack(spacing: DS.Space.s) {
                Button {
                    Task { await browser.goUp() }
                } label: {
                    Image(systemName: "arrow.up")
                }
                .disabled(browser.parentPath == nil)
                .help(String.ui("debugger.picker.up", "상위 폴더"))
                .accessibilityLabel(String.ui("debugger.picker.up", "상위 폴더"))
                Text(browser.path)
                    .font(DS.Font.mono)
                    .lineLimit(1)
                    .truncationMode(.head)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
                Menu {
                    ForEach(places) { place in
                        Button(place.title) { Task { await browser.open(place.path) } }
                    }
                } label: {
                    Label(String.ui("debugger.picker.places", "위치"), systemImage: "folder")
                }
                .menuStyle(.button)
                .fixedSize()
            }
            list
                .frame(height: 300)
                .background(DS.Code.background)
                .clipShape(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
            HStack(spacing: DS.Space.s) {
                Text(chosen.map { FilePath.lastComponent($0, style: .windows) } ?? String.ui("debugger.picker.none", ".exe 파일을 고르세요"))
                    .font(chosen == nil ? DS.Font.body : DS.Font.mono)
                    .foregroundStyle(chosen == nil ? .secondary : .primary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button(String.ui("debugger.picker.back", "돌아가기"), action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("debugger.picker.choose", "선택")) { if let chosen { select(chosen) } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(chosen == nil)
            }
            .controlSize(.large)
        }
    }

    private var places: [PathCrumb] {
        var list = RemoteFileSource.places(for: host)
        list.append(PathCrumb(title: "Program Files", path: "C:\\Program Files"))
        if let user = host.session?.user, !user.isEmpty {
            list.append(PathCrumb(title: String.ui("debugger.picker.userPrograms", "사용자 프로그램"),
                                  path: "C:\\Users\\\(user)\\AppData\\Local\\Programs"))
        }
        return list
    }

    @ViewBuilder
    private var list: some View {
        switch browser.phase {
        case .idle, .loading:
            DebugLoadingLine(text: String.ui("debugger.picker.loading", "폴더를 읽는 중…"))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .failed(let error):
            DebugPanePlaceholder(symbol: "folder.badge.questionmark", title: String.ui("debugger.picker.failed", "폴더를 읽지 못했습니다"),
                                 message: error.message)
        case .loaded:
            if browser.rows.isEmpty {
                DebugPanePlaceholder(symbol: "folder", title: String.ui("debugger.picker.empty", "빈 폴더입니다"))
            } else {
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(browser.rows) { item in
                            PickerRow(item: item, chosen: chosen == item.path) { clicks in
                                if item.isFolder {
                                    chosen = nil
                                    Task { await browser.open(item.path) }
                                } else if Self.isExecutable(item) {
                                    let wasChosen = chosen == item.path
                                    chosen = item.path
                                    if clicks >= 2, wasChosen { select(item.path) }
                                }
                            }
                        }
                    }
                    .padding(.vertical, DS.Space.xs)
                }
            }
        }
    }

    static func isExecutable(_ item: FileItem) -> Bool {
        !item.isDirectory && item.name.lowercased().hasSuffix(".exe")
    }
}

/// A folder or file of the picker: a click opens a folder or chooses an executable and a double
/// click on the chosen executable picks it; other files are dimmed and do nothing. `click` gets
/// the click count.
private struct PickerRow: View {
    let item: FileItem
    let chosen: Bool
    let click: (Int) -> Void
    @State private var hovering = false

    var body: some View {
        let usable = item.isFolder || RemoteFilePicker.isExecutable(item)
        Button {
            click(NSApp.currentEvent?.clickCount ?? 1)
        } label: {
            HStack(spacing: DS.Space.s) {
                Image(systemName: item.isFolder ? "folder.fill" : (RemoteFilePicker.isExecutable(item) ? "macwindow" : "doc"))
                    .foregroundStyle(item.isFolder ? AnyShapeStyle(DS.Color.accent.opacity(0.8)) : AnyShapeStyle(.secondary))
                    .frame(width: 20)
                    .accessibilityHidden(true)
                Text(item.name)
                    .font(DS.Font.body)
                    .foregroundStyle(chosen ? AnyShapeStyle(DS.Color.onFill) : (usable ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary)))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: DS.Space.s)
                if !item.isFolder {
                    Text(item.sizeText)
                        .font(DS.Font.caption)
                        .monospacedDigit()
                        .foregroundStyle(chosen ? AnyShapeStyle(DS.Color.onFill.opacity(0.85)) : AnyShapeStyle(.secondary))
                } else {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, DS.Space.m)
            .frame(height: 28)
            .background(chosen ? AnyShapeStyle(DS.Color.accent) : (hovering && usable ? AnyShapeStyle(DS.Color.neutral.opacity(0.1)) : AnyShapeStyle(.clear)))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!usable)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(chosen ? [.isSelected] : [])
        .accessibilityHint(item.isFolder ? String.ui("debugger.picker.openFolder", "폴더 열기") : "")
    }
}
