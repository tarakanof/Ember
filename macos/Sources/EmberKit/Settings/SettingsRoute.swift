import Foundation

/// Settings › App: panes that configure Ember.app itself.
public enum AppPane: String, CaseIterable, Sendable {
    case general, connection, permissions, sounds
}

/// Settings › Sources: where data comes from, configured once for every device.
public enum SourceID: String, CaseIterable, Sendable {
    case agents, focus, weather, calendar
}

/// A device's own pages that aren't apps (hardware and identity).
public enum HardwarePage: String, CaseIterable, Sendable {
    case status, display
    case timeDate = "time-date"
    case buttons, sensors, sounds, behavior
}

/// A device's version of an app: only presentation, never source data.
public enum AppID: String, CaseIterable, Sendable {
    case agents, bot, focus, weather, calendar
}

/// A page under one device node.
public enum DevicePage: Hashable, Sendable {
    case hardware(HardwarePage)
    /// The device's app list: Rotation on the clock, Pages on the knob.
    case apps
    case app(AppID)
}

/// One Settings selection; stored as a path such as `source/weather` or
/// `device/clock/app/weather`.
public enum SettingsRoute: Hashable, Sendable {
    case app(AppPane)
    case source(SourceID)
    case device(String, DevicePage)

    /// The default the selection lives in (the old pane key, so old values migrate in place).
    public static let storageKey = "settings.pane"
    /// The default holding the expanded sidebar nodes, comma-joined.
    public static let expandedKey = "settings.expanded"
    /// The default bumped by every deep link, so re-opening the current
    /// route still reveals it in the sidebar.
    public static let revealKey = "settings.reveal"
    /// Where Settings opens with nothing stored.
    public static let fallback = SettingsRoute.app(.connection)

    /// The stored path.
    public var stored: String {
        switch self {
        case .app(let p): "app/\(p.rawValue)"
        case .source(let s): "source/\(s.rawValue)"
        case .device(let id, .hardware(let h)): "device/\(id)/hardware/\(h.rawValue)"
        case .device(let id, .apps): "device/\(id)/apps"
        case .device(let id, .app(let a)): "device/\(id)/app/\(a.rawValue)"
        }
    }

    /// The device this route is under, if any.
    public var deviceID: String? {
        if case .device(let id, _) = self { id } else { nil }
    }

    /// Parses a stored path or a pane name from before the per-device
    /// regroup; anything else is the fallback.
    public init(stored name: String?) {
        self = name.flatMap(Self.parse) ?? Self.legacy(name) ?? Self.fallback
    }

    private static func parse(_ path: String) -> SettingsRoute? {
        let parts = path.split(separator: "/", omittingEmptySubsequences: false).map(String.init)
        switch parts.first {
        case "app" where parts.count == 2:
            return AppPane(rawValue: parts[1]).map(SettingsRoute.app)
        case "source" where parts.count == 2:
            return SourceID(rawValue: parts[1]).map(SettingsRoute.source)
        case "device" where parts.count >= 3 && !parts[1].isEmpty:
            let id = parts[1]
            switch (parts[2], parts.count) {
            case ("apps", 3): return .device(id, .apps)
            case ("hardware", 4): return HardwarePage(rawValue: parts[3]).map { .device(id, .hardware($0)) }
            case ("app", 4): return AppID(rawValue: parts[3]).map { .device(id, .app($0)) }
            default: return nil
            }
        default:
            return nil
        }
    }

    private static func legacy(_ name: String?) -> SettingsRoute? {
        switch name {
        case "general", "app": .app(.general)
        case "connection": .app(.connection)
        case "permissions": .app(.permissions)
        case "sounds": .app(.sounds)
        case "clock", "device": .device(DeviceKind.clock.placeholderID, .hardware(.status))
        case "knob": .device(DeviceKind.knob.placeholderID, .hardware(.status))
        case "agents", "display": .source(.agents)
        case "focus", "pomodoro": .source(.focus)
        case "weather": .source(.weather)
        case "calendar", "meetings", "reminders": .source(.calendar)
        default: nil
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
