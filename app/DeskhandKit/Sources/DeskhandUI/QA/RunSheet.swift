import AppKit
import DeskhandClient
import SwiftUI
import UniformTypeIdentifiers
import struct DeskhandClient.Host

/// Starts a QA run: the scenario file of this Mac (chosen in an open panel or from recent ones),
/// the computer, variables (names starting with `secret_` take a hidden value that is never kept)
/// and the time limit. The daemon checks the scenario before it starts; a problem shows with its
/// file and line.
public struct RunSheet: View {
    let store: AppStore
    let model: QAModel
    let dismiss: () -> Void
    @State private var draft: QARunDraft
    @State private var error: OperationError?
    @State private var starting = false

    public init(store: AppStore, model: QAModel, draft: QARunDraft, error: OperationError? = nil, starting: Bool = false,
                dismiss: @escaping () -> Void) {
        self.store = store
        self.model = model
        self.dismiss = dismiss
        _draft = State(initialValue: draft)
        _error = State(initialValue: error)
        _starting = State(initialValue: starting)
    }

    private var host: Host? { store.host(draft.hostID) }
    private var running: QARun? { model.runningRun(hostID: draft.hostID) }
    private var canStart: Bool { draft.request != nil && !starting && running == nil }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(String.ui("qa.sheet.title", "QA 시나리오 실행"))
                    .font(DS.Font.title)
                Text(String.ui("qa.sheet.subtitle", "이 Mac의 시나리오 YAML을 PC에서 실행하고 단계별 결과와 증거, 보고서를 남깁니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.l) {
                GridRow {
                    label(String.ui("qa.sheet.scenario", "시나리오 파일"))
                    scenarioField
                }
                GridRow {
                    label(String.ui("qa.sheet.host", "실행할 PC"))
                    hostField
                }
                GridRow {
                    label(String.ui("qa.sheet.vars", "변수"))
                    VarsEditor(vars: $draft.vars)
                }
                GridRow {
                    label(String.ui("qa.sheet.timeout", "실행 제한 시간"))
                    VStack(alignment: .leading, spacing: DS.Space.xs) {
                        Picker(String.ui("qa.sheet.timeout", "실행 제한 시간"), selection: $draft.timeout) {
                            ForEach(QATimeout.choices, id: \.self) { choice in
                                Text(choice.title).tag(choice)
                            }
                        }
                        .labelsHidden()
                        .fixedSize()
                        Text(String.ui("qa.sheet.timeout.hint", "준비·단계에만 걸립니다. 정리 단계는 제한과 상관없이 끝까지 실행합니다."))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            if let error {
                errorView(error)
            }
            HStack(spacing: DS.Space.m) {
                if starting {
                    ProgressView().controlSize(.small)
                    Text(String.ui("qa.sheet.starting", "시나리오를 검사하고 실행을 시작하는 중…"))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: dismiss)
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("qa.sheet.run", "실행")) { start() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(!canStart)
            }
        }
        .padding(DS.Space.xl)
        .frame(width: 620)
    }

    private func label(_ text: String) -> some View {
        Text(text)
            .font(DS.Font.body)
            .foregroundStyle(.secondary)
            .gridColumnAlignment(.trailing)
            .frame(width: 96, alignment: .trailing)
    }

    // MARK: Scenario

    private var scenarioField: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            HStack(spacing: DS.Space.s) {
                TextField(String.ui("qa.sheet.scenario.placeholder", "/Users/…/scenario.yaml"), text: $draft.scenarioPath)
                    .textFieldStyle(.roundedBorder)
                    .font(DS.Font.mono)
                    .onChange(of: draft.scenarioPath) { _, _ in error = nil }
                    .accessibilityLabel(String.ui("qa.sheet.scenario", "시나리오 파일"))
                Button(String.ui("qa.sheet.choose", "선택…")) { choose() }
                    .help(String.ui("qa.sheet.choose.help", "이 Mac에서 시나리오 YAML 파일을 고릅니다"))
                if !model.recentScenarios.isEmpty {
                    Menu {
                        ForEach(model.recentScenarios) { recent in
                            Button(recent.path.abbreviatingHome) {
                                draft = model.draft(from: recent, hostID: draft.hostID)
                                error = nil
                            }
                        }
                    } label: {
                        Image(systemName: "clock.arrow.circlepath")
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                    .help(String.ui("qa.sheet.recent", "최근 시나리오"))
                    .accessibilityLabel(String.ui("qa.sheet.recent", "최근 시나리오"))
                }
            }
            Text(String.ui("qa.sheet.scenario.hint", "push의 상대 경로는 시나리오 파일이 있는 폴더를 기준으로 합니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.allowedContentTypes = [.yaml]
        panel.prompt = String.ui("qa.sheet.choose.prompt", "고르기")
        panel.message = String.ui("qa.sheet.choose.message", "실행할 QA 시나리오(.yaml)를 고르세요")
        if !draft.scenarioPath.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: draft.scenarioPath).deletingLastPathComponent()
        }
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task { @MainActor in
                let recent = model.recentScenarios.first { $0.path == url.path }
                draft = recent.map { model.draft(from: $0, hostID: draft.hostID) } ?? QARunDraft(scenarioPath: url.path, hostID: draft.hostID,
                                                                                               vars: draft.vars, timeout: draft.timeout)
                error = nil
            }
        }
    }

    // MARK: Host

    private var hostField: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            Picker(String.ui("qa.sheet.host", "실행할 PC"), selection: $draft.hostID) {
                ForEach(store.hosts, id: \.hostID) { host in
                    Text(host.name + " · " + host.connection.title).tag(host.hostID)
                }
            }
            .labelsHidden()
            .fixedSize()
            .onChange(of: draft.hostID) { _, _ in error = nil }
            if let host {
                hostNotes(host)
            }
        }
    }

    @ViewBuilder
    private func hostNotes(_ host: Host) -> some View {
        if let running {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                Image(systemName: "hourglass")
                    .foregroundStyle(DS.Status.accentText)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text(String.ui("qa.sheet.busy", "이 PC에서 ‘\(running.name)’ 실행이 진행 중입니다. 끝난 뒤 실행할 수 있습니다."))
                        .font(DS.Font.caption)
                        .fixedSize(horizontal: false, vertical: true)
                    Button(String.ui("qa.sheet.busy.show", "진행 중인 실행 보기")) { show(running) }
                        .buttonStyle(.link)
                        .tint(DS.Color.accent)
                        .font(DS.Font.caption)
                }
            }
        }
        if host.connection != .ready, host.connection != .degraded {
            note("bolt.horizontal.circle", DS.Status.warningText,
                 String.ui("qa.sheet.offline", "\(host.name)에 연결되어 있지 않습니다. 실행은 시작하지만 PC가 필요한 단계는 ‘차단’으로 끝납니다."))
        } else if host.controlOwner == .human {
            note("person.fill", DS.Color.human,
                 String.ui("qa.sheet.human", "지금 사람이 조작 중입니다. 앱 실행·UI 동작·창 닫기 단계는 PC에 보내지 않고 ‘차단’으로 남깁니다."))
        }
    }

    private func note(_ symbol: String, _ tint: Color, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: symbol)
                .foregroundStyle(tint)
                .accessibilityHidden(true)
            Text(text)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }

    private func show(_ run: QARun) {
        model.selection[.all] = run.runID
        model.selection[.host(run.hostID)] = run.runID
        if store.selectedHostID != run.hostID { store.selectedHostID = run.hostID }
        dismiss()
    }

    // MARK: Errors

    @ViewBuilder
    private func errorView(_ error: OperationError) -> some View {
        if error.code == "INVALID_ARGUMENT", let problem = QAScenarioProblem(message: error.message, path: draft.scenarioPath) {
            ScenarioProblemBox(problem: problem) { start() }
        } else if error.code == "IN_PROGRESS" {
            ErrorBanner(message: String.ui("qa.sheet.inProgress", "이 PC에서 다른 QA 실행이 진행 중입니다"), fix: error.message, code: error.code)
        } else if error.code == "NOT_FOUND" {
            ErrorBanner(message: String.ui("qa.sheet.notFound", "시나리오 파일이나 PC를 찾을 수 없습니다"), fix: error.message, code: error.code)
        } else {
            ErrorBanner(message: String.ui("qa.sheet.failed", "실행을 시작하지 못했습니다"), fix: error.detail, code: error.code)
        }
    }

    private func start() {
        guard canStart else { return }
        starting = true
        error = nil
        let draft = draft
        Task {
            let failure = await model.start(draft)
            starting = false
            if let failure {
                error = failure
                return
            }
            if store.selectedHostID != draft.hostID { store.selectedHostID = draft.hostID }
            dismiss()
        }
    }
}

