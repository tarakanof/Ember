import Foundation

public struct ClockConfig: Codable, Equatable, Sendable {
    public var schema: Int
    public var apps: Apps
    public var rotation: Rotation?

    public init(schema: Int = 1, apps: Apps = Apps(), rotation: Rotation? = nil) {
        self.schema = schema
        self.apps = apps
        self.rotation = rotation
    }

    public struct Apps: Codable, Equatable, Sendable {
        public var agents: Agents
        public var focus: Focus
        public var weather: Weather
        public var calendar: Calendar

        public init(agents: Agents = Agents(), focus: Focus = Focus(), weather: Weather = Weather(),
                    calendar: Calendar = Calendar()) {
            self.agents = agents
            self.focus = focus
            self.weather = weather
            self.calendar = calendar
        }
    }

    public struct Agents: Codable, Equatable, Sendable {
        public var usageCards: Bool
        public var usagePerModel: Bool
        public var hiddenTools: [String]

        public init(usageCards: Bool = true, usagePerModel: Bool = true, hiddenTools: [String] = []) {
            self.usageCards = usageCards
            self.usagePerModel = usagePerModel
            self.hiddenTools = hiddenTools
        }

        enum CodingKeys: String, CodingKey {
            case usageCards = "usage_cards"
            case usagePerModel = "usage_per_model"
            case hiddenTools = "hidden_tools"
        }
    }

    public struct Focus: Codable, Equatable, Sendable {
        public var focusColor: String
        public var breakColor: String

        public init(focusColor: String = "", breakColor: String = "") {
            self.focusColor = focusColor
            self.breakColor = breakColor
        }

        enum CodingKeys: String, CodingKey {
            case focusColor = "focus_color"
            case breakColor = "break_color"
        }
    }

    public struct Weather: Codable, Equatable, Sendable {
        public var on: Bool
        public var nativeIcon: Bool
        public var forecast: Bool
        public var forecastHours: Int
        public var air: Bool
        public var moon: Bool
        public var overlay: Bool
        public var popups: Popups
        public var iconIds: [String: String]

        public init(on: Bool = true, nativeIcon: Bool = false, forecast: Bool = true, forecastHours: Int = 24,
                    air: Bool = true, moon: Bool = true, overlay: Bool = true, popups: Popups = Popups(),
                    iconIds: [String: String] = [:]) {
            self.on = on
            self.nativeIcon = nativeIcon
            self.forecast = forecast
            self.forecastHours = forecastHours
            self.air = air
            self.moon = moon
            self.overlay = overlay
            self.popups = popups
            self.iconIds = iconIds
        }

        enum CodingKeys: String, CodingKey {
            case on, forecast, air, moon, overlay, popups
            case nativeIcon = "native_icon"
            case forecastHours = "forecast_hours"
            case iconIds = "icon_ids"
        }
    }

    public struct Popups: Codable, Equatable, Sendable {
        public var onChange: Bool
        public var sun: Bool
        public var severe: Bool
        public var nativeIcons: Bool
        public var intervalMinutes: Int
        public var durationSeconds: Int

        public init(onChange: Bool = true, sun: Bool = true, severe: Bool = true, nativeIcons: Bool = false,
                    intervalMinutes: Int = 120, durationSeconds: Int = 30) {
            self.onChange = onChange
            self.sun = sun
            self.severe = severe
            self.nativeIcons = nativeIcons
            self.intervalMinutes = intervalMinutes
            self.durationSeconds = durationSeconds
        }

        enum CodingKeys: String, CodingKey {
            case sun, severe
            case onChange = "on_change"
            case nativeIcons = "native_icons"
            case intervalMinutes = "interval_minutes"
            case durationSeconds = "duration_seconds"
        }
    }

    public struct Calendar: Codable, Equatable, Sendable {
        public var on: Bool
        public var tileLeadMinutes: Int
        public var popupLeadMinutes: Int

        public init(on: Bool = true, tileLeadMinutes: Int = 60, popupLeadMinutes: Int = 2) {
            self.on = on
            self.tileLeadMinutes = tileLeadMinutes
            self.popupLeadMinutes = popupLeadMinutes
        }

        enum CodingKeys: String, CodingKey {
            case on
            case tileLeadMinutes = "tile_lead_minutes"
            case popupLeadMinutes = "popup_lead_minutes"
        }
    }

    public struct Rotation: Codable, Equatable, Sendable {
        public var order: [String]
        public var disabled: [String]

        public init(order: [String], disabled: [String]) {
            self.order = order
            self.disabled = disabled
        }

