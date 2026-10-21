import SwiftUI

/// A status as a tinted capsule with its symbol and word in the status color, of one width
/// unless `large`. A spinner replaces the symbol while `spinning`; on a selected row
/// (`inverted`) it draws white on an outline.
struct StatusChip: View {
    static let width: CGFloat = 64

    let title: String
    let symbol: String
    let tint: Color
    let text: Color
    var spinning = false
    var large = false
    var inverted = false

    init(title: String, symbol: String, tint: Color, text: Color, spinning: Bool = false, large: Bool = false, inverted: Bool = false) {
        self.title = title
        self.symbol = symbol
        self.tint = tint
        self.text = text
        self.spinning = spinning
        self.large = large
        self.inverted = inverted
    }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            if spinning {
                ProgressView()
                    .controlSize(.mini)
                    .frame(width: 10, height: 10)
            } else {
                Image(systemName: symbol)
                    .font(.system(size: large ? 11 : 9, weight: .bold))
            }
            Text(title)
                .font(.system(size: large ? 12 : 11, weight: .semibold))
                .lineLimit(1)
        }
        .foregroundStyle(inverted ? DS.Color.onFill : text)
        .padding(.horizontal, DS.Space.s)
        .frame(minWidth: large ? nil : Self.width)
        .frame(height: large ? 22 : 18)
        .chipBackground(tint, selected: inverted)
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
    }
}
