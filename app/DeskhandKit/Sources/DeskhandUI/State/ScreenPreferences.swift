import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

/// Remote screen settings the person chose: stream preset, statistics overlay, keyboard
/// handling and the display shown per computer. Kept in UserDefaults; the screen tab menus and
/// the settings window edit the same values.
@MainActor
@Observable
public final class ScreenPreferences {
    enum Keys {
        static let preset = "screenPreset"
        static let showsStatistics = "screenShowsStatistics"
        static let keyboardMode = "keyboardMode"
        static let swapCommandControl = "keyboardSwapCommandControl"
        static let rightOptionHangul = "keyboardRightOptionHangul"
        static let rightCommandHanja = "keyboardRightCommandHanja"
        static let displays = "screenDisplays"
    }

    public var preset: StreamPreset {
        didSet { defaults.set(preset.rawValue, forKey: Keys.preset) }
    }

    /// Shows fps, latency, bitrate and encoder details over the remote screen.
    public var showsStatistics: Bool {
        didSet { defaults.set(showsStatistics, forKey: Keys.showsStatistics) }
    }

    public var keyboardMode: KeyboardMode {
        didSet { defaults.set(keyboardMode.rawValue, forKey: Keys.keyboardMode) }
    }

    /// ⌘ sends Ctrl and ⌃ sends the Windows key.
    public var swapCommandControl: Bool {
        didSet { defaults.set(swapCommandControl, forKey: Keys.swapCommandControl) }
    }

    /// Right ⌥ sends 한/영.
    public var rightOptionHangul: Bool {
        didSet { defaults.set(rightOptionHangul, forKey: Keys.rightOptionHangul) }
    }

    /// Right ⌘ sends 한자.
    public var rightCommandHanja: Bool {
        didSet { defaults.set(rightCommandHanja, forKey: Keys.rightCommandHanja) }
    }

    /// Display index chosen per host ID.
    private var displays: [String: Int] {
        didSet { defaults.set(displays, forKey: Keys.displays) }
    }

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        preset = defaults.string(forKey: Keys.preset).flatMap(StreamPreset.init(rawValue:)) ?? .balanced
        showsStatistics = defaults.object(forKey: Keys.showsStatistics) as? Bool ?? false
        keyboardMode = defaults.string(forKey: Keys.keyboardMode).flatMap(KeyboardMode.init(rawValue:)) ?? .scancode
        swapCommandControl = defaults.object(forKey: Keys.swapCommandControl) as? Bool ?? true
        rightOptionHangul = defaults.object(forKey: Keys.rightOptionHangul) as? Bool ?? true
        rightCommandHanja = defaults.object(forKey: Keys.rightCommandHanja) as? Bool ?? false
        displays = defaults.dictionary(forKey: Keys.displays) as? [String: Int] ?? [:]
    }

    public var keyMap: KeyMap {
        KeyMap(swapCommandControl: swapCommandControl, rightOptionHangul: rightOptionHangul,
               rightCommandHanja: rightCommandHanja)
    }

    /// The display to show for `host`: the chosen one while the host still has it, otherwise
    /// the primary display, otherwise index 0.
    public func display(for host: Host) -> Int {
        let indices = host.displays.map(\.index)
        if let chosen = displays[host.hostID], indices.isEmpty || indices.contains(chosen) { return chosen }
        return host.displays.first(where: \.primary)?.index ?? host.displays.first?.index ?? 0
    }

    public func setDisplay(_ index: Int, for hostID: String) {
        displays[hostID] = index
    }
}
