import SwiftUI

/// Inline error: what failed (`message`), how to fix it (`fix`), the error code when known,
/// and a retry button when `retry` is given.
public struct ErrorBanner: View {
    public let message: String
    public let fix: String?
    public let code: String?
    public let retry: (() -> Void)?

    nonisolated static let symbols = ["exclamationmark.octagon.fill", "arrow.clockwise"]

    public init(message: String, fix: String? = nil, code: String? = nil, retry: (() -> Void)? = nil) {
        self.message = message
        self.fix = fix
        self.code = code
        self.retry = retry
    }

    public var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Image(systemName: Self.symbols[0])
                .foregroundStyle(DS.Color.danger)
                .font(DS.Font.headline)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(message)
                    .font(DS.Font.headline)
                    .foregroundStyle(.primary)
                if let fix {
                    Text(fix)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                }
                if let code {
                    Text(code)
                        .font(DS.Font.mono)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            if let retry {
                Button(action: retry) {
                    Label(String.ui("action.retry", "다시 시도"), systemImage: Self.symbols[1])
                }
                .controlSize(.regular)
            }
        }
        .padding(DS.Space.m)
        .background(DS.Color.danger.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.danger.opacity(0.28), lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
    }
}
