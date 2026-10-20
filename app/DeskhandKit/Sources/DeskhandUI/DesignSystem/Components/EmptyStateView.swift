import SwiftUI

/// Centered placeholder for a screen or panel with no content: symbol, title, message and an
/// optional prominent action.
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
        ContentUnavailableView {
            Label(title, systemImage: symbol)
        } description: {
            Text(message)
        } actions: {
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
            }
        }
    }
}
