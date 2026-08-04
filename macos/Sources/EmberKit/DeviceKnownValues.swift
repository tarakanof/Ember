import Foundation

/// The discrete `timeSeparatorMode` values NG exposes on /v1/device/settings
/// (replaces AWTRIX3's TFORMAT strings — see the #67 mapping).
public enum TimeSeparatorMode: String, CaseIterable, Identifiable, Sendable {
    case steady, blink, pulse

    public var id: String { rawValue }
    public var displayName: String { rawValue.capitalized }
}

/// The discrete `dateOrder` values NG exposes on /v1/device/settings.
public enum DateOrder: String, CaseIterable, Identifiable, Sendable {
    case dayMonthYear, monthDayYear, yearMonthDay

    public var id: String { rawValue }
    public var displayName: String {
        switch self {
        case .dayMonthYear: "Day / Month / Year"
        case .monthDayYear: "Month / Day / Year"
        case .yearMonthDay: "Year / Month / Day"
        }
    }
}

/// The discrete `dateSeparator` values NG exposes on /v1/device/settings.
public enum DateSeparator: String, CaseIterable, Identifiable, Sendable {
    case dot, slash, dash

    public var id: String { rawValue }
    public var symbol: String {
        switch self {
        case .dot: "."
        case .slash: "/"
        case .dash: "-"
        }
    }
    public var displayName: String { "\"\(symbol)\"" }
}

/// The discrete `dateYearMode` values NG exposes on /v1/device/settings.
public enum DateYearMode: String, CaseIterable, Identifiable, Sendable {
    case none, twoDigit, fourDigit

    public var id: String { rawValue }
    public var displayName: String {
        switch self {
        case .none: "Hidden"
        case .twoDigit: "2-digit"
        case .fourDigit: "4-digit"
        }
    }
}

/// Fallback catalogues and preview renderers for the Device tab's pickers.
/// NG replaced AWTRIX3's static TEFF (0–10) table and TFORMAT/DFORMAT strftime
/// strings — transition/effect names now come from the live
/// GET /v1/device/capabilities response, and time/date are discrete typed
/// fields with no format string to render.
public enum DeviceKnownValues {
    /// Shown when the capabilities endpoint is unreachable or hasn't loaded
    /// yet, so the transition-effect picker still has something sane to offer.
    /// The live list from GET /v1/device/capabilities always wins once loaded.
    public static let fallbackTransitions = [
        "random", "slide", "dim", "zoom", "rotate", "pixelate", "curtain", "ripple", "blink", "reload", "fade",
    ]

    /// Title-cases a device-reported effect/transition name ("random" -> "Random").
    public static func displayName(_ raw: String) -> String {
        guard let first = raw.first else { return raw }
        return String(first).uppercased() + raw.dropFirst()
    }

    /// Renders a live example of the discrete time fields, e.g. "14:05:09" or
    /// "2:05 PM" — replaces DeviceFormats.example's strftime rendering.
    public static func timePreview(hour24: Bool, leadingZero: Bool, showSeconds: Bool, showAmPm: Bool,
                                    at date: Date = .now, timeZone: TimeZone = .current) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        let c = calendar.dateComponents([.hour, .minute, .second], from: date)
        let hour = c.hour ?? 0
        func pad(_ n: Int) -> String { String(format: "%02d", n) }

        let displayHour = hour24 ? hour : (hour % 12 == 0 ? 12 : hour % 12)
        var out = leadingZero ? pad(displayHour) : String(displayHour)
        out += ":" + pad(c.minute ?? 0)
        if showSeconds { out += ":" + pad(c.second ?? 0) }
        if showAmPm && !hour24 { out += hour < 12 ? " AM" : " PM" }
        return out
    }

    /// Renders a live example of the discrete date fields, e.g. "07.06.26" or
    /// "06/07" (year hidden) — replaces DeviceFormats.example's strftime rendering.
    public static func datePreview(order: DateOrder, separator: DateSeparator, yearMode: DateYearMode,
                                    at date: Date = .now, timeZone: TimeZone = .current) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        let c = calendar.dateComponents([.day, .month, .year], from: date)
        func pad(_ n: Int) -> String { String(format: "%02d", n) }

        let day = pad(c.day ?? 0)
        let month = pad(c.month ?? 0)
        let year: String?
        switch yearMode {
        case .none: year = nil
        case .twoDigit: year = pad((c.year ?? 0) % 100)
        case .fourDigit: year = String(c.year ?? 0)
        }

        var parts: [String]
        switch order {
        case .dayMonthYear: parts = [day, month]
        case .monthDayYear: parts = [month, day]
        case .yearMonthDay: parts = [month, day] // year prepended below
        }
        if let year {
            if order == .yearMonthDay {
                parts.insert(year, at: 0)
            } else {
                parts.append(year)
            }
        }
        return parts.joined(separator: separator.symbol)
    }
}
