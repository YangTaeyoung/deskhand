import AppKit
import DeskhandClient
import SwiftUI

/// The selected step: its status, kind, scenario line, start and duration; what happened — a
/// failed step with its failed checks as 기대 and 실제, a blocked step with why and what to do
/// next; the checks as 기준 · 기대 · 실제 · 결과; the evidence as screenshot thumbnails and file
/// chips that open the viewer; the result fields; and the step's values after substitution,
/// secrets as `***`.
struct StepDetail: View {
    let model: QAModel
    let run: QARun
    let step: QAStep
    /// Gives control back to the AI, when a person holds it and the control banner offers it.
    var giveBack: (() -> Void)?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DS.Space.l) {
                header
                outcome
                if !step.assertions.isEmpty {
                    section(String.ui("qa.detail.assertions", "확인한 기준")) {
                        AssertionsTable(assertions: step.assertions)
                    }
                }
                if !step.evidence.isEmpty {
                    section(String.ui("qa.detail.evidence", "증거")) {
                        EvidenceStrip(model: model, run: run, step: step)
                    }
                }
                if let result = step.result, !result.isEmpty {
                    section(String.ui("qa.detail.result", "결과")) {
                        ValueGrid(values: result)
                    }
                }
                if !step.params.isEmpty {
                    section(String.ui("qa.detail.params", "단계 값")) {
                        VStack(alignment: .leading, spacing: DS.Space.s) {
                            ValueGrid(values: step.params)
                            if Self.containsMask(step.params) {
                                Label(String.ui("qa.detail.masked", "secret_ 변수의 값은 ***로 가려 결과와 보고서에 남기지 않습니다"), systemImage: "lock")
                                    .font(DS.Font.caption)
                                    .foregroundStyle(DS.Status.neutralText)
                            }
                        }
                    }
                }
            }
            .padding(DS.Space.l)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(DS.Color.surface)
    }

    static func containsMask(_ values: [String: JSONValue]) -> Bool {
        values.values.contains { value in
            switch value {
            case .string(let text): text.contains("***")
            case .array(let items): items.contains { $0.stringValue?.contains("***") == true }
            default: false
            }
        }
    }

    /// The daemon's own sentence of a blocked step when it says more than the plain explanation.
    static func daemonSentence(_ step: QAStep, shown: (title: String, next: String?)) -> String? {
        guard let message = step.message ?? step.error?.message, !message.isEmpty, message != shown.title else { return nil }
        switch step.error?.code {
        case "CONTROL_HELD_BY_HUMAN", "CONTROL_PREEMPTED", nil: return nil
        default: return message
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                QAStatusBadge(step: step, large: true)
                Text(String(step.index) + ". " + step.name)
                    .font(DS.Font.headline)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: DS.Space.m) {
                StepKindChip(action: step.action)
                Text(step.action)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(DS.Status.neutralText)
                meta("text.alignleft", String.ui("qa.detail.line", "\(String(step.line))번째 줄"))
                meta("square.stack.3d.up", QAFormat.phase(step.stepPhase))
                if let started = step.startedAt {
                    meta("clock", QAFormat.started(started.date))
                }
                if step.durationMs != nil || step.stepStatus == .running {
                    HStack(spacing: DS.Space.xs) {
                        Image(systemName: "timer")
                            .accessibilityHidden(true)
                        StepDuration(step: step)
                    }
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
                }
                if step.optional {
                    StepFlag(text: String.ui("qa.step.optional", "선택 단계"), help: String.ui("qa.step.optional.help", "선택 단계: 결과는 남지만 흐름과 실행 결과에서 뺍니다"))
                }
            }
        }
    }

    private func meta(_ symbol: String, _ text: String) -> some View {
        Label(text, systemImage: symbol)
            .labelStyle(.titleAndIcon)
            .font(DS.Font.caption)
            .foregroundStyle(DS.Status.neutralText)
            .lineLimit(1)
    }

    @ViewBuilder
    private var outcome: some View {
        switch step.stepStatus {
        case .fail:
            OutcomeBox(symbol: "xmark.octagon.fill", tint: DS.Color.danger, title: step.message ?? step.error?.message ?? QAFormat.title(.fail),
                       detail: step.message != nil && step.error?.message != step.message ? step.error?.message : nil, code: step.error?.code) {
                let failed = step.assertions.filter { !$0.passed }
                if !failed.isEmpty {
                    FailedChecks(assertions: Array(failed.prefix(2)))
                }
            }
        case .blocked:
            let reason = QAFormat.blockedReason(step)
            if StepRow.isHumanControl(step) {
                OutcomeBox(symbol: "hand.raised.fill", tint: DS.Color.human, title: reason.title, detail: nil, code: step.error?.code,
                           next: reason.next) {
                    if let giveBack {
                        Button(action: giveBack) {
                            Label(String.ui("control.giveBack", "AI에게 넘기기"), systemImage: "sparkles")
                        }
                        .controlSize(.regular)
                        .help(String.ui("qa.blocked.human.giveBack.help", "이 PC의 제어권을 AI에게 넘깁니다. 그다음 다시 실행하세요."))
                    }
                }
            } else {
                OutcomeBox(symbol: QAFormat.isCancelStopped(step) ? "stop.circle" : "nosign",
                           tint: QAFormat.isCancelStopped(step) ? DS.Color.neutral : DS.Color.warning, title: reason.title, detail: nil,
                           code: step.error?.code, next: reason.next, source: Self.daemonSentence(step, shown: reason))
            }
        case .notRun:
            OutcomeBox(symbol: "minus.circle", tint: DS.Color.neutral, title: String.ui("qa.detail.notRun", "실행하지 않았습니다"),
                       detail: step.message, code: nil)
        case .running:
            OutcomeBox(symbol: nil, tint: DS.Color.accent, title: String.ui("qa.detail.running", "실행 중입니다"),
                       detail: String.ui("qa.detail.running.detail", "끝나면 결과와 증거가 여기에 나타납니다."), code: nil)
        case .pending:
            OutcomeBox(symbol: "circle", tint: DS.Color.neutral, title: String.ui("qa.detail.pending", "아직 실행하지 않은 단계입니다"),
                       detail: nil, code: nil)
        case .pass:
            OutcomeBox(symbol: "checkmark.circle.fill", tint: DS.Color.success,
                       title: step.message.flatMap { $0.isEmpty ? nil : $0 } ?? String.ui("qa.detail.passed", "통과했습니다"), detail: nil, code: nil)
        case nil:
            EmptyView()
        }
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            Text(title)
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(DS.Status.neutralText)
                .accessibilityAddTraits(.isHeader)
            content()
        }
    }
}

