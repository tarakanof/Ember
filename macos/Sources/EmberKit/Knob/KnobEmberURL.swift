import Darwin
import Foundation

public enum KnobEmberURL {
    public struct Suggestion: Equatable, Sendable {
        public let url: String
        public let replacedHost: String?
    }

    public static func needsLANAddress(_ host: String) -> Bool {
        let h = host.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "[]."))
        return h == "localhost" || h.hasPrefix("127.") || h == "::1" || h == "0.0.0.0" || h.hasSuffix(".local")
    }

    public static func suggest(server: URL?, thisMac: String?,
                               resolve: (String) -> String?) -> Suggestion? {
        guard let server, var c = URLComponents(url: server, resolvingAgainstBaseURL: false),
              let host = c.host, !host.isEmpty else { return nil }
        c.path = ""; c.query = nil; c.fragment = nil; c.user = nil; c.password = nil
        var replaced: String?
        if needsLANAddress(host) {
            let lower = host.lowercased()
            let ip = lower.hasSuffix(".local") ? (resolve(host) ?? thisMac) : thisMac
            if let ip {
                c.host = ip
                replaced = host
            }
        }
        guard let s = c.string else { return nil }
        return Suggestion(url: s, replacedHost: replaced)
    }

    public static func thisMacLANIPv4() -> String? {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return nil }
        defer { freeifaddrs(head) }
        var found: [(String, String)] = []
        var p: UnsafeMutablePointer<ifaddrs>? = first
        while let ifa = p {
            defer { p = ifa.pointee.ifa_next }
            let flags = Int32(ifa.pointee.ifa_flags)
            guard flags & IFF_UP != 0, flags & IFF_LOOPBACK == 0,
                  let addr = ifa.pointee.ifa_addr, addr.pointee.sa_family == UInt8(AF_INET) else { continue }
            var buf = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(addr, socklen_t(addr.pointee.sa_len), &buf, socklen_t(buf.count), nil, 0, NI_NUMERICHOST) == 0
            else { continue }
            let ip = String(cString: buf)
            if isPrivateIPv4(ip) { found.append((String(cString: ifa.pointee.ifa_name), ip)) }
        }
        return (found.first { $0.0.hasPrefix("en") } ?? found.first)?.1
    }

    public static func resolveIPv4(_ host: String) -> String? {
        var hints = addrinfo()
        hints.ai_family = AF_INET
        hints.ai_socktype = SOCK_STREAM
        var res: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, nil, &hints, &res) == 0, let r = res else { return nil }
        defer { freeaddrinfo(res) }
        var buf = [CChar](repeating: 0, count: Int(NI_MAXHOST))
        guard getnameinfo(r.pointee.ai_addr, r.pointee.ai_addrlen, &buf, socklen_t(buf.count), nil, 0, NI_NUMERICHOST) == 0
        else { return nil }
        let ip = String(cString: buf)
        return ip.hasPrefix("127.") ? nil : ip
    }

    static func isPrivateIPv4(_ ip: String) -> Bool {
        let o = ip.split(separator: ".").compactMap { Int($0) }
        guard o.count == 4 else { return false }
        return o[0] == 10 || (o[0] == 172 && (16...31).contains(o[1])) || (o[0] == 192 && o[1] == 168)
    }
}
