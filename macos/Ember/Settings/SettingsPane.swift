import SwiftUI
import EmberKit

extension AppPane {
    var title: LocalizedStringResource {
        switch self {
        case .general:     "General"
        case .connection:  "Connection"
        case .permissions: "Permissions"
        case .sounds:      "Sounds & Alerts"
        }
    }

    var systemImage: String {
        switch self {
        case .general:     "gearshape"
        case .connection:  "network"
        case .permissions: "hand.raised"
        case .sounds:      "bell.badge"
        }
    }
}

extension SourceID {
    var title: LocalizedStringResource {
        switch self {
        case .agents:   "Agents"
        case .focus:    "Focus"
        case .weather:  "Weather"
        case .calendar: "Calendar"
        }
    }

    var systemImage: String {
        switch self {
        case .agents:   "sparkles"
        case .focus:    "timer"
        case .weather:  "cloud.sun"
        case .calendar: "calendar"
        }
    }
}

extension HardwarePage {
    var title: LocalizedStringResource {
        switch self {
        case .status:   "Status"
        case .health:   "Hardware"
        case .display:  "Display"
        case .timeDate: "Time & Date"
        case .buttons:  "Buttons"
        case .sensors:  "Sensors"
        case .sounds:   "Sounds"
        case .behavior: "Behavior"
        }
    }

    var systemImage: String {
        switch self {
        case .status:   "info.circle"
        case .health:   "waveform.path.ecg"
        case .display:  "sun.max"
        case .timeDate: "calendar.badge.clock"
        case .buttons:  "hand.tap"
        case .sensors:  "thermometer.medium"
        case .sounds:   "speaker.wave.2"
        case .behavior: "slider.horizontal.3"
        }
    }
}

extension AppID {
    var title: LocalizedStringResource {
        switch self {
        case .agents:   "Agents"
        case .bot:      "Bot"
        case .focus:    "Focus"
        case .weather:  "Weather"
        case .calendar: "Calendar"
        }
    }

    var systemImage: String {
        switch self {
        case .agents:   "sparkles"
        case .bot:      "face.smiling"
        case .focus:    "timer"
        case .weather:  "cloud.sun"
        case .calendar: "calendar"
        }
    }
}

extension DeviceKind {
    var title: LocalizedStringResource {
        switch self {
        case .clock: "Clock"
        case .knob:  "Knob"
        }
    }

    var systemImage: String {
        switch self {
        case .clock: "clock"
        case .knob:  "dial.medium"
        }
    }

    /// The Apps node's own page: the clock's rotation, the knob's pages.
    var appsTitle: LocalizedStringResource {
        switch self {
        case .clock: "Rotation"
        case .knob:  "Pages"
        }
    }

    var appsSystemImage: String {
        switch self {
        case .clock: "arrow.triangle.2.circlepath"
        case .knob:  "rectangle.stack"
        }
    }
}

extension SettingsTree {
    /// The node a device route sits under.
    func node(for route: SettingsRoute) -> SettingsTree.Device? {
        guard let id = route.deviceID else { return nil }
        return devices.first { $0.id == id }
    }

    private func kind(of route: SettingsRoute) -> DeviceKind {
        node(for: route)?.device.kind ?? route.deviceID.flatMap { DeviceKind(deviceID: $0) } ?? .clock
    }

    /// A row's title (device names come from the registry, not from here).
    func title(for route: SettingsRoute) -> LocalizedStringResource {
        switch route {
        case .app(let p): p.title
        case .source(let s): s.title
        case .device(_, .hardware(let h)): h.title
        case .device(_, .app(let a)): a.title
        case .device(_, .apps): kind(of: route).appsTitle
        }
    }

    func systemImage(for route: SettingsRoute) -> String {
        switch route {
        case .app(let p): p.systemImage
        case .source(let s): s.systemImage
        case .device(_, .hardware(let h)): h.systemImage
        case .device(_, .app(let a)): a.systemImage
        case .device(_, .apps): kind(of: route).appsSystemImage
        }
    }

    /// The window title: the pane's name, or "Weather — Clock" under a device.
    func windowTitle(for route: SettingsRoute) -> Text {
        guard let node = node(for: route) else { return Text(title(for: route)) }
        let name = node.device.name
        if case .device(_, .hardware(.status)) = route { return Text(verbatim: name) }
        return Text("\(String(localized: title(for: route))) — \(name)",
                    comment: "Settings window title for a device's page: the page (\"Weather\"), then the device's name (\"Clock\").")
    }
}

private struct SettingsTreeKey: EnvironmentKey {
    static let defaultValue = SettingsTree(devices: [])
}

extension EnvironmentValues {
    /// The Settings sidebar, for panes that link across it ("Shown on").
    var settingsTree: SettingsTree {
        get { self[SettingsTreeKey.self] }
        set { self[SettingsTreeKey.self] = newValue }
    }
}

/// Selects a Settings route; an open Settings window follows at once.
@MainActor
func showSettings(_ route: SettingsRoute) {
    let defaults = UserDefaults.standard
    defaults.set(route.stored, forKey: SettingsRoute.storageKey)
    defaults.set(defaults.integer(forKey: SettingsRoute.revealKey) &+ 1, forKey: SettingsRoute.revealKey)
}

/// The TC001's node id until it becomes a registry record.
let clockDeviceID = DeviceKind.clock.placeholderID
