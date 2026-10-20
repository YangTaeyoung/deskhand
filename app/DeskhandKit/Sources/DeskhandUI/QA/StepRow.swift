import DeskhandClient
import SwiftUI

/// One step: its status mark, number, name, kind, marks for optional and continue-on-fail steps
/// and evidence, the outcome line, and the duration or, while it runs, the time so far. A selected
/// row is filled with the selection color and draws everything in white.
struct StepRow: View {
    let step: QAStep
    let selected: Bool
    @State private var hovering = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            StepStatusMark(step: step, selected: selected)
                .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }
            Text(String(step.index))
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
                .frame(width: 26, alignment: .trailing)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: DS.Space.s) {
                    Text(step.name)
                        .font(DS.Font.body.weight(isFocusStatus ? .semibold : .regular))
                        .foregroundStyle(nameStyle)
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .layoutPriority(1)
                    if QAFormat.action(step.action) != step.name {
                        StepKindChip(action: step.action, inverted: selected)
                    }
                    if step.optional {
                        StepFlag(text: String.ui("qa.step.optional", "선택 단계"), help: String.ui("qa.step.optional.help", "선택 단계: 결과는 남지만 흐름과 실행 결과에서 뺍니다"),
                                 inverted: selected)
                    }
                    if step.continueOnFail {
                        StepFlag(text: String.ui("qa.step.continue", "실패해도 계속"),
                                 help: String.ui("qa.step.continue.help", "이 단계가 실패해도 다음 단계를 실행합니다"), inverted: selected)
                    }
                    if !step.evidence.isEmpty {
                        Label(String(step.evidence.count), systemImage: "paperclip")
                            .font(DS.Font.caption)
                            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
                            .labelStyle(.titleAndIcon)
                            .fixedSize()
                            .help(String.ui("qa.step.evidence.help", "증거 \(String(step.evidence.count))개"))
                    }
                }
                if let line = outcomeLine {
                    Text(line.text)
                        .font(DS.Font.caption)
                        .foregroundStyle(selected ? DS.Color.onFill : line.color)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            Spacer(minLength: DS.Space.s)
            StepDuration(step: step, inverted: selected)
                .font(.system(size: 11, design: .monospaced))
                .fixedSize()
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, 6)
        .frame(minHeight: 34)
        .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .onHover { hovering = $0 }
        .help(String.ui("qa.step.line.help", "시나리오 \(String(step.line))번째 줄 · \(step.action)"))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : [.isButton])
    }

    private var isFocusStatus: Bool {
        (step.stepStatus == .fail || step.stepStatus == .blocked || step.stepStatus == .running) && !QAFormat.isCancelStopped(step)
    }

    private var nameStyle: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.onFill) }
        return step.stepStatus == .notRun ? AnyShapeStyle(DS.Status.neutralText) : AnyShapeStyle(.primary)
    }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Status.selectionFill) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.08)) : AnyShapeStyle(.clear)
    }

    /// The line under the name: why a blocked step did not run, otherwise the daemon's message.
    private var outcomeLine: (text: String, color: Color)? {
        switch step.stepStatus {
        case .blocked:
            let title = QAFormat.blockedReason(step).title
            if QAFormat.isCancelStopped(step) { return (title, DS.Status.neutralText) }
            return (title, Self.isHumanControl(step) ? DS.Status.humanText : DS.Status.warningText)
        case .fail:
            return step.message.map { ($0, DS.Status.dangerText) } ?? step.error.map { ($0.message, DS.Status.dangerText) }
        case .running:
            return (String.ui("qa.step.runningLine", "실행 중…"), DS.Status.accentText)
        default:
            guard let message = step.message, !message.isEmpty else { return nil }
            return (message, DS.Status.neutralText)
        }
    }

    static func isHumanControl(_ step: QAStep) -> Bool {
        step.error?.code == "CONTROL_HELD_BY_HUMAN" || step.error?.code == "CONTROL_PREEMPTED"
    }

    private var accessibilityText: String {
        var parts = [String(step.index) + ". " + step.name, QAFormat.title(step: step), QAFormat.action(step.action)]
        if let line = outcomeLine { parts.append(line.text) }
        if let ms = step.durationMs { parts.append(QAFormat.duration(ms: ms)) }
        return parts.joined(separator: ", ")
    }
}

/// A step's status as its symbol in its color — a raised hand for a person holding control, a
/// stop mark for a cancel — and a spinner while it runs; white on a selected row.
struct StepStatusMark: View {
    let step: QAStep
    var selected = false

    var body: some View {
        Group {
            if step.stepStatus == .running {
                ProgressView()
                    .controlSize(.small)
                    .scaleEffect(0.8)
            } else {
                Image(systemName: StepRow.isHumanControl(step) ? "hand.raised.fill" : QAFormat.symbol(step: step))
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(style)
            }
        }
        .frame(width: 16, height: 16)
        .accessibilityHidden(true)
    }

    private var style: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Color.onFill) }
        if StepRow.isHumanControl(step) { return AnyShapeStyle(DS.Status.humanText) }
        return AnyShapeStyle(QAFormat.text(step: step))
    }
}

