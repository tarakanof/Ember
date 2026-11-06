import Foundation

extension LocalizedStringResource {
    var text: String { String(localized: self) }
}