/// What happened in a tinted box: a symbol (a spinner when nil), a title, a detail, what to do
/// next, the daemon's own sentence, extra content such as failed checks or a button, and the code.
struct OutcomeBox<Extra: View>: View {
    let symbol: String?
    let tint: Color
    let title: String
    let detail: String?
    let code: String?
    var next: String?
    /// The daemon's sentence, shown under the explanation.
    var source: String?
    @ViewBuilder var extra: Extra

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Group {
                if let symbol {
                    Image(systemName: symbol)
                        .foregroundStyle(tint == DS.Color.neutral ? AnyShapeStyle(DS.Status.neutralText) : AnyShapeStyle(tint))
                } else {
                    ProgressView().controlSize(.small)
                }
            }
            .font(DS.Font.body.weight(.semibold))
            .frame(width: 16)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.body.weight(.semibold))
                    .fixedSize(horizontal: false, vertical: true)
                if let detail {
                    Text(detail)
                        .font(DS.Font.body)
                        .foregroundStyle(DS.Status.neutralText)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let next {
                    Label(next, systemImage: "arrow.turn.down.right")
                        .font(DS.Font.body)
                        .foregroundStyle(.primary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let source {
                    Text(String.ui("qa.detail.daemonSays", "데몬: \(source)"))
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Status.neutralText)
                        .fixedSize(horizontal: false, vertical: true)
                }
                extra
                if let code {
                    QAErrorCodeLine(code: code)
                }
            }
            .textSelection(.enabled)
            Spacer(minLength: 0)
        }
        .padding(DS.Space.m)
        .background(tint.opacity(DS.Status.boxOpacity), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(tint.opacity(0.24), lineWidth: 1))
        .accessibilityElement(children: .contain)
    }
}