/// A scenario problem with its file and line, the daemon's sentence, and buttons to open the file
/// in its editor and to check it again.
struct ScenarioProblemBox: View {
    let problem: QAScenarioProblem
    let retry: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Image(systemName: "exclamationmark.octagon.fill")
                .foregroundStyle(DS.Color.danger)
                .font(DS.Font.headline)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.s) {
                Text(String.ui("qa.problem.title", "시나리오 \(String(problem.line))번째 줄을 고쳐야 실행할 수 있습니다"))
                    .font(DS.Font.headline)
                HStack(spacing: DS.Space.xs) {
                    Image(systemName: "doc.text")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    Text((problem.file as NSString).lastPathComponent + ":" + String(problem.line))
                        .font(DS.Font.mono)
                        .textSelection(.enabled)
                }
                .help(problem.file)
                Text(problem.message)
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                ErrorCodeLine(code: "INVALID_ARGUMENT")
                HStack(spacing: DS.Space.s) {
                    if problem.file.hasPrefix("/") {
                        Button(String.ui("qa.problem.edit", "편집기에서 열기")) {
                            NSWorkspace.shared.open(URL(fileURLWithPath: problem.file))
                        }
                    }
                    Button(String.ui("qa.problem.retry", "고친 뒤 다시 검사"), action: retry)
                }
                .padding(.top, DS.Space.xs)
            }
            Spacer(minLength: 0)
        }
        .padding(DS.Space.m)
        .background(DS.Color.danger.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(DS.Color.danger.opacity(0.28), lineWidth: 1))
        .accessibilityElement(children: .contain)
    }
}

