import Foundation

extension String {
    /// The text trimmed and ending with a period when it ends without punctuation.
    var asSentence: String {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        guard let last = trimmed.last, !".!?…".contains(last) else { return trimmed }
        return trimmed + "."
    }

    /// The non-empty `parts`, each as a sentence, joined by spaces: what failed, then how to fix it.
    static func sentences(_ parts: String?...) -> String {
        parts.compactMap { part in
            guard let part, !part.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            return part.asSentence
        }.joined(separator: " ")
    }

    /// Looks up `key` in the DeskhandUI string catalog (Resources/Localizable.xcstrings);
    /// `value` is the Korean source text used when the catalog has no entry.
    static func ui(_ key: StaticString, _ value: String.LocalizationValue) -> String {
        String(localized: key, defaultValue: value, bundle: .module)
    }
}
