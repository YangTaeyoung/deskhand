import Foundation
import Observation

/// Workspace layout the person chose: the selected tab and the inspector. Kept in UserDefaults
/// and shared by every computer.
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

    enum Keys {
        static let tab = "workspaceTab"
        static let showsInspector = "inspectorVisible"
        static let inspectorPane = "inspectorPane"
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

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        tab = defaults.string(forKey: Keys.tab).flatMap(WorkspaceTab.init(rawValue:)) ?? .screen
        showsInspector = defaults.object(forKey: Keys.showsInspector) as? Bool ?? true
        inspectorPane = defaults.string(forKey: Keys.inspectorPane).flatMap(InspectorPane.init(rawValue:)) ?? .activity
    }

    /// Shows the inspector on `pane`, or hides it when it already shows `pane`.
    public func toggleInspector(_ pane: InspectorPane) {
        if showsInspector && inspectorPane == pane {
            showsInspector = false
        } else {
            inspectorPane = pane
            showsInspector = true
        }
    }
}
