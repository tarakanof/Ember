import Foundation

public struct VersionInfo: Codable, Sendable {
    public var binary: String?
    public var version: String?
    public var revision: String?
    public var dirty: Bool?
    public var goVersion: String?
    public var features: [String]?

    public static let firmwareDeleteKeep = "firmware_delete_keep"

    enum CodingKeys: String, CodingKey {
        case binary, version, revision, dirty, features
        case goVersion = "go_version"
    }

    public func supports(_ feature: String) -> Bool { features?.contains(feature) == true }

    public var release: String? {
        guard var ver = version?.trimmingCharacters(in: .whitespaces), !ver.isEmpty, ver != "dev" else { return nil }
        if ver.hasPrefix("v") { ver.removeFirst() }
        return ver.isEmpty ? nil : ver
    }

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
