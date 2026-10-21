import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Bar above the remote screen: the display picker (or the single display), what a click or
/// typing does right now, and menus for stream quality, keyboard handling and statistics.
struct ScreenHeader: View {
    static let height: CGFloat = 40

    @Bindable var preferences: ScreenPreferences
    let host: Host
    let display: Int
    let owner: ControlOwner
    let live: Bool
    let captured: Bool

    /// How much the bar shows: everything, without the display detail and long status, or
    /// short display numbers and icon-only menus.
    private enum Density: CaseIterable {
        case full
        case compact
        case tight
    }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            ForEach(Density.allCases, id: \.self) { density in
                row(density)
            }
        }
        .padding(.horizontal, DS.Space.l)
        .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
        .frame(height: Self.height)
        .clipped()
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
    }

    private func row(_ density: Density) -> some View {
        HStack(spacing: DS.Space.m) {
            displayControl(density)
            Spacer(minLength: DS.Space.s)
            if density != .tight {
                InputStatus(owner: owner, live: live, captured: captured, compact: density == .compact)
            }
            HStack(spacing: DS.Space.xs) {
                presetMenu(iconOnly: density == .tight)
                keyboardMenu(iconOnly: density == .tight)
                statisticsToggle
            }
        }
        .fixedSize(horizontal: false, vertical: true)
    }

    @ViewBuilder
    private func displayControl(_ density: Density) -> some View {
        if host.displays.count > 1 {
            HStack(spacing: DS.Space.s) {
                Picker(String.ui("screen.display.picker", "디스플레이"), selection: Binding(
                    get: { display },
                    set: { preferences.setDisplay($0, for: host.hostID) })) {
                    ForEach(host.displays, id: \.index) { info in
                        Text(density == .tight ? "\(info.index + 1)" : info.title)
                            .accessibilityLabel(info.title)
                            .tag(info.index)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
                .help(String.ui("screen.display.help", "보여 줄 디스플레이를 고릅니다. 바꾸면 화면을 다시 불러옵니다."))
                if density == .full, let info = host.displays.first(where: { $0.index == display }) {
                    Text(info.detail)
                        .font(DS.Font.caption)
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .fixedSize()
                }
            }
        } else {
            let info = host.displays.first
            HStack(spacing: DS.Space.xs) {
                Image(systemName: "display")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Text(info?.title ?? DisplayInfo.title(index: display))
                if density == .full, let info {
                    Text(info.detail)
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                }
            }
            .font(DS.Font.caption)
            .lineLimit(1)
            .fixedSize()
            .accessibilityElement(children: .combine)
        }
    }

    private func presetMenu(iconOnly: Bool) -> some View {
        Menu {
            Picker(String.ui("screen.preset.picker", "화질"), selection: $preferences.preset) {
                ForEach(StreamPreset.allCases) { preset in
                    Text(String.ui("screen.preset.item", "\(preset.title) — \(preset.detail)")).tag(preset)
                }
            }
            .pickerStyle(.inline)
        } label: {
            Label {
                Text(preferences.preset.title)
            } icon: {
                Image(systemName: "dial.medium")
                    .accessibilityHidden(true)
            }
            .labelStyle(MenuLabelStyle(iconOnly: iconOnly))
        }
        .menuStyle(.button)
        .buttonStyle(.borderless)
        .fixedSize()
        .help(String.ui("screen.preset.help", "화질: 선명은 원본 해상도, 균형은 최대 2560 폭, 절약은 최대 1920 폭과 낮은 비트레이트"))
        .accessibilityLabel(String.ui("screen.preset.a11y", "화질, \(preferences.preset.title)"))
    }

    private func keyboardMenu(iconOnly: Bool) -> some View {
        Menu {
            Picker(String.ui("screen.keyboard.mode", "입력 방식"), selection: $preferences.keyboardMode) {
                ForEach(KeyboardMode.allCases) { mode in
                    Text(String.ui("screen.keyboard.modeItem", "\(mode.title) — \(mode.detail)")).tag(mode)
                }
            }
            .pickerStyle(.inline)
            Divider()
            Toggle(String.ui("screen.keyboard.swap", "⌘를 Ctrl로, ⌃을 Windows 키로 보내기"), isOn: $preferences.swapCommandControl)
            Toggle(String.ui("screen.keyboard.hangul", "오른쪽 ⌥을 한/영 키로 보내기"), isOn: $preferences.rightOptionHangul)
            Toggle(String.ui("screen.keyboard.hanja", "오른쪽 ⌘를 한자 키로 보내기"), isOn: $preferences.rightCommandHanja)
            Divider()
            Toggle(String.ui("screen.keyboard.system", "시스템 단축키도 원격으로 보내기 (⌘Q·⌘H·⌘Tab)"), isOn: $preferences.forwardsSystemShortcuts)
            if preferences.forwardsSystemShortcuts, !SystemShortcutTap.isPermitted {
                Text(String.ui("screen.keyboard.system.permission", "⌘Tab은 손쉬운 사용 권한이 있어야 보낼 수 있습니다"))
                Button(String.ui("screen.keyboard.system.openSettings", "손쉬운 사용 설정 열기…")) {
                    SystemSettingsLink.openAccessibility()
                }
            }
        } label: {
            Label {
                Text(String.ui("screen.keyboard", "키보드"))
            } icon: {
                Image(systemName: "keyboard")
                    .accessibilityHidden(true)
            }
            .labelStyle(MenuLabelStyle(iconOnly: iconOnly))
        }
        .menuStyle(.button)
        .buttonStyle(.borderless)
        .fixedSize()
        .help(String.ui("screen.keyboard.help", "키보드 입력 방식과 키 바꾸기"))
    }

    private var statisticsToggle: some View {
        Button {
            preferences.showsStatistics.toggle()
        } label: {
            Label(String.ui("screen.statistics", "통계"), systemImage: "chart.bar.xaxis")
                .labelStyle(.iconOnly)
                .foregroundStyle(preferences.showsStatistics ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                .frame(width: 28, height: 24)
                .background(preferences.showsStatistics ? DS.Color.accent.opacity(0.14) : .clear,
                            in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(preferences.showsStatistics
              ? String.ui("screen.statistics.hide", "스트림 통계 가리기")
              : String.ui("screen.statistics.show", "스트림 통계 보기: fps, 지연, 비트레이트, 인코더"))
        .accessibilityLabel(String.ui("screen.statistics", "통계"))
        .accessibilityAddTraits(preferences.showsStatistics ? [.isSelected] : [])
    }
}

/// Title and icon of a header menu, or the icon alone.
private struct MenuLabelStyle: LabelStyle {
    let iconOnly: Bool

    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: DS.Space.xs) {
            configuration.icon
            if !iconOnly {
                configuration.title
            }
        }
    }
}

/// What input does right now: typing goes to the PC (with the way out), a click takes control
/// from the AI, or a click starts sending input.
private struct InputStatus: View {
    let owner: ControlOwner
    let live: Bool
    let captured: Bool
    let compact: Bool

    var body: some View {
        if live {
            content
                .font(DS.Font.caption)
                .lineLimit(1)
                .fixedSize()
                .accessibilityElement(children: .combine)
        }
    }

    @ViewBuilder
    private var content: some View {
        if captured {
            HStack(spacing: DS.Space.s) {
                Label(String.ui("screen.input.captured", "입력 중"), systemImage: "keyboard.fill")
                    .foregroundStyle(DS.Status.humanText)
                    .fontWeight(.semibold)
                if !compact {
                    Text(String.ui("screen.input.release", "⌃⌥를 눌렀다 떼면 해제"))
                        .foregroundStyle(DS.Status.secondaryOnTint)
                }
            }
            .padding(.horizontal, DS.Space.s)
            .padding(.vertical, DS.Space.xs)
            .background(DS.Color.human.opacity(DS.Status.badgeOpacity), in: Capsule())
        } else if owner == .ai {
            Label(compact
                  ? String.ui("screen.input.ai.short", "클릭하면 가져오기")
                  : String.ui("screen.input.ai", "화면을 클릭하면 제어권을 가져옵니다"),
                  systemImage: "hand.tap")
                .foregroundStyle(DS.Status.aiText)
        } else {
            Label(compact
                  ? String.ui("screen.input.idle.short", "클릭하여 조작")
                  : String.ui("screen.input.idle", "화면을 클릭하면 키보드와 마우스를 보냅니다"),
                  systemImage: "cursorarrow.click")
                .foregroundStyle(.secondary)
        }
    }
}
