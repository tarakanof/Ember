import Foundation

enum SettingsPane: String, CaseIterable, Identifiable, Hashable {
    case connection, device, display, pomodoro, weather, reminders, app
    var id: String { rawValue }

    var title: String {
        switch self {
        case .connection: "Connection"
        case .device:     "Device"
        case .display:    "Code agent"
        case .pomodoro:   "Pomodoro"
        case .weather:    "Weather"
        case .reminders:  "Reminders"
        case .app:        "App"
        }
    }

    var systemImage: String {
        switch self {
        case .connection: "network"
        case .device:     "display"
        case .display:    "chevron.left.forwardslash.chevron.right"
        case .pomodoro:   "timer"
        case .weather:    "cloud.sun"
        case .reminders:  "bell"
        case .app:        "app.badge"
        }
    }
}