extension OutcomeBox where Extra == EmptyView {
    init(symbol: String?, tint: Color, title: String, detail: String?, code: String?, next: String? = nil, source: String? = nil) {
        self.init(symbol: symbol, tint: tint, title: title, detail: detail, code: code, next: next, source: source) { EmptyView() }
    }
}

/// The failed checks of a step as 기대 and 실제 lines, for the outcome box.
struct FailedChecks: View {
    let assertions: [QAAssertion]

    var body: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.xs) {
            ForEach(Array(assertions.enumerated()), id: \.offset) { _, assertion in
                GridRow {
                    Text(QAFormat.assertion(assertion.kind))
                        .font(DS.Font.caption.weight(.semibold))
                        .foregroundStyle(DS.Status.neutralText)
                        .gridCellColumns(2)
                }
                GridRow {
                    Text(String.ui("qa.assert.column.expected", "기대"))
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Status.neutralText)
                    Text(QAFormat.expectedText(assertion.expected, kind: assertion.kind))
                        .font(DS.Font.mono)
                        .lineLimit(2)
                        .truncationMode(.tail)
                }
                GridRow {
                    Text(String.ui("qa.assert.column.actual", "실제"))
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Status.neutralText)
                    HighlightedValue(assertion: assertion, lineLimit: 2)
                }
            }
        }
        .padding(.top, DS.Space.xs)
    }
}

/// An assertion's actual value, in the danger color when the check failed, with the parts that
/// differ marked in the label color on a red highlight, underlined.
struct HighlightedValue: View {
    let assertion: QAAssertion
    var lineLimit: Int?

    var body: some View {
        Text(attributed)
            .font(DS.Font.mono)
            .foregroundStyle(assertion.passed ? AnyShapeStyle(.primary) : AnyShapeStyle(DS.Status.dangerText))
            .lineLimit(lineLimit)
            .truncationMode(.tail)
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var attributed: AttributedString {
        let text = QAFormat.value(assertion.actual, kind: assertion.kind)
        var out = AttributedString(text)
        guard case .string(let raw) = assertion.actual, raw == text else { return out }
        for range in QAFormat.highlights(kind: assertion.kind, expected: assertion.expected, actual: assertion.actual, passed: assertion.passed) {
            guard let lower = AttributedString.Index(range.lowerBound, within: out),
                  let upper = AttributedString.Index(range.upperBound, within: out) else { continue }
            out[lower..<upper].backgroundColor = DS.Color.danger.opacity(0.22)
            out[lower..<upper].foregroundColor = .primary
            out[lower..<upper].underlineStyle = .single
        }
        return out
    }
}

/// Assertions as 기준 · 기대 · 실제 · 결과 rows; a failed one's actual value is in the danger color
/// with the parts that differ marked, and a missing text is named. Long values show six lines
/// until 전체 보기.
struct AssertionsTable: View {
    let assertions: [QAAssertion]
    @State private var expanded: Set<Int> = []

    static let kindWidth: CGFloat = 104
    static let resultWidth: CGFloat = 64

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: DS.Space.m) {
                heading(String.ui("qa.assert.column.kind", "기준")).frame(width: Self.kindWidth, alignment: .leading)
                heading(String.ui("qa.assert.column.expected", "기대")).frame(maxWidth: .infinity, alignment: .leading)
                heading(String.ui("qa.assert.column.actual", "실제")).frame(maxWidth: .infinity, alignment: .leading)
                heading(String.ui("qa.assert.column.result", "결과")).frame(width: Self.resultWidth, alignment: .leading)
            }
            .padding(.horizontal, DS.Space.m)
            .frame(height: 28)
            .background(DS.Color.surfaceRaised.opacity(0.55))
            ForEach(Array(assertions.enumerated()), id: \.offset) { index, assertion in
                Rectangle().fill(DS.Color.separator).frame(height: 1)
                row(index, assertion)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
    }

    private func heading(_ text: String) -> some View {
        Text(text)
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(DS.Status.neutralText)
    }

