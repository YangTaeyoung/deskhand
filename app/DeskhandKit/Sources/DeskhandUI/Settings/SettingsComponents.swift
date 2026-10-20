import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Width of the settings window.
enum SettingsMetrics {
    static let width: CGFloat = 660
}

extension SettingsPane {
    /// Height of the pane's content, which the window takes while the pane shows.
    var height: CGFloat {
        switch self {
        case .general: 480
        case .connection: 640
        case .streaming: 420
        case .keyboard: 720
        case .files: 720
        case .privacy: 720
        }
    }
}

/// A pane of the settings window: a grouped form at the pane's size.
struct SettingsForm<Content: View>: View {
    let pane: SettingsPane
    @ViewBuilder let content: Content

    var body: some View {
        Form {
            content
        }
        .formStyle(.grouped)
        .scrollContentBackground(.hidden)
        .background(DS.Color.surface)
        .frame(width: SettingsMetrics.width, height: pane.height)
    }
}

/// A toggle with its title and a secondary line under it.
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
        .toggleStyle(.dsSwitch)
        .accessibilityHint(detail ?? "")
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
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
            if let detail {
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
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
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A notice row inside a form section: a symbol in `tint`, a title and a message.
struct SettingsNotice<Actions: View>: View {
    let symbol: String
    let tint: Color
    let title: String
    let message: String
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
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                actions
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, DS.Space.xs)
        .accessibilityElement(children: .contain)
    }
}

extension SettingsNotice where Actions == EmptyView {
    init(symbol: String, tint: Color, title: String, message: String) {
        self.init(symbol: symbol, tint: tint, title: title, message: message) { EmptyView() }
    }
}

/// A path in monospaced type, shortened in the middle, selectable.
struct SettingsPath: View {
    let path: String

    var body: some View {
        Text(path)
            .font(DS.Font.mono)
            .lineLimit(1)
            .truncationMode(.middle)
            .textSelection(.enabled)
            .help(path)
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
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }
}
