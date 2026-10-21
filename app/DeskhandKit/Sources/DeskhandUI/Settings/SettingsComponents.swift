import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

extension SettingsPane {
    /// Width of every pane.
    public static let width: CGFloat = 660

    /// Size of the pane's content for the store's state; the window takes it while the pane shows.
    @MainActor
    public func size(for store: AppStore) -> CGSize {
        CGSize(width: Self.width, height: height(for: store))
    }

    /// The pane's content height: its sections, the notices and progress rows the state shows,
    /// and the form's margin.
    @MainActor
    func height(for store: AppStore) -> CGFloat {
        let settings = store.settings
        switch self {
        case .general:
            var height: CGFloat = 428
            if case .unavailable = settings.daemonStatus { height += 64 }
            if settings.restart != .idle { height += 36 }
            if settings.loginItemStatus == .requiresApproval { height += 88 }
            if settings.loginItemError != nil { height += 36 }
            return height
        case .connection:
            guard store.hostsLoaded, let host = settings.host(settings.connectionHostID) else { return 240 }
            var height: CGFloat = 640
            if settings.hostErrors[host.hostID] != nil { height += 128 }
            if settings.hostActions[host.hostID] != nil { height += 48 }
            return height
        case .streaming:
            return 436
        case .keyboard:
            return store.screenPreferences.forwardsSystemShortcuts && !SystemShortcutTap.isPermitted ? 820 : 734
        case .files:
            return 696
        case .privacy:
            guard let host = settings.host(settings.scopeHostID) else { return 560 }
            let roots = store.filesPreferences.workRoots(for: host.hostID).count
            guard store.privacyPreferences.scope(for: host.hostID).files == .workRoots else { return 612 }
            return roots == 0 ? 724 : 668 + CGFloat(roots - 1) * 20
        }
    }
}

/// A pane of the settings window: a grouped form at the pane's size.
struct SettingsForm<Content: View>: View {
    let store: AppStore
    let pane: SettingsPane
    @ViewBuilder let content: Content

    var body: some View {
        Form {
            content
        }
        .formStyle(.grouped)
        .frame(width: SettingsPane.width, height: pane.height(for: store))
    }
}

/// A grouped form at the pane's size with `banner` above its sections.
struct SettingsBannerForm<Banner: View, Content: View>: View {
    let store: AppStore
    let pane: SettingsPane
    @ViewBuilder let banner: Banner
    @ViewBuilder let content: Content

    var body: some View {
        VStack(spacing: 0) {
            banner
                .padding(.horizontal, DS.Space.l + DS.Space.xs)
                .padding(.top, DS.Space.xl)
            Form {
                content
            }
            .formStyle(.grouped)
        }
        .frame(width: SettingsPane.width, height: pane.height(for: store))
    }
}

/// A switch with its title and a secondary line under it.
struct SettingsToggle: View {
    let title: String
    let detail: String?
    @Binding var isOn: Bool

    init(_ title: String, detail: String? = nil, isOn: Binding<Bool>) {
        self.title = title
        self.detail = detail
        _isOn = isOn
    }

    var body: some View {
        Toggle(isOn: $isOn) {
            SettingsLabel(title, detail: detail)
        }
        .toggleStyle(.switch)
    }
}

/// The title of a row and a secondary line under it.
struct SettingsLabel: View {
    let title: String
    let detail: String?

    init(_ title: String, detail: String? = nil) {
        self.title = title
        self.detail = detail
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            Text(title)
            if let detail {
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

/// The value of a labeled row, in the secondary text color.
struct SettingsValue: View {
    let text: String
    var monospaced = false

    init(_ text: String, monospaced: Bool = false) {
        self.text = text
        self.monospaced = monospaced
    }

    var body: some View {
        Text(text)
            .font(monospaced ? DS.Font.mono : DS.Font.body)
            .foregroundStyle(DS.Status.neutralText)
            .lineLimit(1)
            .truncationMode(.middle)
            .textSelection(.enabled)
            .help(text)
    }
}

/// Text under a form section.
struct SettingsFootnote: View {
    let text: String

    init(_ text: String) {
        self.text = text
    }

    var body: some View {
        Text(text)
            .font(DS.Font.caption)
            .foregroundStyle(DS.Status.neutralText)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A notice inside a form section, or with `tinted` a banner on a `tint` fill above a form: a
/// symbol in `tint`, a title and a message.
struct SettingsNotice<Actions: View>: View {
    let symbol: String
    let tint: Color
    let title: String
    let message: String
    var tinted = false
    @ViewBuilder var actions: Actions

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Image(systemName: symbol)
                .foregroundStyle(tint)
                .font(DS.Font.headline)
                .frame(width: DS.Space.l)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.headline)
                Text(message)
                    .font(DS.Font.caption)
                    .foregroundStyle(tinted ? AnyShapeStyle(.primary) : AnyShapeStyle(DS.Status.neutralText))
                    .fixedSize(horizontal: false, vertical: true)
                actions
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, DS.Space.xs)
        .padding(.horizontal, tinted ? DS.Space.m : 0)
        .padding(.vertical, tinted ? DS.Space.s : 0)
        .background(tinted ? tint.opacity(DS.Status.badgeOpacity) : .clear,
                    in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .accessibilityElement(children: .contain)
    }
}

extension SettingsNotice where Actions == EmptyView {
    init(symbol: String, tint: Color, title: String, message: String, tinted: Bool = false) {
        self.init(symbol: symbol, tint: tint, title: title, message: message, tinted: tinted) { EmptyView() }
    }
}

/// A compact placeholder for a pane with nothing to show: symbol, title, message and an action.
struct SettingsEmptyState: View {
    let symbol: String
    let title: String
    let message: String
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        VStack(spacing: DS.Space.m) {
            Image(systemName: symbol)
                .font(.system(size: 28, weight: .regular))
                .foregroundStyle(DS.Status.neutralText)
                .accessibilityHidden(true)
            VStack(spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.headline)
                    .accessibilityAddTraits(.isHeader)
                Text(message)
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 360)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .buttonStyle(.borderedProminent)
                    .tint(DS.Status.selectionFill)
            }
        }
        .padding(DS.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// The row that picks which computer a per-computer section shows.
struct SettingsHostPicker: View {
    let hosts: [Host]
    @Binding var selection: String?

    var body: some View {
        Picker(String.ui("settings.host.picker", "컴퓨터"), selection: $selection) {
            ForEach(hosts, id: \.hostID) { host in
                Text(host.name).tag(Optional(host.hostID))
            }
        }
        .pickerStyle(.menu)
    }
}

/// A short inline failure under a control.
struct SettingsError: View {
    let message: String

    var body: some View {
        Label(message, systemImage: "exclamationmark.triangle.fill")
            .font(DS.Font.caption)
            .foregroundStyle(DS.Status.dangerText)
            .fixedSize(horizontal: false, vertical: true)
    }
}

/// A spinner and a caption for work in progress inside a row.
struct SettingsProgress: View {
    let title: String

    var body: some View {
        HStack(spacing: DS.Space.s) {
            ProgressView()
                .controlSize(.small)
            Text(title)
                .font(DS.Font.caption)
                .foregroundStyle(DS.Status.neutralText)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A small capsule label beside a section title, such as `적용 전`.
struct SettingsBadge: View {
    let title: String
    let tint: Color
    let text: Color

    var body: some View {
        Text(title)
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(text)
            .padding(.horizontal, DS.Space.s)
            .frame(height: 20)
            .background(tint.opacity(DS.Status.badgeOpacity), in: Capsule())
    }
}
