import Foundation

/// Mirror of the server's `GET /version` payload (unauthenticated). Used to show
/// the connected server build in the App tab so app↔server skew is visible.
public struct VersionInfo: Codable, Sendable {
    public var binary: String?
    public var revision: String?
    public var dirty: Bool?
    public var goVersion: String?

    enum CodingKeys: String, CodingKey {
        case binary, revision, dirty
        case goVersion = "go_version"
    }

    /// Short human-readable form, e.g. "ember @ 6ad0339" (or "…-dirty").
    public var short: String {
        let rev = String((revision ?? "").prefix(7))
        if rev.isEmpty { return binary ?? "unknown" }
        return "\(binary ?? "ember") @ \(rev)\(dirty == true ? "-dirty" : "")"
    }
}