/// Variable rows: a name and a value, the value hidden for names starting with `secret_`; an
/// issue line under a bad or repeated name; a button to add a row.
struct VarsEditor: View {
    @Binding var vars: QAVarsDraft

    static func text(_ issue: QAVarsDraft.Issue) -> String {
        switch issue {
        case .invalidName: .ui("qa.vars.invalid", "영문자·숫자·_만 쓰고 숫자로 시작하지 마세요")
        case .repeated: .ui("qa.vars.repeated", "같은 이름의 변수가 위에 있습니다")
        case .missingSecret: .ui("qa.vars.missingSecret", "비밀값을 입력하세요. 비밀값은 다음 실행을 위해 저장하지 않습니다")
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            ForEach($vars.rows) { $row in
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: DS.Space.s) {
                        TextField(String.ui("qa.vars.name", "이름"), text: $row.name)
                            .textFieldStyle(.roundedBorder)
                            .font(DS.Font.mono)
                            .frame(width: 168)
                            .accessibilityLabel(String.ui("qa.vars.name", "이름"))
                        Group {
                            if row.isSecret {
                                SecureField(String.ui("qa.vars.secret", "비밀값"), text: $row.value)
                            } else {
                                TextField(String.ui("qa.vars.value", "값"), text: $row.value)
                            }
                        }
                        .textFieldStyle(.roundedBorder)
                        .font(DS.Font.mono)
                        .accessibilityLabel(row.isSecret ? String.ui("qa.vars.secret.a11y", "\(row.trimmedName) 비밀값") : String.ui("qa.vars.value.a11y", "\(row.trimmedName) 값"))
                        Image(systemName: row.isSecret ? "lock.fill" : "lock.open")
                            .font(.system(size: 11))
                            .foregroundStyle(row.isSecret ? AnyShapeStyle(DS.Status.accentText) : AnyShapeStyle(.quaternary))
                            .frame(width: 14)
                            .help(row.isSecret ? String.ui("qa.vars.masked.help", "결과와 보고서에 ***로 남고 다음 실행을 위해 저장하지 않습니다")
                                               : String.ui("qa.vars.plain.help", "이름이 secret_로 시작하면 값을 가립니다"))
                            .accessibilityHidden(true)
                        Button {
                            vars.rows.removeAll { $0.id == row.id }
                        } label: {
                            Image(systemName: "minus.circle")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                        .help(String.ui("qa.vars.remove", "변수 지우기"))
                        .accessibilityLabel(String.ui("qa.vars.remove", "변수 지우기"))
                    }
                    if let issue = vars.issue(for: row) {
                        Text(Self.text(issue))
                            .font(DS.Font.caption)
                            .foregroundStyle(DS.Status.dangerText)
                    }
                }
            }
            HStack(spacing: DS.Space.m) {
                Button {
                    vars.rows.append(QAVarRow())
                } label: {
                    Label(String.ui("qa.vars.add", "변수 추가"), systemImage: "plus")
                }
                Text(String.ui("qa.vars.hint", "시나리오의 vars를 덮어씁니다. secret_로 시작하는 이름은 값을 가리고 결과에 ***로 남깁니다."))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
