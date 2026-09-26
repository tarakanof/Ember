import Foundation

extension ConnectionHealth {
    /// The Dashboard's window subtitle: "Connected to 192.168.0.2",
    /// "Offline since 10:42", "Not set up".
    public func subtitle(serverHost: String?, locale: Locale = .current,
                         timeZone: TimeZone = .current) -> String {
        let host = serverHost.flatMap { $0.isEmpty ? nil : $0 }
        switch self {
        case .unconfigured:
            return "Not set up"
        case .connecting:
            return host.map { "Connecting to \($0)…" } ?? "Connecting…"
        case .online, .degraded:
            return host.map { "Connected to \($0)" } ?? "Connected"
        case .offline(let since):
            var style = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
            style.timeZone = timeZone
            return "Offline since \(since.formatted(style))"
        }
    }
}
