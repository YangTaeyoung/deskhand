import SwiftUI

/// One row of a `KeyValueGrid`. `monospaced` renders the value in `DS.Font.mono` for paths and IDs.
public struct KeyValueRow: Hashable, Sendable {
    public let key: String
    public let value: String
    public let monospaced: Bool

    public init(_ key: String, _ value: String, monospaced: Bool = false) {
        self.key = key
        self.value = value
        self.monospaced = monospaced
    }
}

/// Two-column grid of left-aligned secondary labels and selectable values, as in an inspector.
/// Long values wrap instead of truncating.
public struct KeyValueGrid: View {
    public let rows: [KeyValueRow]

    public init(_ rows: [KeyValueRow]) {
        self.rows = rows
    }

    public var body: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.s) {
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                GridRow {
                    Text(row.key)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .gridColumnAlignment(.leading)
                        .lineLimit(1)
                    Text(row.value)
                        .font(row.monospaced ? DS.Font.mono : DS.Font.body)
                        .foregroundStyle(.primary)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .accessibilityElement(children: .combine)
            }
        }
    }
}
