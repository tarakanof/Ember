import Foundation

enum SettingsPane: String, CaseIterable, Identifiable, Hashable {
    case connection, display, pomodoro, app
    var id: String { rawValue }

    var title: String {
        switch self {
        case .connection: "Connection"
        case .display:    "Code agent"
        case .pomodoro:   "Pomodoro"
        case .app:        "App"
        }
    }

    var systemImage: String {
        switch self {
        case .connection: "network"
        case .display:    "chevron.left.forwardslash.chevron.right"
        case .pomodoro:   "timer"
        case .app:        "app.badge"
        }
    }
}
