import Foundation
import Network

/// Formatting shared by the Bonjour browsers (`ServerDiscovery`, `ClockDiscovery`)
/// when they turn a resolved `NWEndpoint` into a URL. Kept in one place because
/// the IPv6 rules are subtle enough to get wrong twice.
enum EndpointFormat {
    /// Renders a resolved host as a plain string.
    ///
    /// IPv4 literals drop the interface suffix `NWEndpoint` sometimes appends;
    /// IPv6 keeps its `%zone` (a link-local address is unusable without it —
    /// `urlHost` percent-encodes it later).
    static func host(_ h: NWEndpoint.Host) -> String {
        switch h {
        case .name(let n, _): return n
        case .ipv4(let a): return String("\(a)".split(separator: "%").first ?? "")
        case .ipv6(let a): return "\(a)"
        @unknown default: return ""
        }
    }

    /// Formats a host for a URL authority. IPv6 literals (recognised by a colon)
    /// must be bracketed, and a link-local zone id keeps its `%` separator
    /// percent-encoded as `%25` (RFC 6874) — e.g. `fe80::1%en0` becomes
    /// `[fe80::1%25en0]`. IPv4 literals and hostnames pass through.
    static func urlHost(_ host: String) -> String {
        guard host.contains(":") else { return host }
        return "[\(host.replacingOccurrences(of: "%", with: "%25"))]"
    }
}
