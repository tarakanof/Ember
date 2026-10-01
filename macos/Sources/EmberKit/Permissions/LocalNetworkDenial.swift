import Foundation
import Network
import os

/// Recognises macOS Local Network privacy refusing this app a LAN connection.
public enum LocalNetworkDenial {
    static let dnsNoAuth: Int32 = -65555
    static let dnsPolicyDenied: Int32 = -65570
    static let posixNetworkDown: Int = 50
    static let posixStreamDomain: Int = 1

    /// What a failed connection's network path says, when it's known.
    public enum PathVerdict: Equatable, Sendable {
        /// Unsatisfied because Local Network access is off.
        case localNetworkDenied
        /// Unsatisfied for another reason (no network, say), or satisfied.
        case other
    }

    /// Whether a browse or connection error is a Local Network refusal.
    public static func isDenied(_ error: NWError, host: String? = nil,
                                pathStatus: NWPath.Status? = nil) -> Bool {
        switch error {
        case .dns(let code):
            return code == dnsNoAuth || code == dnsPolicyDenied
        case .posix(let code):
            return code == .ENETDOWN && pathStatus == .satisfied && host.map(isLANHost) == true
        default:
            return false
        }
    }

    /// Whether a URLSession (or Network) error from a request to `host` is a
    /// Local Network refusal.
    public static func isDenied(_ error: Error, host: String?, pathStatus: NWPath.Status?) -> Bool {
        if let e = error as? NWError { return isDenied(e, host: host, pathStatus: pathStatus) }
        let ns = error as NSError
        guard ns.domain == NSURLErrorDomain else { return false }
        return isDenied(urlCode: ns.code,
                        streamDomain: streamValue(ns, "_kCFStreamErrorDomainKey"),
                        streamCode: streamValue(ns, "_kCFStreamErrorCodeKey"),
                        path: pathVerdict(ns),
                        host: host,
                        pathStatus: pathStatus)
    }

    static func isDenied(urlCode: Int, streamDomain: Int?, streamCode: Int?,
                         path: PathVerdict?, host: String?, pathStatus: NWPath.Status?) -> Bool {
        if let path { return path == .localNetworkDenied }
        guard urlCode == NSURLErrorNotConnectedToInternet || urlCode == NSURLErrorCannotConnectToHost,
              streamDomain == posixStreamDomain, streamCode == posixNetworkDown,
              pathStatus == .satisfied
        else { return false }
        return host.map(isLANHost) ?? false
    }

    static let lanSuffixes = [".local", ".home.arpa", ".internal", ".lan"]

    /// Whether Local Network privacy covers connections to `host`: a
    /// private, link-local or unique-local address (IPv4-mapped IPv6
    /// included), a name under a LAN-only suffix, or a bare single-label name.
    public static func isLANHost(_ host: String) -> Bool {
        var h = host.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased()
        if h.isEmpty { return false }
        if let v4 = IPv4Address(h) { return isLANv4([UInt8](v4.rawValue)) }
        if let v6 = IPv6Address(h.split(separator: "%").first.map(String.init) ?? h) {
            let b = [UInt8](v6.rawValue)
            if b[0..<10].allSatisfy({ $0 == 0 }), b[10] == 0xff, b[11] == 0xff {
                return isLANv4(Array(b[12..<16]))
            }
            return (b[0] == 0xfe && (b[1] & 0xc0) == 0x80) || (b[0] & 0xfe) == 0xfc
        }
        if h.hasSuffix(".") { h.removeLast() }
        if h == "localhost" { return false }
        return lanSuffixes.contains { h.hasSuffix($0) } || !h.contains(".")
    }

    private static func isLANv4(_ b: [UInt8]) -> Bool {
        b[0] == 10
            || (b[0] == 172 && (16...31).contains(b[1]))
            || (b[0] == 192 && b[1] == 168)
            || (b[0] == 169 && b[1] == 254)
    }

    private static func streamValue(_ e: NSError, _ key: String) -> Int? {
        if let v = e.userInfo[key] as? Int { return v }
        return (e.userInfo[NSUnderlyingErrorKey] as? NSError)?.userInfo[key] as? Int
    }

    private static func pathVerdict(_ e: NSError) -> PathVerdict? {
        let key = "_NSURLErrorNWPathKey"
        let raw = e.userInfo[key] ?? (e.userInfo[NSUnderlyingErrorKey] as? NSError)?.userInfo[key]
        guard let raw, let path = raw as? nw_path_t else { return nil }
        return nw_path_get_unsatisfied_reason(path) == nw_path_unsatisfied_reason_local_network_denied
            ? .localNetworkDenied : .other
    }
}

/// This Mac's overall network path, kept current by one `NWPathMonitor`.
public final class NetworkPathSnapshot: Sendable {
    public static let shared = NetworkPathSnapshot()

    private let monitor = NWPathMonitor()
    private let latest = OSAllocatedUnfairLock<NWPath.Status?>(initialState: nil)

    private init() {
        monitor.pathUpdateHandler = { [latest] path in latest.withLock { $0 = path.status } }
        monitor.start(queue: DispatchQueue(label: "com.ember.network-path"))
    }

    /// The last status the monitor reported; nil before its first update,
    /// which `LocalNetworkDenial` treats as "not a refusal".
    public var status: NWPath.Status? {
        latest.withLock { $0 }
    }
}
