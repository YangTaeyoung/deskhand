import DeskhandClient
import SwiftUI

/// One step: its status mark, number, name, kind, marks for optional and continue-on-fail steps
/// and evidence, the outcome line, and the duration or, while it runs, the time so far.
struct StepRow: View {
    let step: QAStep
    let selected: Bool
    @State private var hovering = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            StepStatusMark(status: step.stepStatus)
                .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }
            Text(String(step.index))
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
                .frame(width: 22, alignment: .trailing)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: DS.Space.s) {
                    Text(step.name)
                        .font(DS.Font.body.weight(isFocusStatus ? .semibold : .regular))
                        .foregroundStyle(step.stepStatus == .notRun ? AnyShapeStyle(.secondary) : AnyShapeStyle(.primary))
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .layoutPriority(1)
                    if QAFormat.action(step.action) != step.name {
                        StepKindChip(action: step.action)
                    }
                    if step.optional {
                        StepFlag(text: String.ui("qa.step.optional", "선택 단계"), help: String.ui("qa.step.optional.help", "선택 단계: 결과는 남지만 흐름과 실행 결과에서 뺍니다"))
                    }
                    if step.continueOnFail {
                        StepFlag(text: String.ui("qa.step.continue", "실패해도 계속"),
                                 help: String.ui("qa.step.continue.help", "이 단계가 실패해도 다음 단계를 실행합니다"))
                    }
                    if !step.evidence.isEmpty {
                        Label(String(step.evidence.count), systemImage: "paperclip")
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .labelStyle(.titleAndIcon)
                            .fixedSize()
                            .help(String.ui("qa.step.evidence.help", "증거 \(String(step.evidence.count))개"))
                    }
                }
                if let line = outcomeLine {
                    Text(line.text)
                        .font(DS.Font.caption)
                        .foregroundStyle(line.color)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            Spacer(minLength: DS.Space.s)
            StepDuration(step: step)
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
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

    private var isFocusStatus: Bool { step.stepStatus == .fail || step.stepStatus == .blocked || step.stepStatus == .running }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Code.selection) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.08)) : AnyShapeStyle(.clear)
    }

    /// The line under the name: why a blocked step did not run, otherwise the daemon's message.
    private var outcomeLine: (text: String, color: Color)? {
        switch step.stepStatus {
        case .blocked:
            return (QAFormat.blockedReason(step).title, DS.Status.warningText)
        case .fail:
            return step.message.map { ($0, DS.Status.dangerText) } ?? step.error.map { ($0.message, DS.Status.dangerText) }
        case .running:
            return (String.ui("qa.step.runningLine", "실행 중…"), DS.Status.accentText)
        default:
            guard let message = step.message, !message.isEmpty else { return nil }
            return (message, .secondary)
        }
    }

    private var accessibilityText: String {
        var parts = [String(step.index) + ". " + step.name, QAFormat.title(step.stepStatus), QAFormat.action(step.action)]
        if let line = outcomeLine { parts.append(line.text) }
        if let ms = step.durationMs { parts.append(QAFormat.duration(ms: ms)) }
        return parts.joined(separator: ", ")
    }
}

/// A step's status as its symbol in its color; a spinner while it runs.
struct StepStatusMark: View {
    let status: QAStepStatus?

    var body: some View {
        Group {
            if status == .running {
                ProgressView()
                    .controlSize(.small)
                    .scaleEffect(0.8)
            } else {
                Image(systemName: QAFormat.symbol(status))
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(status == .pending || status == .notRun ? AnyShapeStyle(.tertiary) : AnyShapeStyle(QAFormat.text(status)))
            }
        }
        .frame(width: 16, height: 16)
        .accessibilityHidden(true)
    }
}

/// The step kind in words, with its symbol.
struct StepKindChip: View {
    let action: String

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: QAFormat.actionSymbol(action))
                .font(.system(size: 9, weight: .semibold))
            Text(QAFormat.action(action))
                .lineLimit(1)
        }
        .font(DS.Font.caption)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 6)
        .frame(height: 18)
        .background(DS.Color.neutral.opacity(0.1), in: RoundedRectangle(cornerRadius: 4, style: .continuous))
        .fixedSize()
        .help(action)
    }
}

/// A small outlined mark such as 선택.
struct StepFlag: View {
    let text: String
    let help: String

    var body: some View {
        Text(text)
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 5)
            .frame(height: 16)
            .overlay(RoundedRectangle(cornerRadius: 4, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
            .fixedSize()
            .help(help)
    }
}

/// A step's duration, or the time since it started while it runs, updated every second.
struct StepDuration: View {
    let step: QAStep

    var body: some View {
        if step.stepStatus == .running, let started = step.startedAt?.date {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                Text(QAFormat.duration(from: started, to: max(context.date, started)))
                    .foregroundStyle(DS.Status.accentText)
            }
        } else if let ms = step.durationMs {
            Text(QAFormat.duration(ms: ms))
        } else {
            Text("—")
                .foregroundStyle(.tertiary)
        }
    }
}

/// The title of a phase in the step list: the phase in words, its count and how its steps ended.
struct StepPhaseHeader: View {
    let phase: QAStepPhase
    let steps: [QAStep]

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(QAFormat.phase(phase))
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            Text(phase.rawValue)
                .font(.system(size: 10, design: .monospaced))
                .foregroundStyle(.tertiary)
            Text(String(steps.count))
                .font(.system(size: 10, weight: .semibold).monospacedDigit())
                .foregroundStyle(.secondary)
                .padding(.horizontal, 5)
                .frame(height: 15)
                .background(DS.Color.neutral.opacity(0.12), in: Capsule())
            Spacer(minLength: 0)
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: 26)
        .frame(maxWidth: .infinity)
        .background(DS.Color.surface)
        .accessibilityAddTraits(.isHeader)
        .accessibilityLabel(String.ui("qa.phase.a11y", "\(QAFormat.phase(phase)) 단계 \(String(steps.count))개"))
    }
}
