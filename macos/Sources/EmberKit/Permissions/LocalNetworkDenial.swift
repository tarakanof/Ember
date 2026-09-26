import Foundation
import Network

/// Recognises macOS Local Network privacy refusing this app a LAN connection.
///
/// The refusal doesn't look like one. Seen on-device with a rebuilt ad-hoc
/// app: `NWBrowser` fails with DNS-SD `NoAuth` (-65555) or `PolicyDenied`
/// (-65570); a URLSession request to the server fails with
/// `NSURLErrorNotConnectedToInternet` (-1009) over POSIX `ENETDOWN` ("Network
/// is down"), its path "unsatisfied (Local network prohibited)". Reported as
/// is, that reads "Server unreachable" while the server is fine.
public enum LocalNetworkDenial {
    /// `kDNSServiceErr_NoAuth`, from a browse the app may not run.
    static let dnsNoAuth: Int32 = -65555
    /// `kDNSServiceErr_PolicyDenied`.
    static let dnsPolicyDenied: Int32 = -65570
    /// `ENETDOWN`: what a refused LAN connection reports.
    static let posixNetworkDown: Int = 50
    /// `kCFStreamErrorDomainPOSIX`.
    static let posixStreamDomain: Int = 1

    /// What a failed connection's network path says, when it's known.
    public enum PathVerdict: Equatable, Sendable {
        /// Unsatisfied because Local Network access is off.
        case localNetworkDenied
        /// Unsatisfied for another reason (no network, say), or satisfied.
        case other
    }

    /// Whether a browse or connection error is a Local Network refusal. A DNS
    /// `NoAuth`/`PolicyDenied` always is; `ENETDOWN` is only when the host is
    /// on the LAN (Wi-Fi off gives it for any host).
    public static func isDenied(_ error: NWError, host: String? = nil) -> Bool {
        switch error {
        case .dns(let code):
            return code == dnsNoAuth || code == dnsPolicyDenied
        case .posix(let code):
            return code == .ENETDOWN && host.map(isLANHost) == true
        default:
            return false
        }
    }

    /// Whether a URLSession (or Network) error from a request to `host` is a
    /// Local Network refusal. The failed path's unsatisfied reason decides
    /// when the error carries it; otherwise `ENETDOWN` to a LAN host does.
    public static func isDenied(_ error: Error, host: String?) -> Bool {
        if let e = error as? NWError { return isDenied(e, host: host) }
        let ns = error as NSError
        guard ns.domain == NSURLErrorDomain else { return false }
        return isDenied(urlCode: ns.code,
                        streamDomain: streamValue(ns, "_kCFStreamErrorDomainKey"),
                        streamCode: streamValue(ns, "_kCFStreamErrorCodeKey"),
                        path: pathVerdict(ns),
                        host: host)
    }

    /// The pure rule under `isDenied(_:host:)`.
    static func isDenied(urlCode: Int, streamDomain: Int?, streamCode: Int?,
                         path: PathVerdict?, host: String?) -> Bool {
        if let path { return path == .localNetworkDenied }
        guard urlCode == NSURLErrorNotConnectedToInternet || urlCode == NSURLErrorCannotConnectToHost,
              streamDomain == posixStreamDomain, streamCode == posixNetworkDown
        else { return false }
        return host.map(isLANHost) ?? false
    }

    /// Whether Local Network privacy covers connections to `host`: a
    /// private, link-local or unique-local address, a `.local` name, or a
    /// bare single-label name.
    public static func isLANHost(_ host: String) -> Bool {
        let h = host.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased()
        if h.isEmpty { return false }
        if let v4 = IPv4Address(h) {
            let b = [UInt8](v4.rawValue)
            return b[0] == 10
                || (b[0] == 172 && (16...31).contains(b[1]))
                || (b[0] == 192 && b[1] == 168)
                || (b[0] == 169 && b[1] == 254)
        }
        if let v6 = IPv6Address(h.split(separator: "%").first.map(String.init) ?? h) {
            let b = [UInt8](v6.rawValue)
            return (b[0] == 0xfe && (b[1] & 0xc0) == 0x80) || (b[0] & 0xfe) == 0xfc
        }
        if h == "localhost" { return false }
        return h.hasSuffix(".local") || h.hasSuffix(".local.") || !h.contains(".")
    }

    private static func streamValue(_ e: NSError, _ key: String) -> Int? {
        if let v = e.userInfo[key] as? Int { return v }
        return (e.userInfo[NSUnderlyingErrorKey] as? NSError)?.userInfo[key] as? Int
    }

    /// Reads the failed path CFNetwork attaches (`_NSURLErrorNWPathKey`,
    /// private but stable), on the error or its underlying one.
    private static func pathVerdict(_ e: NSError) -> PathVerdict? {
        let key = "_NSURLErrorNWPathKey"
        let raw = e.userInfo[key] ?? (e.userInfo[NSUnderlyingErrorKey] as? NSError)?.userInfo[key]
        guard let raw, let path = raw as? nw_path_t else { return nil }
        return nw_path_get_unsatisfied_reason(path) == nw_path_unsatisfied_reason_local_network_denied
            ? .localNetworkDenied : .other
    }
}
