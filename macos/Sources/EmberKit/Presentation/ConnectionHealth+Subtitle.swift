import Foundation

extension ConnectionHealth {
    /// The Dashboard's window subtitle: "Connected to 192.168.0.2 · v0.29.0",
    /// "Offline since 10:42", "Not set up". `serverVersion` (a release like
    /// "0.29.0", see `VersionInfo.release`) is shown only while connected.
    /// `offlineReason` (the snapshot's error) names a Local Network refusal.
    public func subtitle(serverHost: String?, serverVersion: String? = nil, offlineReason: FeedError? = nil,
                         locale: Locale = .current, timeZone: TimeZone = .current) -> LocalizedStringResource {
        let host = serverHost.flatMap { $0.isEmpty ? nil : $0 }
        let version = serverVersion.flatMap { $0.isEmpty ? nil : $0 }
        switch self {
        case .unconfigured:
            return "Not set up"
        case .connecting:
            if let host { return "Connecting to \(host)…" }
            return "Connecting…"
        case .online, .degraded:
            switch (host, version) {
            case let (host?, version?):
                return LocalizedStringResource(
                    "Connected to \(host) · v\(version)",
                    comment: "Dashboard window subtitle: the server's host name or address, then its version (\"0.29.0\").")
            case let (host?, nil):
                return "Connected to \(host)"
            case let (nil, version?):
                return LocalizedStringResource(
                    "Connected · v\(version)",
                    comment: "Dashboard window subtitle when the server's host is unknown: its version (\"0.29.0\").")
            case (nil, nil):
                return "Connected"
            }
        case .offline where offlineReason == .localNetworkDenied:
            return "Offline: Local Network access is off"
        case .offline(let since) where offlineReason == .timedOut:
            var style = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
            style.timeZone = timeZone
            return LocalizedStringResource(
                "Offline: not responding since \(since.formatted(style))",
                comment: "Dashboard window subtitle: the server stopped answering in time; the time it began (matches \"Offline since %@\").")
        case .offline(let since):
            var style = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
            style.timeZone = timeZone
            return "Offline since \(since.formatted(style))"
        }
    }
}
