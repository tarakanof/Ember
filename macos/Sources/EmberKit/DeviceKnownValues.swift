import Foundation

/// AWTRIX TEFF values (0–10) and their documented effect names, so the Device
/// tab can offer a named picker instead of a bare number.
public enum TransitionEffect: Int, CaseIterable, Identifiable, Sendable {
    case random = 0, slide, dim, zoom, rotate, pixelate, curtain, ripple, blink, reload, fade

    public var id: Int { rawValue }

    public var displayName: String {
        switch self {
        case .random: "Random"
        case .slide: "Slide"
        case .dim: "Dim"
        case .zoom: "Zoom"
        case .rotate: "Rotate"
        case .pixelate: "Pixelate"
        case .curtain: "Curtain"
        case .ripple: "Ripple"
        case .blink: "Blink"
        case .reload: "Reload"
        case .fade: "Fade"
        }
    }
}

/// The TFORMAT/DFORMAT strings the AWTRIX firmware documents, plus a tiny
/// strftime renderer so pickers can show a live example ("14:05") instead of
/// the raw pattern ("%H:%M").
public enum DeviceFormats {
    public static let timeFormats = [
        "%H:%M:%S", "%l:%M:%S", "%H:%M", "%H %M", "%l:%M", "%l %M", "%l:%M %p", "%l %M %p",
    ]
    public static let dateFormats = [
        "%d.%m.%y", "%d.%m", "%y-%m-%d", "%m-%d", "%m/%d/%y", "%m/%d", "%d/%m/%y", "%d/%m", "%m-%d-%y",
    ]

    /// Renders the handful of strftime tokens AWTRIX formats use. Unknown
    /// tokens pass through as-is rather than crashing on firmware additions.
    public static func example(_ format: String, at date: Date = .now, timeZone: TimeZone = .current) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        let c = calendar.dateComponents([.hour, .minute, .second, .day, .month, .year], from: date)
        let hour = c.hour ?? 0
        let hour12 = hour % 12 == 0 ? 12 : hour % 12
        func pad(_ n: Int) -> String { String(format: "%02d", n) }

        var out = ""
        var i = format.startIndex
        while i < format.endIndex {
            let ch = format[i]
            if ch == "%", format.index(after: i) < format.endIndex {
                let token = format[format.index(after: i)]
                switch token {
                case "H": out += pad(hour)
                case "l": out += String(hour12)
                case "M": out += pad(c.minute ?? 0)
                case "S": out += pad(c.second ?? 0)
                case "p": out += hour < 12 ? "AM" : "PM"
                case "d": out += pad(c.day ?? 0)
                case "m": out += pad(c.month ?? 0)
                case "y": out += pad((c.year ?? 0) % 100)
                default: out.append(ch); out.append(token)
                }
                i = format.index(i, offsetBy: 2)
            } else {
                out.append(ch)
                i = format.index(after: i)
            }
        }
        return out
    }
}
