import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// A run: the header with its status, verdict, counts, duration, scenario, computer and actions
/// (다시 실행…, 취소 or 정리 단계도 중단, 보고서 열기, 내보내기…); notes for a cancel that came too
/// late, step kinds the computer cannot do and the export; the steps grouped by phase over the
/// selected step.
struct RunDetail: View {
    let store: AppStore
    @Bindable var model: QAModel
    let run: QARun
    /// The tab is narrow: actions show as icons.
    let compact: Bool

    var body: some View {
        let record = model.record(run.runID)
        let steps = record?.steps
        VStack(spacing: 0) {
            RunHeader(store: store, model: model, run: run, steps: steps, compact: compact)
            RunNotes(model: model, run: run)
            Rectangle().fill(DS.Color.separator).frame(height: 1)
            if let steps {
                DebugSplit(axis: .vertical, fraction: $model.stepsSplit, minimumFirst: 150, minimumSecond: 180) {
                    StepList(model: model, run: run, steps: steps)
                } second: {
                    if let step = model.focusedStep(of: run.runID) {
                        StepDetail(model: model, run: run, step: step)
                            .id(run.runID + "/" + String(step.index))
                    } else {
                        DebugPanePlaceholder(symbol: "list.bullet.rectangle", title: String.ui("qa.detail.noStep", "단계를 고르세요"),
                                             message: String.ui("qa.detail.noStep.detail", "기대·실제 값, 증거, 단계 값을 봅니다."))
                    }
                }
            } else {
                stepsPlaceholder
            }
        }
        .onAppear { model.loadSteps(run.runID) }
        .onChange(of: run.runID) { _, id in model.loadSteps(id) }
    }

    @ViewBuilder
    private var stepsPlaceholder: some View {
        switch model.stepLoads[run.runID] {
        case .failed(let error):
            VStack {
                ErrorBanner(message: String.ui("qa.steps.failed", "단계를 불러오지 못했습니다"), fix: error.fix ?? error.message, code: error.code) {
                    Task { await model.refresh(run.runID) }
                }
                .frame(maxWidth: 520)
            }
            .padding(DS.Space.xl)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        default:
            DebugLoadingLine(text: String.ui("qa.steps.loading", "단계를 불러오는 중…"))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

/// The run's status, name, verdict, counts and facts, and its actions.
private struct RunHeader: View {
    let store: AppStore
    let model: QAModel
    let run: QARun
    let steps: [QAStep]?
    let compact: Bool

    var body: some View {
        let verdict = QARunVerdict(run, steps: steps)
        VStack(alignment: .leading, spacing: DS.Space.s) {
            HStack(alignment: .center, spacing: DS.Space.m) {
                QAStatusBadge(run: run.runStatus, large: true)
                Text(run.name)
                    .font(DS.Font.title)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .textSelection(.enabled)
                Spacer(minLength: DS.Space.m)
                RunActions(store: store, model: model, run: run, compact: compact)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(verdict.title)
                    .font(DS.Font.body.weight(.semibold))
                    .foregroundStyle(QAFormat.text(run.runStatus) == .secondary ? AnyShapeStyle(.primary) : AnyShapeStyle(QAFormat.text(run.runStatus)))
                if let detail = verdict.detail {
                    Text(detail)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                }
            }
            HStack(spacing: DS.Space.s) {
                CountPill(symbol: "checkmark", title: QAFormat.title(.pass), count: run.stepsPassed, tint: DS.Color.success, text: DS.Status.successText)
                CountPill(symbol: "xmark", title: QAFormat.title(.fail), count: run.stepsFailed, tint: DS.Color.danger, text: DS.Status.dangerText)
                CountPill(symbol: "nosign", title: QAFormat.title(QAStepStatus.blocked), count: run.stepsBlocked, tint: DS.Color.warning, text: DS.Status.warningText)
                CountPill(symbol: "minus", title: QAFormat.title(.notRun), count: run.stepsNotRun, tint: DS.Color.neutral, text: .secondary)
                Text(String.ui("qa.header.total", "전체 \(String(run.stepsTotal))단계"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .padding(.leading, DS.Space.xs)
                HStack(spacing: DS.Space.xs) {
                    Image(systemName: "timer")
                        .accessibilityHidden(true)
                    RunDuration(run: run)
                }
                .font(DS.Font.caption.monospacedDigit())
                .foregroundStyle(.secondary)
            }
            facts
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.top, DS.Space.m)
        .padding(.bottom, DS.Space.m)
    }

    private var facts: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: DS.Space.l) { factItems }
            VStack(alignment: .leading, spacing: DS.Space.xs) { factItems }
        }
    }

    @ViewBuilder
    private var factItems: some View {
        fact("doc.text", run.scenarioPath?.abbreviatingHome ?? String.ui("qa.header.inline", "글로 받은 시나리오"), mono: run.scenarioPath != nil,
             help: run.scenarioPath)
        fact("desktopcomputer", store.host(run.hostID)?.name ?? run.hostID)
        fact("clock", QAFormat.started(run.startedAt.date))
        fact("number", run.runID, mono: true, help: String.ui("qa.header.ids", "실행 \(run.runID) · job \(run.jobID)"))
    }

    private func fact(_ symbol: String, _ text: String, mono: Bool = false, help: String? = nil) -> some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: symbol)
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
            Text(text)
                .font(mono ? .system(size: 11, design: .monospaced) : DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
                .textSelection(.enabled)
        }
        .help(help ?? text)
    }
}

