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

    private var main: some View {
        VStack(spacing: 0) {
            WorkspaceTabBar(layout: store.layout)
            ControlBanner(store: store, host: host)
            TabPlaceholderView(tab: store.layout.tab)
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: bannerKind)
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
    }

    private struct BannerState: Equatable {
        let hostID: String
        let kind: ControlBannerKind
    }
}

/// Segmented control of the seven workspace tabs (⌘1–⌘7).
struct WorkspaceTabBar: View {
    /// Height of the tab bar, shared with the inspector header beside it.
    static let height: CGFloat = 44

    @Bindable var layout: WorkspaceLayout

    var body: some View {
        ViewThatFits(in: .horizontal) {
            picker(iconsOnly: false)
            picker(iconsOnly: true)
        }
        .padding(.horizontal, DS.Space.l)
        .frame(minWidth: 0, maxWidth: .infinity)
        .frame(height: Self.height)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(DS.Color.separator)
                .frame(height: 1)
        }
    }

    /// Segments titled with the tab names, or with their symbols when the column is narrow.
    private func picker(iconsOnly: Bool) -> some View {
        Picker(String.ui("tab.picker", "작업 공간 탭"), selection: $layout.tab) {
            ForEach(WorkspaceTab.allCases) { tab in
                Group {
                    if iconsOnly {
                        Image(systemName: tab.symbol)
                            .accessibilityLabel(tab.title)
                    } else {
                        Text(tab.title)
                    }
                }
                .help(String.ui("tab.help", "\(tab.title) (⌘\(tab.number))"))
                .tag(tab)
            }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .fixedSize()
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
