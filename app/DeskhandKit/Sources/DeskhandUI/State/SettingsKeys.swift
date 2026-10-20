import Foundation

/// UserDefaults keys of every setting and remembered layout the app keeps. Every key starts
/// with `prefix`.
public enum SettingsKey {
    public static let prefix = "deskhand."
    /// Version of the key layout in a defaults domain, raised by `SettingsMigration`.
    public static let version = "deskhand.settings.version"

    /// The selected computer's host ID.
    public static let selectedHost = "deskhand.selectedHostID"

    public enum Settings {
        /// The pane the settings window shows.
        public static let pane = "deskhand.settings.pane"
    }

    public enum Workspace {
        public static let tab = "deskhand.workspace.tab"
        public static let inspectorVisible = "deskhand.workspace.inspectorVisible"
        public static let inspectorPane = "deskhand.workspace.inspectorPane"
    }

    public enum Screen {
        public static let preset = "deskhand.screen.preset"
        public static let showsStatistics = "deskhand.screen.showsStatistics"
        public static let showsLocalCursor = "deskhand.screen.showsLocalCursor"
        /// Display index by host ID.
        public static let displays = "deskhand.screen.displays"
    }

    public enum Keyboard {
        public static let mode = "deskhand.keyboard.mode"
        public static let swapCommandControl = "deskhand.keyboard.swapCommandControl"
        public static let rightOptionHangul = "deskhand.keyboard.rightOptionHangul"
        public static let rightCommandHanja = "deskhand.keyboard.rightCommandHanja"
        public static let forwardsSystemShortcuts = "deskhand.keyboard.forwardsSystemShortcuts"
    }

    public enum Files {
        public static let conflictPolicy = "deskhand.files.conflictPolicy"
        public static let confirmMoves = "deskhand.files.confirmMoves"
        public static let sort = "deskhand.files.sort"
        public static let showsHidden = "deskhand.files.showsHidden"
        public static let columnWidths = "deskhand.files.columnWidths"
        public static let hiddenColumns = "deskhand.files.hiddenColumns"
        public static let localPath = "deskhand.files.localPath"
        public static let remotePaths = "deskhand.files.remotePaths"
        public static let queueExpanded = "deskhand.files.queueExpanded"
        public static let queueHeight = "deskhand.files.queueHeight"
        public static let splitFraction = "deskhand.files.splitFraction"
        public static let downloadFolder = "deskhand.files.downloadFolder"
        /// Work root paths by host ID.
        public static let workRoots = "deskhand.files.workRoots"
        public static let finishedTransferRetention = "deskhand.files.finishedTransferRetention"
    }

    public enum Terminal {
        public static let defaultShell = "deskhand.terminal.defaultShell"
        public static let fontSize = "deskhand.terminal.fontSize"
        public static let confirmsMultilinePaste = "deskhand.terminal.confirmsMultilinePaste"
        public static let optionAsMeta = "deskhand.terminal.optionAsMeta"
        public static let allowsClipboardWrite = "deskhand.terminal.allowsClipboardWrite"
    }

    public enum Processes {
        public static let sortColumn = "deskhand.processes.sortColumn"
        public static let sortAscending = "deskhand.processes.sortAscending"
        public static let tree = "deskhand.processes.tree"
    }

    public enum QA {
        public static let allHosts = "deskhand.qa.allHosts"
        public static let recentScenarios = "deskhand.qa.recentScenarios"
    }

    public enum Debugger {
        static let recentLaunchesPrefix = "deskhand.debugger.recentLaunches."

        public static func recentLaunches(_ hostID: String) -> String { recentLaunchesPrefix + hostID }
    }

    public enum Table {
        static let prefix = "deskhand.table."

        public static func firstColumnWidth(_ autosaveName: String) -> String { prefix + autosaveName + ".firstColumnWidth" }
    }

    public enum Privacy {
        /// AI data scope by host ID, as JSON.
        public static let aiScopes = "deskhand.privacy.aiScopes"
    }

    public enum QuickSwitch {
        /// Items opened from the quick switch, newest first, as JSON.
        public static let recents = "deskhand.quickSwitch.recents"
    }
}

