import Foundation

enum SettingsPane: String, CaseIterable, Identifiable, Hashable {
    case connection, display, pomodoro, weather, app
    var id: String { rawValue }

    var title: String {
        switch self {
        case .connection: "Connection"
        case .display:    "Code agent"
        case .pomodoro:   "Pomodoro"
        case .weather:    "Weather"
        case .app:        "App"
        }
    }

    var systemImage: String {
        switch self {
        case .connection: "network"
        case .display:    "chevron.left.forwardslash.chevron.right"
        case .pomodoro:   "timer"
        case .weather:    "cloud.sun"
        case .app:        "app.badge"
        }
    }
}
