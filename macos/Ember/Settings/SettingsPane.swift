import SwiftUI
import EmberKit

/// The sidebar's panes. The raw values (`SettingsPaneID`) are the
/// cross-window contract: `openSettings(pane: "connection", …)`.
typealias SettingsPane = SettingsPaneID

extension SettingsPaneID {
    var title: LocalizedStringResource {
        switch self {
        case .general:    "General"
        case .connection: "Connection"
        case .clock:      "Clock"
        case .agents:     "Agents"
        case .focus:      "Focus"
        case .weather:    "Weather"
        case .calendar:   "Calendar"
        case .sounds:     "Sounds & Alerts"
        }
    }

    var systemImage: String {
        switch self {
        case .general:    "gearshape"
        case .connection: "network"
        case .clock:      "clock"
        case .agents:     "sparkles"
        case .focus:      "timer"
        case .weather:    "cloud.sun"
        case .calendar:   "calendar"
        case .sounds:     "bell.badge"
        }
    }
}
