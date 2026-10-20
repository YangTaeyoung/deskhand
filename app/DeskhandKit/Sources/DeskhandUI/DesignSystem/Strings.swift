import Foundation

extension String {
    /// The text trimmed and ending with a period when it ends without punctuation.
    var asSentence: String {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        guard let last = trimmed.last, !".!?…".contains(last) else { return trimmed }
        return trimmed + "."
    }

    /// Looks up `key` in the DeskhandUI string catalog (Resources/Localizable.xcstrings);
    /// `value` is the Korean source text used when the catalog has no entry.
    static func ui(_ key: StaticString, _ value: String.LocalizationValue) -> String {
        String(localized: key, defaultValue: value, bundle: .module)
    }
}