        public init(_ update: AppsUpdate) {
            self.init(order: update.order, disabled: update.disabled)
        }
    }

    public func rebased(onto current: ClockConfig, from sent: ClockConfig) -> ClockConfig {
        let edits = patch(from: sent)
        guard !edits.isEmpty, let base = try? JSONValue.object(encoding: current),
              let data = try? JSONEncoder().encode(JSONValue.object(Self.merge(base, edits, at: ""))),
              let merged = try? JSONDecoder().decode(ClockConfig.self, from: data) else { return current }
        return merged
    }

    private static func merge(_ base: [String: JSONValue], _ patch: [String: JSONValue], at path: String) -> [String: JSONValue] {
        var out = base
        for (key, value) in patch {
            let here = path.isEmpty ? key : path + "." + key
            if case .object(let p) = value, case .object(let b)? = base[key], !wholeValuePaths.contains(here) {
                out[key] = .object(merge(b, p, at: here))
            } else {
                out[key] = value
            }
        }
        return out
    }

    static let wholeValuePaths: Set<String> = ["apps.weather.icon_ids", "rotation"]

    public func patch(from old: ClockConfig) -> [String: JSONValue] {
        let new = (try? JSONValue.object(encoding: self)) ?? [:]
        let before = (try? JSONValue.object(encoding: old)) ?? [:]
        return Self.diff(new, before, at: "")
    }

    private static func diff(_ new: [String: JSONValue], _ old: [String: JSONValue], at path: String) -> [String: JSONValue] {
        var out: [String: JSONValue] = [:]
        for (key, value) in new where key != "schema" || !path.isEmpty {
            let before = old[key]
            guard value != before else { continue }
            let here = path.isEmpty ? key : path + "." + key
            if case .object(let n) = value, case .object(let o)? = before, !wholeValuePaths.contains(here) {
                let sub = diff(n, o, at: here)
                if !sub.isEmpty { out[key] = .object(sub) }
            } else {
                out[key] = value
            }
        }
        return out
    }
}

public protocol ClockAppSlice: Equatable, Sendable {
    associatedtype Source: Equatable & Sendable
    func apply(to source: inout Source)
    mutating func take(from source: Source)
}

extension ClockConfig.Agents: ClockAppSlice {
    public func apply(to source: inout UsageConfig) {
        source.usageWidget = usageCards
        source.usagePerModel = usagePerModel
    }

    public mutating func take(from source: UsageConfig) {
        usageCards = source.usageWidget
        usagePerModel = source.usagePerModel
    }
}

extension ClockConfig.Focus: ClockAppSlice {
    public func apply(to source: inout PomoConfig) {
        source.focusColor = focusColor
        source.breakColor = breakColor
    }

    public mutating func take(from source: PomoConfig) {
        focusColor = source.focusColor
        breakColor = source.breakColor
    }
}

extension ClockConfig.Weather: ClockAppSlice {
    public func apply(to source: inout WeatherConfig) {
        source.rotateInApps = on
        source.tileNativeIcons = nativeIcon
        source.forecastTile = forecast
        source.forecastHours = forecastHours
        source.airTile = air
        source.moonPhase = moon
        source.overlay = overlay
        source.popupOnChange = popups.onChange
        source.sunPopups = popups.sun
        source.severeAlert = popups.severe
        source.useNativeIcons = popups.nativeIcons
        source.popupIntervalMinutes = popups.intervalMinutes
        source.popupDurationSeconds = popups.durationSeconds
        source.iconIds = iconIds
    }

    public mutating func take(from source: WeatherConfig) {
        on = source.rotateInApps
        nativeIcon = source.tileNativeIcons
        forecast = source.forecastTile
        forecastHours = source.forecastHours
        air = source.airTile
        moon = source.moonPhase
        overlay = source.overlay
        popups.onChange = source.popupOnChange
        popups.sun = source.sunPopups
        popups.severe = source.severeAlert
        popups.nativeIcons = source.useNativeIcons
        popups.intervalMinutes = source.popupIntervalMinutes
        popups.durationSeconds = source.popupDurationSeconds
        iconIds = source.iconIds
    }
}

extension ClockConfig.Calendar: ClockAppSlice {
    public func apply(to source: inout MeetingsConfig) {
        source.enabled = on
        source.tileLeadMinutes = tileLeadMinutes
        source.popupLeadMinutes = popupLeadMinutes
    }

    public mutating func take(from source: MeetingsConfig) {
        on = source.enabled
        tileLeadMinutes = source.tileLeadMinutes
        popupLeadMinutes = source.popupLeadMinutes
    }
}
