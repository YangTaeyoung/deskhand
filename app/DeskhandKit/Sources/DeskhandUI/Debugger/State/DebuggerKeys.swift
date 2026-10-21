import AppKit
import DeskhandClient

/// The ⌘ alternates of the execution controls, next to F8, F10, F11 and ⇧F11: ⌘\ pauses or
/// resumes, ⌘' steps over, ⌘; steps into and ⇧⌘; steps out. None apply while the console input
/// has the keyboard.
public enum DebuggerKeys {
    public enum Action: Equatable, Sendable {
        case togglePause
        case step(DebugStepKind)
    }

    /// The action of a key press, from its characters ignoring modifiers.
    public static func action(characters: String, modifiers: NSEvent.ModifierFlags, consoleFocused: Bool) -> Action? {
        guard !consoleFocused else { return nil }
        let flags = modifiers.intersection([.command, .shift, .option, .control])
        switch (characters, flags) {
        case ("\\", [.command]): return .togglePause
        case ("'", [.command]): return .step(.over)
        case (";", [.command]): return .step(.into)
        case (";", [.command, .shift]), (":", [.command, .shift]): return .step(.out)
        default: return nil
        }
    }
}