/// The step kind in words, with its symbol.
struct StepKindChip: View {
    let action: String
    var inverted = false

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: QAFormat.actionSymbol(action))
                .font(.system(size: 9, weight: .semibold))
            Text(QAFormat.action(action))
                .lineLimit(1)
        }
        .font(DS.Font.caption)
        .foregroundStyle(inverted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
        .padding(.horizontal, 6)
        .frame(height: 18)
        .background(inverted ? AnyShapeStyle(.clear) : AnyShapeStyle(DS.Color.neutral.opacity(0.1)),
                    in: RoundedRectangle(cornerRadius: 4, style: .continuous))
        .overlay {
            if inverted {
                RoundedRectangle(cornerRadius: 4, style: .continuous).strokeBorder(DS.Color.onFill.opacity(0.6), lineWidth: 1)
            }
        }
        .fixedSize()
        .help(action)
    }
}

/// A small outlined mark such as 선택 단계.
struct StepFlag: View {
    let text: String
    let help: String
    var inverted = false

    var body: some View {
        Text(text)
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(inverted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
            .padding(.horizontal, 5)
            .frame(height: 16)
            .overlay(RoundedRectangle(cornerRadius: 4, style: .continuous)
                .strokeBorder(inverted ? AnyShapeStyle(DS.Color.onFill.opacity(0.6)) : AnyShapeStyle(DS.Color.separator), lineWidth: 1))
            .fixedSize()
            .help(help)
    }
}

/// A step's duration, or the time since it started while it runs, updated every second.
struct StepDuration: View {
    let step: QAStep
    var inverted = false

    var body: some View {
        if step.stepStatus == .running, let started = step.startedAt?.date {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                Text(QAFormat.duration(from: started, to: max(context.date, started)))
                    .foregroundStyle(inverted ? DS.Color.onFill : DS.Status.accentText)
            }
        } else if let ms = step.durationMs {
            Text(QAFormat.duration(ms: ms))
                .foregroundStyle(inverted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
        } else {
            Text("—")
                .foregroundStyle(inverted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText))
        }
    }
}

/// The title of a phase in the step list: the phase in words and its daemon name, its step count,
/// and how many of its steps passed, failed, were blocked, were stopped by a cancel or did not run.
struct StepPhaseHeader: View {
    let phase: QAStepPhase
    let steps: [QAStep]

    var body: some View {
        let counts = QAStepCounts(steps: steps)
        HStack(spacing: DS.Space.s) {
            Text(QAFormat.phase(phase))
                .font(DS.Font.caption.weight(.bold))
                .foregroundStyle(.primary)
            Text(phase.rawValue)
                .font(.system(size: 10, design: .monospaced))
                .foregroundStyle(DS.Status.neutralText)
            Text(String(steps.count))
                .font(.system(size: 10, weight: .semibold).monospacedDigit())
                .foregroundStyle(DS.Status.neutralText)
                .padding(.horizontal, 5)
                .frame(height: 15)
                .background(DS.Color.neutral.opacity(0.12), in: Capsule())
            Spacer(minLength: 0)
            StepTally(counts: counts)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.top, DS.Space.s)
        .frame(height: 30)
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .ignore)
        .accessibilityAddTraits(.isHeader)
        .accessibilityLabel(Self.spoken(phase, steps.count, counts))
    }

    static func spoken(_ phase: QAStepPhase, _ count: Int, _ counts: QAStepCounts) -> String {
        let name = QAFormat.phase(phase)
        let total = String(count)
        return .ui("qa.phase.a11y", "\(name) \(total)개") + ", " + StepTally.spoken(counts)
    }
}

/// Counts of passed, failed, blocked, cancel-stopped and not-run steps as symbols with numbers;
/// zero counts are left out.
struct StepTally: View {
    let counts: QAStepCounts

    static func spoken(_ counts: QAStepCounts) -> String {
        let passed = String(counts.passed)
        let failed = String(counts.failed)
        let blocked = String(counts.blocked)
        let notRun = String(counts.notRun)
        var text = String.ui("qa.counts.a11y", "통과 \(passed), 실패 \(failed), 차단 \(blocked), 실행 안 함 \(notRun)")
        if counts.cancelStopped > 0 {
            let stopped = String(counts.cancelStopped)
            text += ", " + String.ui("qa.counts.cancelStopped.a11y", "취소로 멈춤 \(stopped)")
        }
        return text
    }

    var inverted = false

    init(counts: QAStepCounts, inverted: Bool = false) {
        self.counts = counts
        self.inverted = inverted
    }

    var body: some View {
        HStack(spacing: DS.Space.s) {
            item("checkmark", counts.passed, DS.Status.successText)
            item("xmark", counts.failed, DS.Status.dangerText)
            item("nosign", counts.blocked, DS.Status.warningText)
            item("stop.fill", counts.cancelStopped, DS.Status.neutralText)
            item("minus", counts.notRun, DS.Status.neutralText)
        }
        .font(.system(size: 11, weight: .semibold).monospacedDigit())
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Self.spoken(counts))
    }

    @ViewBuilder
    private func item(_ symbol: String, _ count: Int, _ color: Color) -> some View {
        if count > 0 {
            HStack(spacing: 2) {
                Image(systemName: symbol)
                    .font(.system(size: 8, weight: .heavy))
                Text(String(count))
            }
            .foregroundStyle(inverted ? DS.Color.onFill : color)
        }
    }
}
