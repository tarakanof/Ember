import Foundation

public struct KnobWeatherLook: Equatable, Sendable {
    public enum Face: String, Sendable, CaseIterable {
        case clearDay, clearNight, partlyCloudy, overcast, fog, rain, snow, storm
    }

    public enum Intensity: Sendable, Equatable { case none, light, moderate, heavy }

    public var face: Face
    public var intensity: Intensity = .none
    public var night = false
    public var still = false

    public init(face: Face, intensity: Intensity = .none, night: Bool = false, still: Bool = false) {
        self.face = face; self.intensity = intensity; self.night = night; self.still = still
    }

    public init(provider: String, condition: String, code: String?, night: Bool?) {
        var look: KnobWeatherLook?
        var suffixNight: Bool?
        if let code, !code.isEmpty {
            if let v = Int(code) { look = Self.wmo(v) }
            else if provider != "open-meteo" { (look, suffixNight) = Self.met(code) }
        }
        var l = look ?? Self.bucket(condition)
        l.night = (night ?? suffixNight) == true
        if l.face == .clearDay && l.night { l.face = .clearNight }
        self = l
    }

    public init(state: WeatherState?, now: Date, maxAge: TimeInterval, calendar: Calendar = .current) {
        guard let state, let cur = state.current else {
            self.init(face: .overcast, still: true)
            return
        }
        var night: Bool?
        if let sun = state.sun {
            let rise = Self.minute(sun.sunrise, calendar), set = Self.minute(sun.sunset, calendar)
            let then = Self.isNight(now: Self.minute(state.generatedAt, calendar), rise: rise, set: set)
            let rounded = Self.isNight(now: Self.minute(now, calendar), rise: rise, set: set)
            let since = now.timeIntervalSince(state.generatedAt)
            let fetched = Self.second(state.generatedAt, calendar)
            func passed(_ r: Date) -> Bool {
                let ahead = (Self.second(r, calendar) + Self.sunRoundingHalfS - fetched + 2 * Self.day)
                    .truncatingRemainder(dividingBy: Self.day)
                return ahead > 0 && ahead <= since
            }
            let crossed = since < 0 || then != rounded || passed(sun.sunrise) || passed(sun.sunset)
            night = crossed ? rounded : sun.night ?? rounded
        }
        self.init(provider: state.provider, condition: cur.condition, code: cur.conditionCode, night: night)
        still = !state.enabled || cur.stale || now.timeIntervalSince(cur.fetchedAt) > maxAge
    }

    static let sunRoundingHalfS: TimeInterval = 150
    static let day: TimeInterval = 86_400

    static func second(_ d: Date, _ cal: Calendar) -> TimeInterval {
        let c = cal.dateComponents([.hour, .minute, .second], from: d)
        return TimeInterval((c.hour ?? 0) * 3600 + (c.minute ?? 0) * 60 + (c.second ?? 0))
    }

    static func minute(_ d: Date, _ cal: Calendar) -> Int {
        let c = cal.dateComponents([.hour, .minute], from: d)
        return (c.hour ?? 0) * 60 + (c.minute ?? 0)
    }

    static func isNight(now: Int, rise: Int, set: Int) -> Bool {
        if rise < set { return now < rise || now >= set }
        return now >= set && now < rise
    }

    static func bucket(_ c: String) -> KnobWeatherLook {
        switch c {
        case "clear": .init(face: .clearDay)
        case "fog": .init(face: .fog)
        case "rain": .init(face: .rain, intensity: .moderate)
        case "snow": .init(face: .snow, intensity: .moderate)
        case "storm": .init(face: .storm, intensity: .moderate)
        default: .init(face: .overcast)
        }
    }

    static func wmo(_ c: Int) -> KnobWeatherLook? {
        switch c {
        case 0: .init(face: .clearDay)
        case 1, 2: .init(face: .partlyCloudy)
        case 3: .init(face: .overcast)
        case 45, 48: .init(face: .fog)
        case 51, 53, 55, 56, 57: .init(face: .rain, intensity: .light)
        case 61, 63, 66, 80, 81: .init(face: .rain, intensity: .moderate)
        case 65, 67, 82: .init(face: .rain, intensity: .heavy)
        case 71, 77, 85: .init(face: .snow, intensity: .light)
        case 73: .init(face: .snow, intensity: .moderate)
        case 75, 86: .init(face: .snow, intensity: .heavy)
        case 95: .init(face: .storm, intensity: .moderate)
        case 96, 99: .init(face: .storm, intensity: .heavy)
        default: nil
        }
    }

    static func met(_ symbol: String) -> (KnobWeatherLook?, Bool?) {
        let s = String(symbol.lowercased().prefix(39))
        let night: Bool? = s.contains("_night") || s.contains("_polartwilight") ? true : s.contains("_day") ? false : nil
        let i: Intensity = s.contains("heavy") ? .heavy : s.contains("light") ? .light : .moderate
        let look: KnobWeatherLook? =
            if s.contains("thunder") { .init(face: .storm, intensity: i) }
            else if s.contains("sleet") { .init(face: .rain, intensity: i) }
            else if s.contains("snow") { .init(face: .snow, intensity: i) }
            else if s.contains("rain") || s.contains("showers") { .init(face: .rain, intensity: i) }
            else if s.contains("fog") { .init(face: .fog) }
            else if s.contains("partlycloudy") { .init(face: .partlyCloudy) }
            else if s.contains("cloudy") { .init(face: .overcast) }
            else if s.contains("clearsky") || s.contains("fair") { .init(face: .clearDay) }
            else { nil }
        return (look, night)
    }
}