/// A count with its symbol and name on a tinted capsule.
private struct CountPill: View {
    let symbol: String
    let title: String
    let count: Int
    let tint: Color
    let text: Color

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: symbol)
                .font(.system(size: 9, weight: .heavy))
            Text(title)
            Text(String(count))
                .fontWeight(.bold)
                .monospacedDigit()
        }
        .font(DS.Font.caption)
        .foregroundStyle(count == 0 ? AnyShapeStyle(.secondary) : AnyShapeStyle(text))
        .padding(.horizontal, DS.Space.s)
        .frame(height: 20)
        .background((count == 0 ? DS.Color.neutral : tint).opacity(count == 0 ? 0.08 : DS.Status.badgeOpacity), in: Capsule())
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title + " " + String(count))
    }
}

/// 다시 실행…, 취소 or 정리 단계도 중단 while it runs, 보고서 열기 and 내보내기… once it ended.
/// Narrow tabs show the actions as icons.
private struct RunActions: View {
    let store: AppStore
    let model: QAModel
    let run: QARun
    let compact: Bool

    var body: some View {
        let cancel = model.cancelState(of: run.runID)
        HStack(spacing: DS.Space.s) {
            if cancel != .hidden {
                cancelButton(cancel)
            }
            action(String.ui("qa.action.rerun", "다시 실행…"), symbol: "arrow.clockwise",
                   help: run.scenarioPath == nil ? String.ui("qa.action.rerun.inline", "글로 받은 시나리오는 앱에서 다시 실행할 수 없습니다")
                                                 : String.ui("qa.action.rerun.help", "같은 시나리오와 변수로 실행 창을 엽니다 (⇧⌘R)"),
                   disabled: run.scenarioPath == nil) {
                model.presentRunSheet(hostID: run.hostID, rerun: run)
            }
            .keyboardShortcut("r", modifiers: [.command, .shift])
            if !run.isRunning {
                let report = QAModel.reportPath(run)
                action(String.ui("qa.action.report", "보고서 열기"), symbol: "safari",
                       help: report == nil ? String.ui("qa.action.report.none", "이 실행에는 보고서가 없습니다")
                                           : String.ui("qa.action.report.help", "report.html을 기본 브라우저로 엽니다"),
                       disabled: report == nil) {
                    model.openReport(run)
                }
                exportButton
            }
        }
        .controlSize(.regular)
    }

    private func action(_ title: String, symbol: String, help: String, disabled: Bool = false, perform: @escaping () -> Void) -> some View {
        Button(action: perform) {
            if compact {
                Image(systemName: symbol)
            } else {
                Label(title, systemImage: symbol)
            }
        }
        .disabled(disabled)
        .help(help)
        .accessibilityLabel(title)
    }

    @ViewBuilder
    private var exportButton: some View {
        let exporting: Bool = {
            if case .exporting = model.exports[run.runID] { return true }
            return false
        }()
        action(String.ui("qa.action.export", "내보내기…"), symbol: "square.and.arrow.up",
               help: run.reportDir == nil ? String.ui("qa.action.export.none", "이 실행에는 결과 묶음이 없습니다")
                                          : String.ui("qa.action.export.help", "결과 묶음(보고서·manifest·증거)을 고른 폴더에 복사하고 기록과 맞는지 확인합니다"),
               disabled: run.reportDir == nil || exporting) {
            chooseExportFolder()
        }
    }

