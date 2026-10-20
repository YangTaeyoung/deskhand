import SwiftUI

/// Colored dot for a `ConnectionState`, optionally followed by its label.
///
/// Offline draws a hollow ring, connecting and reconnecting add a pulsing halo (steady when
/// Reduce Motion is on), and every other state is a solid dot.
public struct StatusDot: View {
    public let state: ConnectionState
    public let showsLabel: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private static let dotSize = DS.Space.s
    private static let frameSize = DS.Space.m

    public init(state: ConnectionState, showsLabel: Bool = false) {
        self.state = state
        self.showsLabel = showsLabel
    }

    public var body: some View {
        HStack(spacing: DS.Space.s) {
            dot
            if showsLabel {
                Text(state.title)
                    .font(DS.Font.body)
                    .foregroundStyle(.primary)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("a11y.connectionState", "연결 상태: \(state.title)"))
    }

    private var dot: some View {
        ZStack {
            if state.isTransitional {
                Circle()
                    .fill(state.tint)
                    .phaseAnimator(reduceMotion ? [0.3] : [0.38, 0.1]) { halo, opacity in
                        halo.opacity(opacity)
                    } animation: { _ in
                        .easeInOut(duration: 0.9)
                    }
            }
            if state == .offline {
                Circle()
                    .strokeBorder(state.tint, lineWidth: 1.5)
                    .frame(width: Self.dotSize, height: Self.dotSize)
            } else {
                Circle()
                    .fill(state.tint)
                    .frame(width: Self.dotSize, height: Self.dotSize)
            }
        }
        .frame(width: Self.frameSize, height: Self.frameSize)
    }
}
