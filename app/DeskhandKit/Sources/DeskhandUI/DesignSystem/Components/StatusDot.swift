import SwiftUI

/// Colored dot for a `ConnectionState`, optionally followed by its label.
///
/// Offline draws a hollow ring, connecting and reconnecting add a pulsing halo (steady when
/// Reduce Motion is on), and every other state is a solid dot. On a selected row's highlight the
/// dot keeps its state color and gains a white ring.
public struct StatusDot: View {
    public let state: ConnectionState
    public let showsLabel: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.backgroundProminence) private var prominence

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

    private var tint: Color { state.tint }
    private var onHighlight: Bool { prominence == .increased }

    private var dot: some View {
        ZStack {
            if state.isTransitional {
                Circle()
                    .fill(tint)
                    .phaseAnimator(reduceMotion ? [0.3] : [0.38, 0.1]) { halo, opacity in
                        halo.opacity(opacity)
                    } animation: { _ in
                        .easeInOut(duration: 0.9)
                    }
            }
            if state == .offline {
                Circle()
                    .strokeBorder(onHighlight ? DS.Color.onFill : tint, lineWidth: 1.5)
                    .frame(width: Self.dotSize, height: Self.dotSize)
            } else {
                Circle()
                    .fill(tint)
                    .frame(width: Self.dotSize, height: Self.dotSize)
                    .overlay(Circle().strokeBorder(DS.Color.onFill, lineWidth: onHighlight ? 1.5 : 0))
            }
        }
        .frame(width: Self.frameSize, height: Self.frameSize)
    }
}