/// Moves settings kept under the keys used before `SettingsKey` into it, once per defaults
/// domain: each old key's value is copied to its new key unless the new key already has a
/// value, and the old key is removed.
public enum SettingsMigration {
    public static let currentVersion = 1

    /// Keys used before the namespace and their keys in it.
    static let renamedKeys: [(old: String, new: String)] = [
        ("selectedHostID", SettingsKey.selectedHost),
        ("workspaceTab", SettingsKey.Workspace.tab),
        ("inspectorVisible", SettingsKey.Workspace.inspectorVisible),
        ("inspectorPane", SettingsKey.Workspace.inspectorPane),
        ("screenPreset", SettingsKey.Screen.preset),
        ("screenShowsStatistics", SettingsKey.Screen.showsStatistics),
        ("screenDisplays", SettingsKey.Screen.displays),
        ("keyboardMode", SettingsKey.Keyboard.mode),
        ("keyboardSwapCommandControl", SettingsKey.Keyboard.swapCommandControl),
        ("keyboardRightOptionHangul", SettingsKey.Keyboard.rightOptionHangul),
        ("keyboardRightCommandHanja", SettingsKey.Keyboard.rightCommandHanja),
        ("keyboardForwardsSystemShortcuts", SettingsKey.Keyboard.forwardsSystemShortcuts),
        ("filesConflictPolicy", SettingsKey.Files.conflictPolicy),
        ("filesConfirmMoves", SettingsKey.Files.confirmMoves),
        ("filesSort", SettingsKey.Files.sort),
        ("filesShowsHidden", SettingsKey.Files.showsHidden),
        ("filesColumnWidths", SettingsKey.Files.columnWidths),
        ("filesHiddenColumns", SettingsKey.Files.hiddenColumns),
        ("filesLocalPath", SettingsKey.Files.localPath),
        ("filesRemotePaths", SettingsKey.Files.remotePaths),
        ("filesQueueExpanded", SettingsKey.Files.queueExpanded),
        ("filesQueueHeight", SettingsKey.Files.queueHeight),
        ("filesSplitFraction", SettingsKey.Files.splitFraction),
        ("terminalDefaultShell", SettingsKey.Terminal.defaultShell),
        ("terminalFontSize", SettingsKey.Terminal.fontSize),
        ("terminalConfirmsMultilinePaste", SettingsKey.Terminal.confirmsMultilinePaste),
        ("terminalOptionAsMeta", SettingsKey.Terminal.optionAsMeta),
        ("terminalAllowsClipboardWrite", SettingsKey.Terminal.allowsClipboardWrite),
        ("processes.sortColumn", SettingsKey.Processes.sortColumn),
        ("processes.sortAscending", SettingsKey.Processes.sortAscending),
        ("processes.tree", SettingsKey.Processes.tree),
        ("qa.allHosts", SettingsKey.QA.allHosts),
        ("qa.recentScenarios", SettingsKey.QA.recentScenarios),
    ]

    /// Prefixes of keys used before the namespace that end in a host ID or table name, and
    /// their prefixes in it.
    static let renamedPrefixes: [(old: String, new: String)] = [
        ("debugger.recentLaunches.", SettingsKey.Debugger.recentLaunchesPrefix),
        ("DataTable.", SettingsKey.Table.prefix),
    ]

    public static func migrate(_ defaults: UserDefaults) {
        guard defaults.integer(forKey: SettingsKey.version) < currentVersion else { return }
        var renames = renamedKeys
        for key in defaults.dictionaryRepresentation().keys {
            for prefix in renamedPrefixes where key.hasPrefix(prefix.old) {
                renames.append((key, prefix.new + key.dropFirst(prefix.old.count)))
            }
        }
        for rename in renames {
            guard let value = defaults.object(forKey: rename.old) else { continue }
            if defaults.object(forKey: rename.new) == nil {
                defaults.set(value, forKey: rename.new)
            }
            defaults.removeObject(forKey: rename.old)
        }
        defaults.set(currentVersion, forKey: SettingsKey.version)
    }
}
