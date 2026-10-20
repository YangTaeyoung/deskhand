import SwiftUI

/// Capsule showing who holds input control. Human and AI control fill the capsule with the
/// owner color; observing uses a quiet neutral capsule. `clientName` names the controlling client.
/// `sessionLocked` replaces the owner with 로그인 필요 while Windows shows its lock screen.
public struct ControlPill: View {
    public let owner: ControlOwner
    public let clientName: String?
    public let sessionLocked: Bool

    public init(owner: ControlOwner, clientName: String? = nil, sessionLocked: Bool = false) {
        self.owner = owner
        self.clientName = clientName
        self.sessionLocked = sessionLocked
    }

    /// Text of the pill: the owner title, followed by the client name when someone holds control.
    nonisolated public static func label(owner: ControlOwner, clientName: String?) -> String {
        guard owner != .none, let clientName, !clientName.isEmpty else { return owner.title }
        return .ui("control.withClient", "\(owner.title) · \(clientName)")
    }

    nonisolated static var loginRequired: String { .ui("control.loginRequired", "로그인 필요") }

    public var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: sessionLocked ? "lock.fill" : owner.symbol)
                .imageScale(.small)
                .foregroundStyle(sessionLocked ? AnyShapeStyle(DS.Color.warning) : AnyShapeStyle(.foreground))
            Text(sessionLocked ? Self.loginRequired : owner.title)
                .fontWeight(.semibold)
            if !sessionLocked, owner != .none, let clientName, !clientName.isEmpty {
                Text(verbatim: "·")
                    .opacity(0.7)
                Text(clientName)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
        }
        .font(DS.Font.caption)
        .foregroundStyle(foreground)
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.xs)
        .background(background, in: Capsule())
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("a11y.control", "제어권: \(sessionLocked ? Self.loginRequired : Self.label(owner: owner, clientName: clientName))"))
    }

    private var foreground: AnyShapeStyle {
        if sessionLocked { return AnyShapeStyle(.primary) }
        return owner == .none ? AnyShapeStyle(.secondary) : AnyShapeStyle(.white)
    }

    private var background: AnyShapeStyle {
        if sessionLocked { return AnyShapeStyle(DS.Color.warning.opacity(0.16)) }
        return owner == .none ? AnyShapeStyle(DS.Color.observe.opacity(0.14)) : AnyShapeStyle(owner.fill.gradient)
    }
}
