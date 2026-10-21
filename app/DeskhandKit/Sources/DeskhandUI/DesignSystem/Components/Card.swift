import SwiftUI

extension View {
    /// Places the view on a raised panel: `DS.Color.surfaceRaised` fill, large radius and a hairline border.
    public func dsCard(padding: CGFloat = DS.Space.l) -> some View {
        self
            .padding(padding)
            .background(DS.Color.surfaceRaised, in: RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous)
                    .strokeBorder(DS.Color.separator, lineWidth: 1)
            )
    }
}
