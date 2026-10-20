import SwiftUI

/// Placeholder for a screen or panel with no content: symbol, a primary title, a secondary
/// message and an optional prominent action grouped right under the text. Takes its natural size;
/// callers center it in the space they give it.
public struct EmptyStateView: View {
    public let symbol: String
    public let title: String
    public let message: String
    public let actionTitle: String?
    public let action: (() -> Void)?

    public init(
        symbol: String,
        title: String,
        message: String,
        actionTitle: String? = nil,
        action: (() -> Void)? = nil
    ) {
        self.symbol = symbol
        self.title = title
        self.message = message
        self.actionTitle = actionTitle
        self.action = action
    }

    public var body: some View {
        VStack(spacing: DS.Space.l) {
            Image(systemName: symbol)
                .font(.system(size: 40, weight: .regular))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            VStack(spacing: DS.Space.s) {
                Text(title)
                    .font(DS.Font.display)
                    .foregroundStyle(.primary)
                    .accessibilityAddTraits(.isHeader)
                Text(message)
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: 440)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .multilineTextAlignment(.center)
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
            }
        }
        .padding(DS.Space.l)
    }
}
