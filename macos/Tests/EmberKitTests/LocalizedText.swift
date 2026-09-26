import Foundation

extension LocalizedStringResource {
    /// The resolved English text (the test bundle has no translations, so
    /// lookups fall back to the key with its arguments substituted).
    var text: String { String(localized: self) }
}
