import Foundation

public struct KnobAirFace: Equatable, Sendable {
    public var have = false
    public var aqi = 0
    public var band = 0
    public var wilted = false
    public var fraction = 0.0

    public init() {}

    public init(state: WeatherState?, now: Date, shown: KnobAirFace? = nil,
                theme: KnobTheme.Weather = KnobTheme.standard.weather, calendar: Calendar = .current) {
        let o = KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar)
        let t = theme.air
        guard !o.still, let raw = o.faces?.aqi, (0...t.max).contains(raw) else { return }
        have = true
        aqi = raw
        band = Self.band(aqi, t)
        wilted = Self.wilted(aqi, shown: shown.flatMap { $0.have ? $0.wilted : nil }, t)
        fraction = Double(min(aqi, t.gauge)) / Double(t.gauge)
    }

    static let words = ["Good", "Fair", "Moderate", "Poor", "Very poor", "Extremely poor"]

    public static func band(_ aqi: Int, _ t: KnobTheme.Weather.Air) -> Int {
        var b = 0
        while b < t.bandMax.count - 1 && aqi > t.bandMax[b] { b += 1 }
        return b
    }

    public static func word(_ band: Int) -> String { words[min(max(band, 0), words.count - 1)] }

    public static func bandColor(_ band: Int, _ t: KnobTheme.Weather.Air) -> RGB {
        t.bandColors[min(max(band, 0), t.bandColors.count - 1)]
    }

    public static func wilted(_ aqi: Int, shown: Bool?, _ t: KnobTheme.Weather.Air) -> Bool {
        switch shown {
        case true?: aqi > t.poor - t.hyst
        case false?: aqi > t.poor + t.hyst
        case nil: aqi > t.poor
        }
    }

    public static func color(_ aqi: Int, _ t: KnobTheme.Weather.Air) -> RGB {
        let s = t.stops, c = t.stopColors
        guard let first = s.first, let last = s.last, s.count == c.count else { return RGB(r: 0, g: 0, b: 0) }
        if aqi <= first { return c[0] }
        if aqi >= last { return c[c.count - 1] }
        var i = 1
        while aqi > s[i] { i += 1 }
        return KnobWeatherObs.ramp(Double(aqi - s[i - 1]) / Double(s[i] - s[i - 1]), c[i - 1], c[i])
    }

    public var valueText: String { valueText(t: nil) }

    func valueText(t: Double?) -> String { have ? "\(KnobArrive.count(aqi, t))" : "--" }

    public var unit: String? { have ? "AQI" : nil }

    public func valueColor(_ w: KnobTheme.Weather, t: Double? = nil) -> RGB { have ? Self.color(KnobArrive.count(aqi, t), w.air) : w.stillColor }

    public var sub: String { have ? Self.word(band) : "" }

    public func subColor(_ w: KnobTheme.Weather) -> RGB { Self.bandColor(have ? band : 0, w.air) }

    public func ringColor(_ f: Double, _ w: KnobTheme.Weather) -> RGB {
        have ? Self.color(Int((Double(w.air.gauge) * f).rounded()), w.air) : w.ring.trackColor
    }
}
