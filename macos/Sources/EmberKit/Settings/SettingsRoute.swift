import Foundation

public enum AppPane: String, CaseIterable, Sendable {
    case general, connection, permissions, sounds
}

public enum SourceID: String, CaseIterable, Sendable {
    case agents, focus, weather, calendar
    case music
}

public enum HardwarePage: String, CaseIterable, Sendable {
    case status
    case health
    case display
    case timeDate = "time-date"
    case buttons, sensors, sounds, behavior
}

public enum AppID: String, CaseIterable, Sendable {
    case agents, bot, focus, weather, calendar, nowplaying
}

public enum DevicePage: Hashable, Sendable {
    case hardware(HardwarePage)
    case apps
    case app(AppID)
}

public enum SettingsRoute: Hashable, Sendable {
    case app(AppPane)
    case source(SourceID)
    case device(String, DevicePage)

    public static let storageKey = "settings.pane"
    public static let expandedKey = "settings.expanded"
    public static let revealKey = "settings.reveal"
    public static let fallback = SettingsRoute.app(.connection)

    public var stored: String {
        switch self {
        case .app(let p): "app/\(p.rawValue)"
        case .source(let s): "source/\(s.rawValue)"
        case .device(let id, .hardware(let h)): "device/\(id)/hardware/\(h.rawValue)"
        case .device(let id, .apps): "device/\(id)/apps"
        case .device(let id, .app(let a)): "device/\(id)/app/\(a.rawValue)"
        }
    }

    public var deviceID: String? {
        if case .device(let id, _) = self { id } else { nil }
    }

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

public enum MelodyChoice: Hashable, Sendable {
    case builtIn
    case stored(String)
    case custom

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

    public func value(replacing current: String, available: [String]) -> String {
        switch self {
        case .builtIn: return ""
        case .stored(let name): return name
        case .custom:
            return MelodyChoice(value: current, available: available) == .custom ? current : ""
        }
    }
}

public enum FocusPreset {
    public static let minutes = [15, 25, 45, 50, 90]
    public static let customRange = 5...180
}
