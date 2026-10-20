import AppKit
import DeskhandClient
import Foundation

/// Shorter forms of an object preview that end at a property boundary, so a narrow column shows
/// whole properties and a single `…`.
public enum ValuePreview {
    /// `preview` itself, then forms that keep fewer leading elements of its top-level `{…}` or
    /// `[…]` followed by `…`, then the bare container, then the description before it.
    public static func candidates(_ preview: String) -> [String] {
        guard let container = container(of: preview) else { return [preview] }
        var elements = container.elements
        let hadMore = elements.last == "…"
        if hadMore { elements.removeLast() }
        var forms: [String] = [preview]
        for keep in stride(from: elements.count, through: 0, by: -1) {
            let shown = Array(elements.prefix(keep)) + ((keep < elements.count || hadMore) && keep > 0 ? ["…"] : [])
            let body = keep == 0 ? "…" : shown.joined(separator: ", ")
            forms.append(container.prefix + String(container.open) + body + String(container.close))
        }
        let description = container.prefix.trimmingCharacters(in: .whitespaces)
        if !description.isEmpty { forms.append(description) }
        var seen = Set<String>()
        return forms.filter { seen.insert($0).inserted }
    }

    struct Container {
        var prefix: String
        var open: Character
        var close: Character
        var elements: [String]
    }

    /// The top-level container that closes at the end of `preview`, and its elements.
    static func container(of preview: String) -> Container? {
        let chars = Array(preview)
        guard let close = chars.last, close == "}" || close == "]" else { return nil }
        let open: Character = close == "}" ? "{" : "["
        var depth = 0
        var quote: Character?
        var start: Int?
        var elements: [String] = []
        var elementStart = 0
        var i = 0
        while i < chars.count {
            let c = chars[i]
            if let q = quote {
                if c == "\\" { i += 2; continue }
                if c == q { quote = nil }
                i += 1
                continue
            }
            switch c {
            case "\"", "'", "`":
                quote = c
            case "{", "[", "(":
                if depth == 0, c == open, start == nil {
                    start = i
                    elementStart = i + 1
                }
                depth += 1
            case "}", "]", ")":
                depth -= 1
                if depth == 0, start != nil, i == chars.count - 1 {
                    let element = String(chars[elementStart..<i]).trimmingCharacters(in: .whitespaces)
                    if !element.isEmpty { elements.append(element) }
                }
            case "," where depth == 1 && start != nil:
                elements.append(String(chars[elementStart..<i]).trimmingCharacters(in: .whitespaces))
                elementStart = i + 1
            default:
                break
            }
            i += 1
        }
        guard let start, depth == 0 else { return nil }
        return Container(prefix: String(chars[0..<start]), open: open, close: close, elements: elements)
    }
}

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
