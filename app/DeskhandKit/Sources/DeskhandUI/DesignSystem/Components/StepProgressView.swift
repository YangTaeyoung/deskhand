import SwiftUI

/// Vertical list of steps joined by a rail. Done steps show a check, the active step a spinner,
/// failed steps a cross with the failure reason in red, and pending steps an empty ring.
public struct StepProgressView: View {
    public let steps: [StepItem]

    private static let indicatorSize: CGFloat = DS.Space.l
    private static let railWidth: CGFloat = 2

    public init(steps: [StepItem]) {
        self.steps = steps
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(steps.enumerated()), id: \.element.id) { index, step in
                row(step, index: index, isLast: index == steps.count - 1)
            }
        }
    }

    private func row(_ step: StepItem, index: Int, isLast: Bool) -> some View {
        HStack(alignment: .top, spacing: DS.Space.m) {
            VStack(spacing: DS.Space.xs) {
                indicator(step.status)
                    .frame(width: Self.indicatorSize, height: Self.indicatorSize)
                if !isLast {
                    Capsule()
                        .fill(step.status == .done ? AnyShapeStyle(DS.Color.success.opacity(0.6)) : AnyShapeStyle(DS.Color.separator))
                        .frame(width: Self.railWidth)
                        .frame(maxHeight: .infinity)
                        .padding(.bottom, DS.Space.xs)
                }
            }
            .frame(width: Self.indicatorSize)

            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(step.title)
                    .font(step.status == .active ? DS.Font.headline : DS.Font.body)
                    .foregroundStyle(step.status == .pending ? .secondary : .primary)
                if let detail = step.detail {
                    Text(detail)
                        .font(DS.Font.caption)
                        .foregroundStyle(step.status == .failed ? AnyShapeStyle(DS.Status.dangerText) : AnyShapeStyle(.secondary))
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(.bottom, isLast ? 0 : DS.Space.m)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel(step, index: index))
    }

    @ViewBuilder
    private func indicator(_ status: StepStatus) -> some View {
        switch status {
        case .done, .failed:
            if let symbol = status.symbol {
                Image(systemName: symbol)
                    .resizable()
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, status.tint)
            }
        case .active:
            ProgressView()
                .controlSize(.small)
        case .pending:
            Circle()
                .strokeBorder(DS.Color.neutral.opacity(0.6), lineWidth: 1.5)
                .frame(width: Self.indicatorSize - 2, height: Self.indicatorSize - 2)
        }
    }

    private func accessibilityLabel(_ step: StepItem, index: Int) -> String {
        let base = String.ui("a11y.step", "\(index + 1)/\(steps.count)단계, \(step.title), \(step.status.title)")
        guard let detail = step.detail else { return base }
        return "\(base), \(detail)"
    }
}
