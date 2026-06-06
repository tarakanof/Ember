import Foundation

enum SettingsPane: String, CaseIterable, Identifiable, Hashable {
    case connection, display, pomodoro, app
    var id: String { rawValue }

    var title: String {
        switch self {
        case .connection: "Connection"
        case .display:    "Display"
        case .pomodoro:   "Pomodoro"
        case .app:        "App"
        }
    }

    var systemImage: String {
        switch self {
        case .connection: "network"
        case .display:    "rectangle.dashed"
        case .pomodoro:   "timer"
        case .app:        "app.badge"
        }
    }
}
