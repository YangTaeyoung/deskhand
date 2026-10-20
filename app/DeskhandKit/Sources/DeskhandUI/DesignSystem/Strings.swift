import Foundation

extension String {
    /// Looks up `key` in the DeskhandUI string catalog (Resources/Localizable.xcstrings);
    /// `value` is the Korean source text used when the catalog has no entry.
    static func ui(_ key: StaticString, _ value: String.LocalizationValue) -> String {
        String(localized: key, defaultValue: value, bundle: .module)
    }
}
