import Foundation

/// The kinds of display Ember drives; raw values are the registry `kind`.
public enum DeviceKind: String, CaseIterable, Sendable {
    /// The Ulanzi TC001 on awtrix-ng (push: the server renders every frame).
    case clock = "awtrix-ng"
    /// The cinder round knob (pull: it reads state and its own config).
    case knob = "cinder-knob"

    /// The id a node of this kind has while no registry record names it.
    public var placeholderID: String {
        switch self {
        case .clock: "clock"
        case .knob: "knob"
        }
    }

    /// The kind a device id belongs to: its placeholder, or a registry id
    /// starting with it (`knob-61fc8c`); nil for anything else.
    public init?(deviceID id: String) {
        guard let kind = Self.allCases.first(where: { id == $0.placeholderID || id.hasPrefix($0.placeholderID + "-") })
        else { return nil }
        self = kind
    }
}

/// Which pages and apps each device kind has.
public enum AppCatalog {
    /// What knob firmware without a supported-pages report draws.
    public static let knobDefaultPages = ["bot", "pomodoro", "weather"]

    /// The kind's hardware pages, in sidebar order.
    public static func hardware(_ kind: DeviceKind) -> [HardwarePage] {
        switch kind {
        case .clock: [.status, .health, .display, .timeDate, .buttons, .sensors, .sounds]
        case .knob: [.status, .health, .display, .behavior]
        }
    }

    /// The apps a device of this kind can show, in sidebar order. For the
    /// knob, only apps whose page the firmware draws (`supportedPages`, or
    /// `knobDefaultPages` when it doesn't say).
    public static func apps(_ kind: DeviceKind, supportedPages: [String]? = nil) -> [AppID] {
        switch kind {
        case .clock:
            return [.agents, .focus, .weather, .calendar]
        case .knob:
            let pages = Set(supportedPages ?? knobDefaultPages)
            return [AppID.bot, .focus, .weather].filter { knobPage($0).map(pages.contains) ?? false }
        }
    }

    /// The source an app presents, or nil for one that has none.
    public static func source(of app: AppID) -> SourceID? {
        switch app {
        case .agents, .bot: .agents
        case .focus: .focus
        case .weather: .weather
        case .calendar: .calendar
        }
    }

    /// The knob page id that shows this app, or nil when the knob has none.
    public static func knobPage(_ app: AppID) -> String? {
        switch app {
        case .bot: "bot"
        case .focus: "pomodoro"
        case .weather: "weather"
        case .agents, .calendar: nil
        }
    }
}
