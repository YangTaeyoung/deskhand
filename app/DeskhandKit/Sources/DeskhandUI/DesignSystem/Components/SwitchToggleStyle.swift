import SwiftUI

/// A switch drawn in SwiftUI at the size and shape of the system switch in a grouped form: the
/// accent track when on in an active window, gray in an inactive one or when off, with a white
/// capsule knob. The label sits on the leading side; VoiceOver reads the control as a toggle.
public struct SwitchToggleStyle: ToggleStyle {
    public init() {}

    public func makeBody(configuration: Configuration) -> some View {
        SwitchRow(configuration: configuration)
    }
}

extension ToggleStyle where Self == SwitchToggleStyle {
    /// `SwitchToggleStyle`.
    public static var dsSwitch: SwitchToggleStyle { SwitchToggleStyle() }
}

private struct SwitchRow: View {
    let configuration: ToggleStyleConfiguration
    @Environment(\.isEnabled) private var isEnabled
    @Environment(\.controlActiveState) private var activeState
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    static let width: CGFloat = 36
    static let height: CGFloat = 16
    static let knobWidth: CGFloat = 22
    static let inset: CGFloat = 2

    private var trackFill: Color {
        guard configuration.isOn else {
            return DS.Color.neutral.opacity(colorScheme == .dark ? 0.36 : 0.22)
        }
        return activeState == .inactive ? DS.Color.neutral.opacity(0.5) : DS.Color.accent
    }

    var body: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            configuration.label
                .frame(maxWidth: .infinity, alignment: .leading)
            Button {
                configuration.isOn.toggle()
            } label: {
                track
            }
            .buttonStyle(.plain)
            .accessibilityHidden(true)
        }
        .opacity(isEnabled ? 1 : 0.5)
        .accessibilityRepresentation {
            Toggle(isOn: configuration.$isOn) { configuration.label }
                .toggleStyle(.switch)
        }
    }

    private var track: some View {
        let knob = Self.height - Self.inset * 2
        return Capsule(style: .continuous)
            .fill(trackFill)
            .overlay(Capsule(style: .continuous).strokeBorder(Color.black.opacity(colorScheme == .dark ? 0.2 : 0.06), lineWidth: 0.5))
            .frame(width: Self.width, height: Self.height)
            .overlay(alignment: configuration.isOn ? .trailing : .leading) {
                Capsule(style: .continuous)
                    .fill(DS.Color.onFill)
                    .shadow(color: .black.opacity(0.28), radius: 0.75, y: 0.5)
                    .frame(width: Self.knobWidth, height: knob)
                    .padding(Self.inset)
            }
            .animation(reduceMotion ? nil : .snappy(duration: 0.18), value: configuration.isOn)
            .contentShape(Capsule())
    }
}