    private func chooseExportFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = String.ui("qa.export.prompt", "여기로 내보내기")
        panel.message = String.ui("qa.export.message", "결과 묶음을 복사할 폴더를 고르세요. 그 안에 job 폴더를 새로 만듭니다.")
        let model = model
        let runID = run.runID
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task { @MainActor in await model.export(runID, to: url.path) }
        }
    }

    @ViewBuilder
    private func cancelButton(_ state: QACancelState) -> some View {
        switch state {
        case .available:
            Button(role: .destructive) {
                Task { await model.cancel(run.runID) }
            } label: {
                if compact { Image(systemName: "xmark.circle") } else { Label(String.ui("qa.cancel", "취소"), systemImage: "xmark.circle") }
            }
            .keyboardShortcut(".", modifiers: .command)
            .help(String.ui("qa.cancel.help", "진행 중인 단계를 멈추고 남은 단계를 건너뛴 뒤 정리 단계를 실행합니다 (⌘.)"))
            .accessibilityLabel(String.ui("qa.cancel", "취소"))
        case .teardownStoppable:
            Button(role: .destructive) {
                Task { await model.cancel(run.runID) }
            } label: {
                if compact { Image(systemName: "stop.circle") } else { Label(String.ui("qa.cancel.teardown", "정리 단계도 중단"), systemImage: "stop.circle") }
            }
            .foregroundStyle(DS.Status.dangerText)
            .keyboardShortcut(".", modifiers: .command)
            .help(String.ui("qa.cancel.teardown.help", "정리 단계(teardown)까지 멈춥니다. PC에 앱이나 파일이 남을 수 있습니다 (⌘.)"))
            .accessibilityLabel(String.ui("qa.cancel.teardown", "정리 단계도 중단"))
        case .sending, .stopping:
            pending(String.ui("qa.cancel.stopping", "취소하는 중…"))
        case .sendingTeardownStop, .stoppingTeardown:
            pending(String.ui("qa.cancel.stoppingTeardown", "정리 단계를 멈추는 중…"))
        case .hidden:
            EmptyView()
        }
    }

    private func pending(_ title: String) -> some View {
        HStack(spacing: DS.Space.s) {
            ProgressView().controlSize(.small)
            if !compact {
                Text(title)
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(title)
    }
}

/// Notes under the header: the cancel that came too late, the step kinds the computer could not
/// do, and the export in progress, done or failed.
private struct RunNotes: View {
    let model: QAModel
    let run: QARun

    var body: some View {
        let verdict = QARunVerdict(run)
        VStack(spacing: DS.Space.s) {
            if let note = verdict.cancelNote {
                NoteStrip(symbol: "info.circle.fill", tint: DS.Color.accent, title: note,
                          detail: String.ui("qa.note.cancelLate", "취소가 정리 단계 중에 도착해 멈춘 단계가 없었으므로 정리 단계를 끝까지 실행했고, 결과는 단계로 정했습니다."))
            }
            if !run.unsupported.isEmpty {
                let kinds = run.unsupported.map { QAFormat.action($0.action) + " (" + QAFormat.feature(of: $0.action) + ")" }.joined(separator: ", ")
                NoteStrip(symbol: "exclamationmark.triangle.fill", tint: DS.Color.warning,
                          title: String.ui("qa.note.unsupported", "이 PC에서 할 수 없는 단계 종류가 있습니다: \(kinds)"),
                          detail: run.unsupported.first?.reason)
            }
            if let export = model.exports[run.runID] {
                ExportStrip(model: model, run: run, state: export)
            }
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.bottom, hasNotes ? DS.Space.m : 0)
    }

    private var hasNotes: Bool {
        QARunVerdict(run).cancelNote != nil || !run.unsupported.isEmpty || model.exports[run.runID] != nil
    }
}

/// A one-line note with a symbol, a title, a detail and optional trailing buttons.
private struct NoteStrip<Trailing: View>: View {
    let symbol: String?
    let tint: Color
    let title: String
    var detail: String?
    var code: String?
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Group {
                if let symbol {
                    Image(systemName: symbol).foregroundStyle(tint)
                } else {
                    ProgressView().controlSize(.small)
                }
            }
            .frame(width: 16)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(DS.Font.body.weight(.semibold))
                    .fixedSize(horizontal: false, vertical: true)
                if let detail, !detail.isEmpty {
                    Text(detail)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let code {
                    ErrorCodeLine(code: code)
                }
            }
            .textSelection(.enabled)
            Spacer(minLength: DS.Space.s)
            trailing
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(tint.opacity(0.24), lineWidth: 1))
        .accessibilityElement(children: .contain)
    }
}

extension NoteStrip where Trailing == EmptyView {
    init(symbol: String?, tint: Color, title: String, detail: String? = nil, code: String? = nil) {
        self.init(symbol: symbol, tint: tint, title: title, detail: detail, code: code) { EmptyView() }
    }
}

/// An export in progress, its result with 다음에서 보기, or its failure with the original folder.
private struct ExportStrip: View {
    let model: QAModel
    let run: QARun
    let state: QAExportState

