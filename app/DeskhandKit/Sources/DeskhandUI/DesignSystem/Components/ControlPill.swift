import SwiftUI

/// Capsule showing who holds input control. Human and AI control fill the capsule with the
/// owner color; observing uses a quiet neutral capsule. `clientName` names the controlling client.
public struct ControlPill: View {
    public let owner: ControlOwner
    public let clientName: String?

    public init(owner: ControlOwner, clientName: String? = nil) {
        self.owner = owner
        self.clientName = clientName
    }

    /// Text of the pill: the owner title, followed by the client name when someone holds control.
    nonisolated public static func label(owner: ControlOwner, clientName: String?) -> String {
        guard owner != .none, let clientName, !clientName.isEmpty else { return owner.title }
        return .ui("control.withClient", "\(owner.title) · \(clientName)")
    }

    public var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: owner.symbol)
                .imageScale(.small)
            Text(owner.title)
                .fontWeight(.semibold)
            if owner != .none, let clientName, !clientName.isEmpty {
                Text(verbatim: "·")
                    .opacity(0.6)
                Text(clientName)
                    .opacity(0.85)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
        }
        .font(DS.Font.caption)
        .foregroundStyle(owner == .none ? AnyShapeStyle(.secondary) : AnyShapeStyle(.white))
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.xs)
        .background(background, in: Capsule())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("a11y.control", "제어권: \(Self.label(owner: owner, clientName: clientName))"))
    }

    private var background: AnyShapeStyle {
        owner == .none ? AnyShapeStyle(DS.Color.observe.opacity(0.14)) : AnyShapeStyle(owner.tint.gradient)
    }
}
