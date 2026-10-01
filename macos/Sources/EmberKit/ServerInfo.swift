import Foundation

/// Mirror of the server's `GET /version` payload (unauthenticated).
public struct VersionInfo: Codable, Sendable {
    public var binary: String?
    public var version: String?
    public var revision: String?
    public var dirty: Bool?
    public var goVersion: String?

    enum CodingKeys: String, CodingKey {
        case binary, version, revision, dirty
        case goVersion = "go_version"
    }

    /// The release version alone ("0.29.0"), for places that show just the
    /// build: no commit and no dirty marker.
    public var release: String? {
        guard var ver = version?.trimmingCharacters(in: .whitespaces), !ver.isEmpty, ver != "dev" else { return nil }
        if ver.hasPrefix("v") { ver.removeFirst() }
        return ver.isEmpty ? nil : ver
    }

    /// Short human-readable form.
    public var short: String {
        let rev = String((revision ?? "").prefix(7))
        let suffix = dirty == true ? "-dirty" : ""
        if let ver = version, !ver.isEmpty, ver != "dev" {
            return rev.isEmpty ? "\(ver)\(suffix)" : "\(ver) · \(rev)\(suffix)"
        }
        if rev.isEmpty { return binary ?? "unknown" }
        return "\(binary ?? "ember") @ \(rev)\(suffix)"
    }
}
