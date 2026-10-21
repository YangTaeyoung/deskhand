import SwiftUI

/// Capsule showing who holds input control. Human and AI control fill the capsule with the
/// owner color under white text; observing uses a quiet neutral capsule. `clientName` names the
/// controlling client. `aiPaused` (after an emergency stop) reads AI 일시정지됨 and
/// `sessionLocked` (Windows lock screen) reads 로그인 필요, both on a pale orange capsule with
/// label-colored text; the lock wins over the pause.
public struct ControlPill: View {
    /// `regular` for rows and cards, `large` beside large toolbar buttons.
    public enum Size: Sendable {
        case regular
        case large
    }

    public let owner: ControlOwner
    public let clientName: String?
    public let aiPaused: Bool
    public let sessionLocked: Bool
    public let size: Size

    public init(owner: ControlOwner, clientName: String? = nil, aiPaused: Bool = false,
                sessionLocked: Bool = false, size: Size = .regular) {
        self.owner = owner
        self.clientName = clientName
        self.aiPaused = aiPaused
        self.sessionLocked = sessionLocked
        self.size = size
    }

    /// Text of the pill: the owner title, followed by the client name when someone holds control.
    nonisolated public static func label(owner: ControlOwner, clientName: String?) -> String {
        guard owner != .none, let clientName, !clientName.isEmpty else { return owner.title }
        return .ui("control.withClient", "\(owner.title) · \(clientName)")
    }

    nonisolated static var loginRequired: String { .ui("control.loginRequired", "로그인 필요") }
    nonisolated static var paused: String { .ui("banner.aiPaused.title", "AI 일시정지됨") }

    /// Changes whenever the pill shows a different state or client.
    public var identity: String {
        "\(owner.rawValue)|\(clientName ?? "")|\(aiPaused)|\(sessionLocked)"
    }

    private enum Look {
        case owner
        case notice(symbol: String, title: String)
    }

    private var look: Look {
        if sessionLocked { return .notice(symbol: "lock.fill", title: Self.loginRequired) }
        if aiPaused { return .notice(symbol: "pause.circle.fill", title: Self.paused) }
        return .owner
    }

    public var body: some View {
        HStack(spacing: DS.Space.xs) {
            switch look {
            case .owner:
                Image(systemName: owner.symbol)
                    .imageScale(.small)
                Text(owner.title)
                    .fontWeight(.semibold)
                if owner != .none, let clientName, !clientName.isEmpty {
                    Text(verbatim: "·")
                        .opacity(0.7)
                    Text(clientName)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            case .notice(let symbol, let title):
                Image(systemName: symbol)
                    .imageScale(.small)
                Text(title)
                    .fontWeight(.semibold)
            }
        }
        .font(size == .large ? DS.Font.body : DS.Font.caption)
        .foregroundStyle(foreground)
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, size == .large ? 0 : DS.Space.xs)
        .frame(minHeight: size == .large ? 28 : nil)
        .background(background, in: Capsule())
        .overlay {
            if case .notice = look {
                Capsule().strokeBorder(DS.Color.warning.opacity(0.45), lineWidth: 1)
            }
        }
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("a11y.control", "제어권: \(accessibilityValue)"))
    }

    private var accessibilityValue: String {
        switch look {
        case .owner: Self.label(owner: owner, clientName: clientName)
        case .notice(_, let title): title
        }
    }

    private var foreground: AnyShapeStyle {
        switch look {
        case .notice: AnyShapeStyle(.primary)
        case .owner: owner == .none ? AnyShapeStyle(DS.Status.neutralText) : AnyShapeStyle(DS.Color.onFill)
        }
    }

    private var background: AnyShapeStyle {
        switch look {
        case .notice: AnyShapeStyle(DS.Color.warning.opacity(0.18))
        case .owner: owner == .none ? AnyShapeStyle(DS.Color.neutral.opacity(DS.Status.badgeOpacity)) : AnyShapeStyle(owner.fill)
        }
    }
}
