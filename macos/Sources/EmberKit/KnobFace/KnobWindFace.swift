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

    struct Geo {
        var mount: CGPoint
        var droop: Double
        var quads: [[CGPoint]]
    }

    static let geo: [Geo] = [
        Geo(mount: CGPoint(x: 240.3, y: 78), droop: 64.3, quads: [
            [.init(x: 0.76, y: -22.79), .init(x: 27.62, y: -16.04), .init(x: 21.75, y: 17.69), .init(x: -0.76, y: 22.79)],
            [.init(x: 27.62, y: -16.04), .init(x: 50.99, y: 1.03), .init(x: 45.41, y: 15.74), .init(x: 21.75, y: 17.69)],
            [.init(x: 50.99, y: 1.03), .init(x: 73.5, y: 12.21), .init(x: 68.13, y: 24.42), .init(x: 45.41, y: 15.74)],
            [.init(x: 73.5, y: 12.21), .init(x: 95.63, y: 23.24), .init(x: 91.23, y: 33.26), .init(x: 68.13, y: 24.42)],
            [.init(x: 95.63, y: 23.24), .init(x: 117.76, y: 34.27), .init(x: 114.33, y: 42.1), .init(x: 91.23, y: 33.26)]]),
        Geo(mount: CGPoint(x: 201.7, y: 101), droop: 26.8, quads: [
            [.init(x: 0, y: -22.8), .init(x: 24.7, y: -20.14), .init(x: 24.7, y: 20.14), .init(x: 0, y: 22.8)],
            [.init(x: 24.7, y: -20.14), .init(x: 49.4, y: -17.48), .init(x: 49.4, y: 17.48), .init(x: 24.7, y: 20.14)],
            [.init(x: 49.4, y: -17.48), .init(x: 75.42, y: -14.76), .init(x: 72.78, y: 14.76), .init(x: 49.4, y: 17.48)],
            [.init(x: 75.42, y: -14.76), .init(x: 100.57, y: -2.6), .init(x: 96.26, y: 11.34), .init(x: 72.78, y: 14.76)],
            [.init(x: 100.57, y: -2.6), .init(x: 122.69, y: 10.58), .init(x: 119.19, y: 18.38), .init(x: 96.26, y: 11.34)]]),
        Geo(mount: CGPoint(x: 191.2, y: 101), droop: 0, quads: [
            [.init(x: 0, y: -22.8), .init(x: 24.7, y: -20.14), .init(x: 24.7, y: 20.14), .init(x: 0, y: 22.8)],
            [.init(x: 24.7, y: -20.14), .init(x: 49.4, y: -17.48), .init(x: 49.4, y: 17.48), .init(x: 24.7, y: 20.14)],
            [.init(x: 49.4, y: -17.48), .init(x: 74.1, y: -14.82), .init(x: 74.1, y: 14.82), .init(x: 49.4, y: 17.48)],
            [.init(x: 74.1, y: -14.82), .init(x: 98.8, y: -12.16), .init(x: 98.8, y: 12.16), .init(x: 74.1, y: 14.82)],
            [.init(x: 98.8, y: -12.16), .init(x: 123.5, y: -9.5), .init(x: 123.5, y: 9.5), .init(x: 98.8, y: 12.16)]]),
    ]

    static let arm = 22.0, poleTop = 26.0, baseY = 205.0

    public static func sockQuads(level: Int) -> [[CGPoint]] {
        let g = geo[min(max(level, 0), geo.count - 1)]
        let r: Double = g.droop * .pi / 180
        let c = CGFloat(cos(r)), s = CGFloat(sin(r)), m = g.mount
        func turn(_ p: CGPoint) -> CGPoint {
            let x: CGFloat = m.x + p.x * c - p.y * s
            let y: CGFloat = m.y + p.x * s + p.y * c
            return CGPoint(x: x, y: y)
        }
        return g.quads.map { q in q.map(turn) }
    }

    static func mount(level: Int) -> CGPoint { geo[min(max(level, 0), geo.count - 1)].mount }
}
