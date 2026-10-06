import Foundation

public enum DeviceKind: String, CaseIterable, Sendable {
    case clock = "awtrix-ng"
    case knob = "cinder-knob"

    public var placeholderID: String {
        switch self {
        case .clock: "clock"
        case .knob: "knob"
        }
    }

    public init?(deviceID id: String) {
        guard let kind = Self.allCases.first(where: { id == $0.placeholderID || id.hasPrefix($0.placeholderID + "-") })
        else { return nil }
        self = kind
    }
}

public enum AppCatalog {
    public static let knobDefaultPages = ["bot", "pomodoro", "weather"]

    public static func hardware(_ kind: DeviceKind) -> [HardwarePage] {
        switch kind {
        case .clock: [.status, .health, .display, .timeDate, .buttons, .sensors, .sounds]
        case .knob: [.status, .health, .display, .behavior]
        }
    }

    public static func apps(_ kind: DeviceKind, supportedPages: [String]? = nil) -> [AppID] {
        switch kind {
        case .clock:
            return [.agents, .focus, .weather, .calendar]
        case .knob:
            let pages = Set(supportedPages ?? knobDefaultPages)
            return [AppID.bot, .focus, .weather, .nowplaying].filter { knobPage($0).map(pages.contains) ?? false }
        }
    }

    public static func source(of app: AppID) -> SourceID? {
        switch app {
        case .agents, .bot: .agents
        case .focus: .focus
        case .weather: .weather
        case .calendar: .calendar
        case .nowplaying: .music
        }
    }

    public static func knobPage(_ app: AppID) -> String? {
        switch app {
        case .bot: "bot"
        case .focus: "pomodoro"
        case .weather: "weather"
        case .nowplaying: "nowplaying"
        case .agents, .calendar: nil
        }
    }
}
