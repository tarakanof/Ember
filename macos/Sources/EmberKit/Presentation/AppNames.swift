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
    ]

    public static func display(_ wireName: String) -> LocalizedStringResource {
        let lower = wireName.lowercased()
        let name = lower.hasPrefix("ember-") ? String(lower.dropFirst("ember-".count)) : lower
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
