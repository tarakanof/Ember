import Foundation

/// The Settings panes by their stored name. The raw values are a contract:
/// other windows open Settings on a pane by writing one to the
/// `settings.pane` default (`openSettings(pane:using:)`).
public enum SettingsPaneID: String, CaseIterable, Sendable {
    case general, connection, clock, agents, focus, weather, calendar, sounds

    /// The default the pane selection lives in.
    public static let storageKey = "settings.pane"

    /// Resolves a stored name, including the names panes had before the
    /// Settings restructure, so a saved selection or an older caller still
    /// lands on the right pane. Unknown names open Connection.
    public init(stored name: String?) {
        if let name, let pane = SettingsPaneID(rawValue: name) {
            self = pane
            return
        }
        switch name {
        case "app": self = .general
        case "device": self = .clock
        case "display": self = .agents
        case "pomodoro": self = .focus
        case "meetings", "reminders": self = .calendar
        default: self = .connection
        }
    }
}

/// A melody setting shown as a picker: the built-in default, a melody
/// stored on the clock, or anything else the user typed (an RTTTL string, or
/// a name the clock no longer has).
public enum MelodyChoice: Hashable, Sendable {
    case builtIn
    case stored(String)
    case custom

    /// The choice for a stored setting value.
    public init(value: String, available: [String]) {
        let v = value.trimmingCharacters(in: .whitespaces)
        if v.isEmpty {
            self = .builtIn
        } else if available.contains(v) {
            self = .stored(v)
        } else {
            self = .custom
        }
    }

    /// The setting value after picking this choice; `custom` keeps what's
    /// there unless it was a stored name or empty.
    public func value(replacing current: String, available: [String]) -> String {
        switch self {
        case .builtIn: return ""
        case .stored(let name): return name
        case .custom:
            return MelodyChoice(value: current, available: available) == .custom ? current : ""
        }
    }
}

/// Focus length presets for the Focus pane; anything else is "Custom".
public enum FocusPreset {
    public static let minutes = [15, 25, 45, 50, 90]
    public static let customRange = 5...180
}