    private func row(_ index: Int, _ assertion: QAAssertion) -> some View {
        let expected = QAFormat.expectedText(assertion.expected, kind: assertion.kind)
        let actual = QAFormat.value(assertion.actual, kind: assertion.kind)
        let long = Self.isLong(expected) || Self.isLong(actual)
        let open = expanded.contains(index)
        let verdict = assertion.passed ? QAFormat.title(QAStepStatus.pass) : QAFormat.title(QAStepStatus.fail)
        let kind = QAFormat.assertion(assertion.kind)
        return HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            VStack(alignment: .leading, spacing: 2) {
                Text(kind)
                    .font(DS.Font.body)
                Text(assertion.kind)
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundStyle(DS.Status.neutralText)
            }
            .frame(width: Self.kindWidth, alignment: .leading)
            Text(expected)
                .font(DS.Font.mono)
                .lineLimit(open ? nil : 6)
                .truncationMode(.tail)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                if let missing = QAFormat.missingNote(kind: assertion.kind, expected: assertion.expected, passed: assertion.passed) {
                    Text(missing)
                        .font(DS.Font.caption.weight(.semibold))
                        .foregroundStyle(DS.Status.dangerText)
                }
                HighlightedValue(assertion: assertion, lineLimit: open ? nil : 6)
                if long {
                    Button(open ? String.ui("qa.assert.collapse", "줄이기") : String.ui("qa.assert.expand", "전체 보기")) {
                        if open { expanded.remove(index) } else { expanded.insert(index) }
                    }
                    .buttonStyle(.link)
                    .tint(DS.Color.accent)
                    .font(DS.Font.caption)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            QAStatusBadge(step: assertion.passed ? .pass : .fail)
                .frame(width: Self.resultWidth, alignment: .leading)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(assertion.passed ? AnyShapeStyle(.clear) : AnyShapeStyle(DS.Code.errorRow))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("qa.assert.a11y", "\(kind), 기대 \(expected), 실제 \(actual), \(verdict)"))
    }

    static func isLong(_ text: String) -> Bool {
        text.count > 360 || text.split(separator: "\n", omittingEmptySubsequences: false).count > 6
    }
}

/// Parameter or result values as `key  value` rows in a monospaced font, keys sorted.
struct ValueGrid: View {
    let values: [String: JSONValue]

    var body: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.xs) {
            ForEach(values.keys.sorted(), id: \.self) { key in
                GridRow {
                    Text(key)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(DS.Status.neutralText)
                        .gridColumnAlignment(.leading)
                    Text(QAFormat.value(values[key] ?? .null))
                        .font(DS.Font.mono)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .accessibilityElement(children: .combine)
            }
        }
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.neutral.opacity(0.06), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
    }
}

/// A step's evidence: screenshots as thumbnails, other files as chips. Each opens the viewer;
/// the context menu shows the file in Finder, opens it with its app or copies its path.
struct EvidenceStrip: View {
    let model: QAModel
    let run: QARun
    let step: QAStep

    var body: some View {
        FlowLayout(spacing: DS.Space.m, lineSpacing: DS.Space.m) {
            ForEach(step.evidence) { artifact in
                Button {
                    model.viewer = QAEvidenceSelection(runID: run.runID, stepIndex: step.index, artifactID: artifact.artifactID)
                } label: {
                    if QAEvidenceStore.isImage(artifact) {
                        EvidenceThumbnail(store: model.evidence, artifact: artifact)
                    } else {
                        EvidenceChip(artifact: artifact)
                    }
                }
                .buttonStyle(.plain)
                .help(String.ui("qa.evidence.open.help", "\(artifact.name) 보기 · \(artifact.path.abbreviatingHome)"))
                .accessibilityLabel(String.ui("qa.evidence.open.a11y", "증거 \(artifact.name) 보기"))
                .contextMenu { EvidenceMenu(artifact: artifact) }
            }
        }
    }
}

/// Finder, open-with-app and copy-path items for an evidence file.
struct EvidenceMenu: View {
    let artifact: Artifact

