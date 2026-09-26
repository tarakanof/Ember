import Foundation

extension ConnectionHealth {
    /// The Dashboard's window subtitle: "Connected to 192.168.0.2",
    /// "Offline since 10:42", "Not set up".
    public func subtitle(serverHost: String?, locale: Locale = .current,
                         timeZone: TimeZone = .current) -> LocalizedStringResource {
        let host = serverHost.flatMap { $0.isEmpty ? nil : $0 }
        switch self {
        case .unconfigured:
            return "Not set up"
        case .connecting:
            if let host { return "Connecting to \(host)…" }
            return "Connecting…"
        case .online, .degraded:
            if let host { return "Connected to \(host)" }
            return "Connected"
        case .offline(let since):
            var style = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
            style.timeZone = timeZone
            return "Offline since \(since.formatted(style))"
        }
    }
}
