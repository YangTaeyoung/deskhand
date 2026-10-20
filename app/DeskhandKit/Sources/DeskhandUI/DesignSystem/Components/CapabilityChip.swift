import SwiftUI

/// Capsule naming one agent capability, with a state symbol and tint.
/// The tooltip shows `reason` when given, otherwise the state label.
public struct CapabilityChip: View {
    public let name: String
    public let state: CapabilityState
    public let reason: String?

    public init(name: String, state: CapabilityState, reason: String? = nil) {
        self.name = name
        self.state = state
        self.reason = reason
    }

    public var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: state.symbol)
                .symbolRenderingMode(.palette)
                .foregroundStyle(.white, state.tint)
                .imageScale(.small)
            Text(name)
                .foregroundStyle(state == .unsupported ? .secondary : .primary)
                .lineLimit(1)
        }
        .font(DS.Font.caption.weight(.medium))
        .padding(.leading, DS.Space.xs)
        .padding(.trailing, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .background(state.tint.opacity(0.14), in: Capsule())
        .help(reason ?? label)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(reason.map { "\(label), \($0)" } ?? label)
    }

    private var label: String {
        .ui("a11y.capability", "\(name): \(state.title)")
    }
}
