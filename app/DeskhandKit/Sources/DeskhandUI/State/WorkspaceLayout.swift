import Foundation
import Observation

/// Workspace layout the person chose: the selected tab, the inspector and the logs tab's system
/// summary. Kept in UserDefaults and shared by every computer.
@MainActor
@Observable
public final class WorkspaceLayout {
    /// Panes of the inspector.
    public enum InspectorPane: String, CaseIterable, Identifiable, Sendable {
        /// Timeline of human, AI and system actions.
        case activity
        /// Connection, session, displays and capabilities of the computer.
        case info

        public var id: String { rawValue }

        public var title: String {
            switch self {
            case .activity: .ui("inspector.activity", "활동")
            case .info: .ui("inspector.info", "정보")
            }
        }
    }

    private enum Keys {
        static let tab = SettingsKey.Workspace.tab
        static let showsInspector = SettingsKey.Workspace.inspectorVisible
        static let inspectorPane = SettingsKey.Workspace.inspectorPane
        static let showsLogSummary = SettingsKey.Logs.showsSummary
    }

    public var tab: WorkspaceTab {
        didSet { defaults.set(tab.rawValue, forKey: Keys.tab) }
    }

    public var showsInspector: Bool {
        didSet { defaults.set(showsInspector, forKey: Keys.showsInspector) }
    }

    public var inspectorPane: InspectorPane {
        didSet { defaults.set(inspectorPane.rawValue, forKey: Keys.inspectorPane) }
    }

    /// The logs tab shows the system summary above the logs.
    public var showsLogSummary: Bool {
        didSet { defaults.set(showsLogSummary, forKey: Keys.showsLogSummary) }
    }

    /// The sidebar's PC list has keyboard focus. Not kept between launches.
    public var sidebarFocused = false

    /// The last 찾기 (⌘F) request; the visible tab focuses its search field on each.
    public private(set) var findRequest = FindRequest()
    /// Counts 새로 고침 (⌘R) requests; the visible tab reads its content again on each.
    public private(set) var refreshRequests = 0

    public func requestFind() { findRequest = FindRequest(count: findRequest.count + 1, at: .now) }
    public func requestRefresh() { refreshRequests += 1 }

    /// The tab has a search field for 찾기.
    public static func canFind(_ tab: WorkspaceTab) -> Bool {
        [.files, .processes, .logs, .debugger, .qa].contains(tab)
    }

    /// The tab reads its content again on 새로 고침.
    public static func canRefresh(_ tab: WorkspaceTab) -> Bool {
        [.files, .processes, .logs, .debugger].contains(tab)
    }

    /// The inspector is folded away because the workspace is too narrow for it beside the content.
    public private(set) var inspectorAutoHidden = false

    /// Narrowest tab content kept beside the inspector.
    static let minimumContentWidth: CGFloat = 640
    /// Width the inspector takes while it shows.
    static let inspectorWidth: CGFloat = 320
    /// Extra width the workspace needs before a folded inspector comes back.
    static let returnMargin: CGFloat = 40

    /// The inspector shows: the person wants it and the workspace is wide enough.
    public var isInspectorVisible: Bool { showsInspector && !inspectorAutoHidden }

    /// Shows or hides the inspector as the person asks, whatever the width.
    public func setInspectorVisible(_ visible: Bool) {
        showsInspector = visible
        inspectorAutoHidden = false
    }

    public func toggleInspectorVisibility() {
        setInspectorVisible(!isInspectorVisible)
    }

    /// Folds the inspector away when the content beside it would be narrower than
    /// `minimumContentWidth` in a workspace `width` wide, and brings it back once the workspace is
    /// `returnMargin` wider than that.
    public func fit(width: CGFloat) {
        guard showsInspector else {
            inspectorAutoHidden = false
            return
        }
        let content = width - Self.inspectorWidth
        if content < Self.minimumContentWidth {
            inspectorAutoHidden = true
        } else if inspectorAutoHidden, content < Self.minimumContentWidth + Self.returnMargin {
            inspectorAutoHidden = true
        } else {
            inspectorAutoHidden = false
        }
    }

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        tab = defaults.string(forKey: Keys.tab).flatMap(WorkspaceTab.init(rawValue:)) ?? .screen
        showsInspector = defaults.object(forKey: Keys.showsInspector) as? Bool ?? true
        inspectorPane = defaults.string(forKey: Keys.inspectorPane).flatMap(InspectorPane.init(rawValue:)) ?? .activity
        showsLogSummary = defaults.object(forKey: Keys.showsLogSummary) as? Bool ?? true
    }

    /// Shows the inspector on `pane`, or hides it when it already shows `pane`.
    public func toggleInspector(_ pane: InspectorPane) {
        if isInspectorVisible && inspectorPane == pane {
            setInspectorVisible(false)
        } else {
            inspectorPane = pane
            setInspectorVisible(true)
        }
    }
}

/// One 찾기 request: how many came before it and when it was made.
public struct FindRequest: Equatable, Sendable {
    public var count = 0
    public var at: Date?

    /// Made within the last half second, so a search field that appears because of it takes focus.
    public func isRecent(now: Date = .now) -> Bool {
        at.map { now.timeIntervalSince($0) < 0.5 } ?? false
    }
}
