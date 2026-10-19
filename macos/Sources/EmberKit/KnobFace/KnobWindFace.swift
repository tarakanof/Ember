import Foundation

public struct KnobWindFace: Equatable, Sendable {
    public var have = false
    public var kmh = 0
    public var dir: Int?
    public var level = 0
    public var fraction = 0.0
    public var sub = ""

    public init() {}

    public init(state: WeatherState?, now: Date, shown: KnobWindFace? = nil,
                theme: KnobTheme.Weather = KnobTheme.standard.weather, calendar: Calendar = .current) {
        let o = KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar)
        let t = theme.wind
        guard o.fresh, let raw = o.faces?.windKmh, raw >= 0, raw <= t.maxKmh else { return }
        have = true
        kmh = min(max(raw, 0), t.maxKmh)
        dir = o.faces?.windDirDeg.flatMap { (0...360).contains($0) ? $0 % 360 : nil }
        level = Self.levelKeep(kmh, shown: shown.flatMap { $0.have ? $0.level : nil }, t)
        fraction = Double(min(kmh, t.gaugeKmh)) / Double(t.gaugeKmh)
        sub = "\(Self.color(kmh, t).hex) \(Self.band(kmh, t))#"
        if let dir, kmh > 0 { sub += " \u{2022} " + Self.compass(dir) }
    }

    static let bandWords = ["Calm", "Light air", "Light breeze", "Gentle breeze", "Moderate breeze", "Fresh breeze",
                            "Strong breeze", "Near gale", "Gale", "Strong gale", "Storm", "Violent storm", "Hurricane"]
    static let compassWords = ["N", "NE", "E", "SE", "S", "SW", "W", "NW"]

    static func bandIndex(_ kmh: Int, _ t: KnobTheme.Weather.Wind) -> Int {
        t.bandsKmh.firstIndex { kmh <= $0 } ?? t.bandsKmh.count
    }

    public static func color(_ kmh: Int, _ t: KnobTheme.Weather.Wind) -> RGB { t.colors[min(bandIndex(kmh, t), t.colors.count - 1)] }

    public static func band(_ kmh: Int, _ t: KnobTheme.Weather.Wind) -> String { bandWords[min(bandIndex(kmh, t), bandWords.count - 1)] }

    public static func dark(_ c: RGB, _ t: KnobTheme.Weather.Wind) -> RGB {
        func s(_ v: UInt8) -> UInt8 { UInt8((Double(v) * t.darkGain).rounded()) }
        return RGB(r: s(c.r), g: s(c.g), b: s(c.b))
    }

    public static func compass(_ dir: Int) -> String {
        let d = ((dir % 360) + 360) % 360
        return compassWords[((2 * d + 45) / 90) % 8]
    }

    public static func level(_ kmh: Int, _ t: KnobTheme.Weather.Wind) -> Int {
        var l = 0
        while l < t.edgesKmh.count && kmh >= t.edgesKmh[l] { l += 1 }
        return l
    }

    public static func levelKeep(_ kmh: Int, shown: Int?, _ t: KnobTheme.Weather.Wind) -> Int {
        let l = level(kmh, t), e = t.edgesKmh
        guard let shown, (0...e.count).contains(shown), l != shown else { return l }
        let above = shown == 0 || kmh >= e[shown - 1] - t.hystKmh
        let below = shown == e.count || kmh < e[shown] + t.hystKmh
        return above && below ? shown : l
    }

    public var valueText: String { have ? "\(kmh)" : "--" }

    public func valueColor(_ w: KnobTheme.Weather) -> RGB { have ? Self.color(kmh, w.wind) : w.stillColor }

    public func ringColor(_ f: Double, _ w: KnobTheme.Weather) -> RGB {
        have ? Self.color(Int((Double(w.wind.gaugeKmh) * f).rounded()), w.wind) : w.ring.trackColor
    }
}