    var body: some View {
        Button(String.ui("qa.evidence.reveal", "Finder에서 보기")) { FinderReveal.reveal(path: artifact.path) }
        Button(String.ui("qa.evidence.openApp", "기본 앱으로 열기")) { NSWorkspace.shared.open(URL(fileURLWithPath: artifact.path)) }
        Divider()
        Button(String.ui("qa.evidence.copyPath", "경로 복사")) { Pasteboard.copy(artifact.path) }
    }
}

/// A screenshot as a 16:10 thumbnail over its name and size, downsampled when it first shows.
struct EvidenceThumbnail: View {
    static let size = CGSize(width: 192, height: 120)

    let store: QAEvidenceStore
    let artifact: Artifact
    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            ZStack {
                DS.Color.screenBackdrop
                switch store.content(of: artifact, size: .thumbnail) {
                case .image(let image, _):
                    Image(nsImage: image)
                        .resizable()
                        .interpolation(.high)
                        .aspectRatio(contentMode: .fill)
                case .failed:
                    VStack(spacing: DS.Space.xs) {
                        Image(systemName: "photo.badge.exclamationmark")
                            .font(.system(size: 18))
                        Text(String.ui("qa.evidence.unreadable", "읽을 수 없음"))
                            .font(DS.Font.caption)
                    }
                    .foregroundStyle(DS.Color.overlayForeground)
                default:
                    ProgressView()
                        .controlSize(.small)
                        .tint(DS.Color.overlayForeground)
                }
            }
            .frame(width: Self.size.width, height: Self.size.height)
            .clipShape(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                .strokeBorder(hovering ? DS.Color.accent : DS.Color.separator, lineWidth: hovering ? 2 : 1))
            .overlay(alignment: .bottomTrailing) {
                Image(systemName: "arrow.up.left.and.arrow.down.right")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(DS.Color.onFill)
                    .padding(5)
                    .background(DS.Color.scrim, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
                    .padding(6)
                    .opacity(hovering ? 1 : 0)
            }
            Text(artifact.name)
                .font(.system(size: 11, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(width: Self.size.width, alignment: .leading)
            Text(QAFormat.bytes(artifact.size))
                .font(DS.Font.caption)
                .foregroundStyle(DS.Status.neutralText)
        }
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onAppear { store.load(artifact, size: .thumbnail) }
    }
}

/// A non-image evidence file as a chip with its kind's symbol, name and size.
struct EvidenceChip: View {
    let artifact: Artifact
    @State private var hovering = false

    static func symbol(_ kind: String) -> String {
        switch kind {
        case "ui_tree": "list.bullet.indent"
        case "log": "doc.text"
        case "report": "chart.bar.doc.horizontal"
        case "trace": "waveform.path"
        default: "doc"
        }
    }

    static func kindTitle(_ kind: String) -> String {
        switch kind {
        case "ui_tree": .ui("qa.evidence.kind.uiTree", "UI 스냅샷")
        case "log": .ui("qa.evidence.kind.log", "로그")
        case "report": .ui("qa.evidence.kind.report", "수집 기록")
        case "screenshot": .ui("qa.evidence.kind.screenshot", "스크린샷")
        case "trace": .ui("qa.evidence.kind.trace", "추적")
        default: .ui("qa.evidence.kind.file", "파일")
        }
    }

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Image(systemName: Self.symbol(artifact.kind))
                .font(.system(size: 16))
                .foregroundStyle(DS.Status.neutralText)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(artifact.name)
                    .font(.system(size: 12, design: .monospaced))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(Self.kindTitle(artifact.kind) + " · " + QAFormat.bytes(artifact.size))
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
            }
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .frame(width: 240, alignment: .leading)
        .background(DS.Color.neutral.opacity(hovering ? 0.14 : 0.08), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
            .strokeBorder(hovering ? DS.Color.accent : DS.Color.separator, lineWidth: 1))
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
    }
}

/// `오류 코드 CODE` with a selectable monospaced code, in the neutral gray that stays legible on
/// tinted boxes.
struct QAErrorCodeLine: View {
    let code: String

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Text(String.ui("error.codeLabel", "오류 코드"))
            Text(code)
                .monospaced()
                .textSelection(.enabled)
        }
        .font(DS.Font.caption)
        .foregroundStyle(DS.Status.neutralText)
        .accessibilityElement(children: .combine)
    }
}
