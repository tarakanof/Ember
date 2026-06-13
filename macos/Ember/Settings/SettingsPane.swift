import Foundation

enum SettingsPane: String, CaseIterable, Identifiable, Hashable {
    case connection, device, display, pomodoro, weather, reminders, meetings, app
    var id: String { rawValue }

    var title: String {
        switch self {
        case .connection: "Connection"
        case .device:     "Device"
        case .display:    "Agent"
        case .pomodoro:   "Pomodoro"
        case .weather:    "Weather"
        case .reminders:  "Reminders"
        case .meetings:   "Meetings"
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
        case .meetings:   "calendar"
        case .app:        "app.badge"
        }
    }
}
