import Foundation

public struct ValidationError: Error, Equatable { public let message: String }

private func rejectControlChars(_ v: String) throws {
    if v.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7f }) {
        throw ValidationError(message: "value may not contain control characters")
    }
}

/// http(s) URL with a host and no embedded credentials. Returns the trimmed value.
public func validateServerURL(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    guard !v.isEmpty else { throw ValidationError(message: "server URL must not be empty") }
    guard let u = URLComponents(string: v),
          let scheme = u.scheme, (scheme == "http" || scheme == "https"),
          let host = u.host, !host.isEmpty,
          u.user == nil, u.password == nil else {
        throw ValidationError(message: "must be an http(s) URL with a host and no embedded credentials")
    }
    return v
}

private let hexColor = try! NSRegularExpression(pattern: "^#[0-9a-fA-F]{6}$")

/// #RRGGBB hex, or "" (unset = no tint). Returns the trimmed value.
public func validateSourceColor(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    if v.isEmpty { return "" }
    let range = NSRange(v.startIndex..., in: v)
    guard hexColor.firstMatch(in: v, range: range) != nil else {
        throw ValidationError(message: "color must be #RRGGBB hex")
    }
    return v
}

/// Non-empty source name.
public func validateSource(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    guard !v.isEmpty else { throw ValidationError(message: "source must not be empty") }
    return v
}

/// Token: any value without control chars; blank means "keep current" (caller's job).
public func validateToken(_ value: String) throws -> String {
    let v = value.trimmingCharacters(in: .whitespaces)
    try rejectControlChars(v)
    return v
}
