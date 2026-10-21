import AppKit
import DeskhandClient
import Foundation

/// Shorter forms of an object preview that end at a property boundary, so a narrow column shows
/// whole properties and a single `…`.
public enum ValuePreview {
    /// Lengths a long string of the first element is cut to, longest first.
    static let stringCuts = [12, 6]

    /// `preview` itself, then forms that keep fewer leading elements of its top-level `{…}` or
    /// `[…]` followed by `…`, then the first element with its string value cut to `stringCuts`,
    /// then the bare container, then the description before it.
    public static func candidates(_ preview: String) -> [String] {
        guard let container = container(of: preview) else { return [preview] }
        var elements = container.elements
        let hadMore = elements.last == "…"
        if hadMore { elements.removeLast() }
        var forms: [String] = [preview]
        func wrap(_ body: String) -> String { container.prefix + String(container.open) + body + String(container.close) }
        for keep in stride(from: elements.count, through: 1, by: -1) {
            let shown = Array(elements.prefix(keep)) + (keep < elements.count || hadMore ? ["…"] : [])
            forms.append(wrap(shown.joined(separator: ", ")))
        }
        if let first = elements.first {
            let more = elements.count > 1 || hadMore ? ", …" : ""
            for cut in stringCuts {
                if let shortened = shorten(first, to: cut) { forms.append(wrap(shortened + more)) }
            }
        }
        forms.append(wrap("…"))
        let description = container.prefix.trimmingCharacters(in: .whitespaces)
        if !description.isEmpty { forms.append(description) }
        var seen = Set<String>()
        return forms.filter { seen.insert($0).inserted }
    }

    /// `element` (`"text"` or `name: "text"`) with its string cut to `length` characters and `…`,
    /// or nil when it holds no string longer than that.
    static func shorten(_ element: String, to length: Int) -> String? {
        guard let close = element.last, close == "\"" || close == "'" || close == "`",
              let open = element.firstIndex(of: close), open < element.index(before: element.endIndex) else { return nil }
        let content = element[element.index(after: open)..<element.index(before: element.endIndex)]
        guard content.count > length else { return nil }
        var cut = String(content.prefix(length))
        while cut.last?.isWhitespace == true { cut.removeLast() }
        return String(element[...open]) + cut + "…" + String(close)
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
