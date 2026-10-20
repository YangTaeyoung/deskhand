import Foundation
import Observation

/// Terminal tab settings the person chose: the shell ⌘T opens, the text size and the
/// multi-line paste confirmation. Kept in UserDefaults; the terminal menus and the settings
/// window edit the same values.
@MainActor
@Observable
public final class TerminalPreferences {
    enum Keys {
        static let defaultShell = "terminalDefaultShell"
        static let fontSize = "terminalFontSize"
        static let confirmsMultilinePaste = "terminalConfirmsMultilinePaste"
    }

    /// The shell a new tab opens with ⌘T and the + button.
    public var defaultShell: TerminalShell {
        didSet { defaults.set(defaultShell.rawValue, forKey: Keys.defaultShell) }
    }

    /// Text size of every terminal in points.
    public var fontSize: Double {
        didSet { defaults.set(fontSize, forKey: Keys.fontSize) }
    }

    /// Pasting text that holds a line break asks first, showing how many lines it has.
    public var confirmsMultilinePaste: Bool {
        didSet { defaults.set(confirmsMultilinePaste, forKey: Keys.confirmsMultilinePaste) }
    }

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        defaultShell = defaults.string(forKey: Keys.defaultShell).flatMap(TerminalShell.init(rawValue:)) ?? .powershell
        let size = defaults.object(forKey: Keys.fontSize) as? Double ?? Double(DS.Terminal.fontSize)
        fontSize = min(max(size, Double(DS.Terminal.minimumFontSize)), Double(DS.Terminal.maximumFontSize))
        confirmsMultilinePaste = defaults.object(forKey: Keys.confirmsMultilinePaste) as? Bool ?? true
    }
}
