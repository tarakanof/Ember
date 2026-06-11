import Foundation

/// Mirror of the server's `GET /version` payload (unauthenticated). Used to show
/// the connected server build in the App tab so app↔server skew is visible.
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

    /// Short human-readable form. A released server reports a semver, shown with
    /// the commit, e.g. "0.9.0 · 44143ca"; a local/"dev" or older server (no
    /// semver) falls back to "ember @ 44143ca" (or "…-dirty").
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
