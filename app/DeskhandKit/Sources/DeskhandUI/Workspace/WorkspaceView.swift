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
                store.debugger(for: host).start()
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
        let debugger = store.debugger(for: host)
        let pausedElsewhere = store.layout.tab == .debugger ? nil : debugger.pausedElsewhere
        return VStack(spacing: 0) {
            WorkspaceTabBar(layout: store.layout, badges: debugger.openSessions.contains { !$0.pausedTargets.isEmpty } ? [.debugger] : [])
                .layoutPriority(1)
            ControlBanner(store: store, host: host)
                .layoutPriority(1)
            if let pausedElsewhere {
                DebuggerPausedBanner(pause: pausedElsewhere) {
                    debugger.reveal(pausedElsewhere)
                    store.layout.tab = .debugger
                } dismiss: {
                    debugger.dismiss(pausedElsewhere)
                }
                .layoutPriority(1)
                .transition(.opacity)
            }
            tabContent
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                .clipped()
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: bannerKind)
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: pausedElsewhere)
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
        case .debugger:
            DebuggerView(store: store, host: host, chrome: chrome)
                .id(host.hostID)
        case .qa:
            TabPlaceholderView(tab: store.layout.tab)
        }
    }

    private struct BannerState: Equatable {
        let hostID: String
        let kind: ControlBannerKind
    }
}

/// Leading row of the seven workspace tabs (⌘1–⌘7): symbol and title, or symbols only when
/// the column is narrow; tabs in `badges` carry a paused badge.
struct WorkspaceTabBar: View {
    /// Height of the tab bar, shared with the inspector header beside it.
    static let height: CGFloat = 44

    @Bindable var layout: WorkspaceLayout
    var badges: Set<WorkspaceTab> = []

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
                TabButton(title: tab.title, symbol: tab.symbol, iconOnly: iconsOnly, isSelected: layout.tab == tab,
                          badge: badges.contains(tab) && layout.tab != tab) {
                    layout.tab = tab
                }
                .help(badges.contains(tab) && tab == .debugger
                      ? String.ui("tab.help.debuggerPaused", "\(tab.title) (⌘\(String(tab.number))) · 일시정지된 앱이 있습니다")
                      : String.ui("tab.help", "\(tab.title) (⌘\(tab.number))"))
            }
        }
        .fixedSize()
    }
}

/// One tab of a tab row: the selected tab is drawn in the accent color on a tinted capsule,
/// others in the secondary color with a hover wash; `badge` adds an orange pause dot.
struct TabButton: View {
    let title: String
    var symbol: String?
    var iconOnly = false
    let isSelected: Bool
    var badge = false
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: DS.Space.xs) {
                if let symbol {
                    Image(systemName: symbol)
                        .imageScale(.medium)
                        .overlay(alignment: .topTrailing) {
                            if badge, iconOnly { PausedDot().offset(x: DS.Space.xs, y: -DS.Space.xs) }
                        }
                }
                if !iconOnly {
                    Text(title)
                        .lineLimit(1)
                }
                if badge, !iconOnly { PausedDot() }
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
        .accessibilityValue(badge ? String.ui("tab.debuggerPaused.a11y", "일시정지된 앱 있음") : "")
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

/// An orange circle with a pause symbol, marking a tab whose app is paused.
struct PausedDot: View {
    var body: some View {
        Image(systemName: "pause.fill")
            .font(.system(size: 6, weight: .heavy))
            .foregroundStyle(DS.Color.onFill)
            .frame(width: 12, height: 12)
            .background(DS.Color.warning, in: Circle())
            .accessibilityHidden(true)
    }
}

/// A pause of a debugged app while another tab is in front: the app, where it stopped, a button
/// to the debugger tab and one to dismiss it until the next pause.
struct DebuggerPausedBanner: View {
    let pause: PausedElsewhere
    let show: () -> Void
    let dismiss: () -> Void

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Image(systemName: "pause.circle.fill")
                .font(.system(size: 14))
                .foregroundStyle(DS.Color.warning)
                .accessibilityHidden(true)
            Text(message)
                .font(DS.Font.body)
                .lineLimit(1)
                .truncationMode(.middle)
            Text("·")
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Button(String.ui("debugger.pausedBanner.show", "디버거 보기"), action: show)
                .buttonStyle(.link)
                .tint(DS.Color.accent)
            Spacer(minLength: DS.Space.s)
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(String.ui("debugger.pausedBanner.dismiss", "이 알림 닫기"))
            .accessibilityLabel(String.ui("debugger.pausedBanner.dismiss", "이 알림 닫기"))
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.warning.opacity(0.1))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }

    private var message: String {
        let name = pause.appName
        if let location = pause.location {
            return .ui("debugger.pausedBanner", "‘\(name)’ 앱이 \(location)에서 멈췄습니다")
        }
        return .ui("debugger.pausedBanner.noLocation", "‘\(name)’ 앱이 멈췄습니다")
    }
}
