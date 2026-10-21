import SwiftUI

/// A small spinner with a caption: the loading state of a list or a detail pane.
struct LoadingLine: View {
    let text: String

    var body: some View {
        HStack(spacing: DS.Space.s) {
            ProgressView().controlSize(.small)
            Text(text)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Gray placeholder rows in the shape of a table while its first rows load: a header, then rows
/// striped like `DataTable` with a bar of varying length in each column. Tables use this as their
/// loading state; VoiceOver reads `label`.
struct TableSkeleton: View {
    enum Column {
        case fixed(CGFloat)
        /// Takes the width the fixed columns leave.
        case flexible
    }

    let label: String
    let columns: [Column]
    /// A square before the first column, for tables whose rows start with a symbol.
    var leadingSymbol = false

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    private static let fractions: [CGFloat] = [0.62, 0.48, 0.71, 0.55, 0.4, 0.66, 0.5, 0.58, 0.44, 0.69, 0.52, 0.6]
    private static let spacing = DS.Space.l

    var body: some View {
        let rowHeight = DS.TableMetrics(dynamicTypeSize).rowHeight
        GeometryReader { proxy in
            let rows = max(1, Int((proxy.size.height - rowHeight) / rowHeight))
            let widths = widths(in: proxy.size.width)
            VStack(alignment: .leading, spacing: 0) {
                row(widths, index: nil)
                    .frame(height: rowHeight)
                Rectangle().fill(DS.Color.separator).frame(height: 1)
                ForEach(0..<rows, id: \.self) { index in
                    row(widths, index: index)
                        .frame(height: rowHeight)
                        .background {
                            if index % 2 == 1 {
                                RoundedRectangle(cornerRadius: 5, style: .continuous)
                                    .fill(DS.Color.neutral.opacity(0.05))
                                    .padding(.horizontal, DS.Space.xs)
                            }
                        }
                }
            }
        }
        .clipped()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(label)
    }

    /// Each column's width: fixed widths as given, flexible columns sharing what is left.
    private func widths(in total: CGFloat) -> [CGFloat] {
        let symbol: CGFloat = leadingSymbol ? 14 + Self.spacing : 0
        let fixed = columns.reduce(CGFloat(0)) { sum, column in
            if case .fixed(let width) = column { return sum + width }
            return sum
        }
        let gaps = CGFloat(max(0, columns.count - 1)) * Self.spacing + 2 * Self.spacing + symbol
        let flexibleCount = CGFloat(columns.filter { if case .flexible = $0 { true } else { false } }.count)
        let flexible = flexibleCount > 0 ? max(40, (total - fixed - gaps) / flexibleCount) : 0
        return columns.map { column in
            if case .fixed(let width) = column { return width }
            return flexible
        }
    }

    /// The header when `index` is nil, otherwise row `index`.
    private func row(_ widths: [CGFloat], index: Int?) -> some View {
        HStack(spacing: Self.spacing) {
            if leadingSymbol {
                bar(width: 14, height: index == nil ? 0 : 14)
            }
            ForEach(Array(widths.enumerated()), id: \.offset) { column, width in
                let fraction = index.map { Self.fractions[($0 + column * 5) % Self.fractions.count] } ?? 0.4
                bar(width: max(12, min(width, 220) * fraction), height: index == nil ? 9 : 10)
                    .frame(width: width, alignment: .leading)
            }
        }
        .padding(.horizontal, Self.spacing)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func bar(width: CGFloat, height: CGFloat) -> some View {
        RoundedRectangle(cornerRadius: height / 2.5, style: .continuous)
            .fill(DS.Color.neutral.opacity(0.16))
            .frame(width: width, height: height)
    }
}
