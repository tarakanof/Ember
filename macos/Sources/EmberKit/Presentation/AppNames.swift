import Foundation

public enum AppNames {
    private static let known: [String: LocalizedStringResource] = [
        "claude": "Claude",
        "codex": "Codex",
        "t3": "T3 Code",
        "weather": "Weather",
        "weather-popup": "Weather Alert",
        "forecast": "Forecast",
        "air": "Air Quality",
        "air-popup": "Air Quality Alert",
        "sun-popup": "Sunrise & Sunset",
        "pomodoro": "Pomodoro",
        "meet": "Meeting",
        "meeting": "Meeting",
        "reminder": "Reminder",
        "notify": "Notification",
        "usage-alarm": "Usage Reset",
        "print": "Print",
        "print-eta": "Print · Ready At",
        "print-pct": "Print · Percent",
        "print-temp": "Print · Temperature",
        "print-done": "Print Done",
        "print-attention": "Printer Needs You",
        "print-error": "Printer Error",
    ]

    private static func key(_ wireName: String) -> String {
        let lower = wireName.lowercased()
        return lower.hasPrefix("ember-") ? String(lower.dropFirst("ember-".count)) : lower
    }

    static func isKnown(_ wireName: String) -> Bool {
        known[key(wireName)] != nil
    }

    public static func display(_ wireName: String) -> LocalizedStringResource {
        let name = key(wireName)
        if let hit = known[name] { return hit }
        if name.hasPrefix("usage-") { return "Usage" }
        let words = wireName
            .replacingOccurrences(of: "_", with: " ")
            .replacingOccurrences(of: "-", with: " ")
            .split(separator: " ")
        let titled = words.map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined(separator: " ")
        return "\(words.isEmpty ? wireName : titled)"
    }
}