    var body: some View {
        switch state {
        case .exporting(let dir):
            NoteStrip(symbol: nil, tint: DS.Color.accent, title: String.ui("qa.export.running", "결과 묶음을 내보내는 중…"),
                      detail: String.ui("qa.export.runningTo", "\(dir.abbreviatingHome) 에 복사하고 manifest의 sha256과 맞춰 봅니다"))
        case .exported(let result):
            let files = String(result.files)
            let size = QAFormat.bytes(result.bytes)
            NoteStrip(symbol: "checkmark.circle.fill", tint: DS.Color.success,
                      title: String.ui("qa.export.done", "결과 묶음을 내보냈습니다 · 파일 \(files)개 · \(size)"),
                      detail: result.dir.abbreviatingHome) {
                Button(String.ui("qa.export.reveal", "Finder에서 보기")) { FinderReveal.reveal(path: result.dir) }
                dismiss
            }
        case .failed(let error, let dir):
            NoteStrip(symbol: "exclamationmark.octagon.fill", tint: DS.Color.danger, title: title(error),
                      detail: [error.message, fix(error)].compactMap { $0 }.joined(separator: "\n"), code: error.code) {
                if let reportDir = run.reportDir, error.code == "HASH_MISMATCH" {
                    Button(String.ui("qa.export.revealSource", "원본 폴더 보기")) { FinderReveal.reveal(path: reportDir) }
                }
                Button(String.ui("action.retry", "다시 시도")) { Task { await model.export(run.runID, to: dir) } }
                dismiss
            }
        }
    }

    private var dismiss: some View {
        Button {
            model.dismissExport(run.runID)
        } label: {
            Image(systemName: "xmark")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(.secondary)
                .frame(width: 20, height: 20)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(String.ui("action.close", "닫기"))
        .accessibilityLabel(String.ui("action.close", "닫기"))
    }

    private func title(_ error: OperationError) -> String {
        error.code == "HASH_MISMATCH" ? .ui("qa.export.mismatch", "결과 묶음이 기록과 달라 내보내지 않았습니다")
                                      : .ui("qa.export.failed", "결과 묶음을 내보내지 못했습니다")
    }

    private func fix(_ error: OperationError) -> String? {
        switch error.code {
        case "HASH_MISMATCH":
            .ui("qa.export.mismatch.fix", "실행 뒤 묶음의 파일이 바뀌었거나 빠졌습니다. 원본 폴더를 확인하세요. 복사본은 남기지 않았습니다.")
        case "INVALID_ARGUMENT":
            .ui("qa.export.invalid.fix", "이 Mac에 있는 폴더를 다시 고르세요.")
        case "NOT_FOUND":
            .ui("qa.export.notFound.fix", "이 실행의 결과 묶음이 데몬 산출물 폴더에 없습니다.")
        default:
            error.fix
        }
    }
}

/// Steps grouped by phase under phase titles; ↑ and ↓ move the selection. The focused step
/// scrolls into view when the list appears, and while the run goes on the running step does unless
/// the person chose a step.
struct StepList: View {
    let model: QAModel
    let run: QARun
    let steps: [QAStep]
    @FocusState private var focused: Bool

    var body: some View {
        let focusIndex = model.focusedStep(of: run.runID)?.index
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(QAStepPhase.allCases, id: \.self) { phase in
                        let group = steps.filter { ($0.stepPhase ?? .steps) == phase }
                        if !group.isEmpty {
                            Section {
                                ForEach(group) { step in
                                    StepRow(step: step, selected: step.index == focusIndex)
                                        .id(step.index)
                                        .contentShape(Rectangle())
                                        .onTapGesture {
                                            focused = true
                                            model.selectStep(step.index, of: run.runID)
                                        }
                                        .accessibilityAction { model.selectStep(step.index, of: run.runID) }
                                        .padding(.horizontal, DS.Space.s)
                                }
                                .padding(.bottom, 1)
                            } header: {
                                StepPhaseHeader(phase: phase, steps: group)
                            }
                        }
                    }
                }
                .padding(.bottom, DS.Space.s)
            }
            .focusable()
            .focused($focused)
            .focusEffectDisabled()
            .onKeyPress(.upArrow) {
                move(by: -1, proxy: proxy)
                return .handled
            }
            .onKeyPress(.downArrow) {
                move(by: 1, proxy: proxy)
                return .handled
            }
            .onChange(of: run.currentStep) { _, current in
                guard model.selectedSteps[run.runID] == nil, let current else { return }
                withAnimation(.snappy(duration: 0.2)) { proxy.scrollTo(current, anchor: .center) }
            }
            .onAppear {
                if let focusIndex { proxy.scrollTo(focusIndex) }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("qa.steps.a11y", "단계 목록"))
    }

    private func move(by delta: Int, proxy: ScrollViewProxy) {
        let order = QAStepPhase.allCases.flatMap { phase in steps.filter { ($0.stepPhase ?? .steps) == phase } }.map(\.index)
        guard !order.isEmpty else { return }
        let current = model.focusedStep(of: run.runID)?.index
        let position = order.firstIndex { $0 == current } ?? (delta > 0 ? -1 : order.count)
        let next = order[min(max(position + delta, 0), order.count - 1)]
        model.selectStep(next, of: run.runID)
        proxy.scrollTo(next)
    }
}
