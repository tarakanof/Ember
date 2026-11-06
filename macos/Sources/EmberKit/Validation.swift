import Foundation

public struct ValidationError: Error, Equatable, LocalizedError {
    public let message: LocalizedStringResource

    public init(message: LocalizedStringResource) { self.message = message }

    public var errorDescription: String? { String(localized: message) }
}

private func rejectControlChars(_ v: String) throws {
    if v.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7f }) {
        throw ValidationError(message: "The value can't contain control characters.")
    }
}

public let autoServerURL = "auto"

public func isAutoServerURL(_ value: String) -> Bool {
    value.trimmingCharacters(in: .whitespaces).lowercased() == autoServerURL
}

public func validateServerURL(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    guard !v.isEmpty else { throw ValidationError(message: "Enter the server's URL.") }
    if isAutoServerURL(v) { return autoServerURL }
    guard let u = URLComponents(string: v),
          let scheme = u.scheme, (scheme == "http" || scheme == "https"),
          let host = u.host, !host.isEmpty,
          u.user == nil, u.password == nil else {
        throw ValidationError(message: """
            The server URL must start with http:// or https://, include a host, and have no user name or password.
            """)
    }
    return v
}

private let hexColor = try! NSRegularExpression(pattern: "^#[0-9a-fA-F]{6}$")

public func validateSourceColor(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    if v.isEmpty { return "" }
    let range = NSRange(v.startIndex..., in: v)
    guard hexColor.firstMatch(in: v, range: range) != nil else {
        throw ValidationError(message: "The color must be a hex value like #FF8800.")
    }
    return v
}

public func validateSource(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    guard !v.isEmpty else { throw ValidationError(message: "Enter a source name.") }
    return v
}

public func validateToken(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    return v
}
