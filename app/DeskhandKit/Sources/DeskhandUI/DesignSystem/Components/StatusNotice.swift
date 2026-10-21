import SwiftUI

/// Inline status message with a symbol (or a spinner when `symbol` is nil), tinted background,
/// and an error code line when `code` is given.
struct StatusNotice: View {
    let symbol: String?
    let tint: Color
    let title: String
    let message: String
    var code: String?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Group {
                if let symbol {
                    Image(systemName: symbol)
                        .foregroundStyle(tint)
                } else {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .font(DS.Font.headline)
            .frame(width: DS.Space.l)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.headline)
                Text(message)
                    .font(DS.Font.body)
                    .foregroundStyle(DS.Status.secondaryOnTint)
                    .fixedSize(horizontal: false, vertical: true)
                if let code {
                    ErrorCodeLine(code: code)
                        .padding(.top, DS.Space.xs)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(DS.Space.m)
        .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(tint.opacity(0.24), lineWidth: 1)
        )
        .accessibilityElement(children: .combine)
    }
}
