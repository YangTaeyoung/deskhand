import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// How the workspace draws window chrome.
public enum WorkspaceChrome: Sendable {
    /// System toolbar items and inspector column.
    case native
    /// The inspector as a fixed-width column beside the content, without toolbar items, for
    /// offscreen snapshots; `WorkspaceToolbarStrip` draws the toolbar.
    case standIn
}

/// A computer's workspace: the tab bar, the control banner, the selected tab and the inspector.
/// The toolbar carries the computer's session, stream quality and control actions.
public struct WorkspaceView: View {
    let store: AppStore
    let host: Host
    let chrome: WorkspaceChrome
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(store: AppStore, host: Host, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.host = host
        self.chrome = chrome
    }

    private var bannerKind: ControlBannerKind {
        ControlBannerKind(host: host, control: store.control(for: host.hostID))
    }

    public var body: some View {
        framed
            .task(id: host.hostID) {
                await store.loadControl(for: host.hostID)
                await store.loadTimeline(for: host.hostID)
            }
            .onChange(of: BannerState(hostID: host.hostID, kind: bannerKind)) { old, new in
                guard old.hostID == new.hostID else { return }
                AccessibilityNotification.Announcement(new.kind.title).post()
            }
    }

    @ViewBuilder
    private var framed: some View {
        @Bindable var layout = store.layout
        switch chrome {
        case .native:
            main
                .toolbar { WorkspaceToolbar(store: store, host: host) }
                .inspector(isPresented: $layout.showsInspector) {
                    TimelineInspector(store: store, host: host)
                        .inspectorColumnWidth(min: 280, ideal: 320, max: 440)
                }
        case .standIn:
            HStack(spacing: 0) {
                main
                if layout.showsInspector {
                    Divider()
                    TimelineInspector(store: store, host: host)
                        .frame(width: 320)
                }
            }
        }
    }

    /// Layout rule: the tab bar has a fixed height and the control banner its ideal height; the
    /// tab content region below them reports a zero minimum size to the split view and is
    /// clipped to its frame, whatever the tab draws.
    private var main: some View {
        VStack(spacing: 0) {
            WorkspaceTabBar(layout: store.layout)
                .layoutPriority(1)
            ControlBanner(store: store, host: host)
                .layoutPriority(1)
            tabContent
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                .clipped()
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: bannerKind)
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
    }

    @ViewBuilder
    private var tabContent: some View {
        switch store.layout.tab {
        case .screen:
            ScreenView(store: store, host: host, chrome: chrome)
                .id(host.hostID)
        case .terminal:
            TerminalView(store: store, host: host, chrome: chrome)
                .id(host.hostID)
        case .files:
            FilesView(store: store, host: host)
                .id(host.hostID)
        case .processes:
            ProcessesView(store: store, host: host, chrome: chrome)
                .id(host.hostID)
        case .logs:
            LogsView(store: store, host: host, chrome: chrome)
                .id(host.hostID)
        case .debugger, .qa:
            TabPlaceholderView(tab: store.layout.tab)
        }
    }

    private struct BannerState: Equatable {
        let hostID: String
        let kind: ControlBannerKind
    }
}

/// Leading row of the seven workspace tabs (⌘1–⌘7): symbol and title, or symbols only when
/// the column is narrow.
struct WorkspaceTabBar: View {
    /// Height of the tab bar, shared with the inspector header beside it.
    static let height: CGFloat = 44

    @Bindable var layout: WorkspaceLayout

    var body: some View {
        ViewThatFits(in: .horizontal) {
            tabs(iconsOnly: false)
            tabs(iconsOnly: true)
        }
        .padding(.horizontal, DS.Space.l)
        .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
        .frame(height: Self.height)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("tab.picker", "작업 공간 탭"))
    }

    private func tabs(iconsOnly: Bool) -> some View {
        HStack(spacing: DS.Space.xs) {
            ForEach(WorkspaceTab.allCases) { tab in
                TabButton(title: tab.title, symbol: tab.symbol, iconOnly: iconsOnly, isSelected: layout.tab == tab) {
                    layout.tab = tab
                }
                .help(String.ui("tab.help", "\(tab.title) (⌘\(tab.number))"))
            }
        }
        .fixedSize()
    }
}

/// One tab of a tab row: the selected tab is drawn in the accent color on a tinted capsule,
/// others in the secondary color with a hover wash.
struct TabButton: View {
    let title: String
    var symbol: String?
    var iconOnly = false
    let isSelected: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: DS.Space.xs) {
                if let symbol {
                    Image(systemName: symbol)
                        .imageScale(.medium)
                }
                if !iconOnly {
                    Text(title)
                        .lineLimit(1)
                }
            }
            .font(isSelected ? DS.Font.body.weight(.semibold) : DS.Font.body)
            .foregroundStyle(isSelected ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
            .padding(.horizontal, iconOnly ? DS.Space.s : DS.Space.m)
            .frame(height: 28)
            .background(background, in: Capsule())
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityLabel(title)
        .accessibilityAddTraits(isSelected ? [.isSelected] : [])
    }

    private var background: AnyShapeStyle {
        if isSelected { return AnyShapeStyle(DS.Color.accent.opacity(0.14)) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.12)) : AnyShapeStyle(.clear)
    }
}

/// Placeholder of a tab whose content is not built yet: what the tab will do.
struct TabPlaceholderView: View {
    let tab: WorkspaceTab

    var body: some View {
        VStack(spacing: 0) {
            EmptyStateView(symbol: tab.symbol, title: tab.heading, message: tab.summary)
            Text(String.ui("tab.placeholder.badge", "준비 중인 탭입니다"))
                .font(DS.Font.caption.weight(.medium))
                .foregroundStyle(.secondary)
                .padding(.horizontal, DS.Space.m)
                .padding(.vertical, DS.Space.xs)
                .background(DS.Color.neutral.opacity(0.12), in: Capsule())
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .combine)
    }
}
