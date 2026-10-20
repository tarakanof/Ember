import CoreGraphics
import Foundation

public struct KnobTempFace: Equatable, Sendable {
    public var value: Int?
    public var still: Bool
    public var lo: Int
    public var hi: Int
    public var hasRange: Bool
    public var sub: String
    public var subColor: RGB?
    public var cloudBand: Int?

    public init(value: Double?, still: Bool = false, todayMinC: Double? = nil, todayMaxC: Double? = nil, sub: String = "",
                subColor: RGB? = nil, cloudBand: Int? = nil,
                scale: KnobTheme.Weather.Scale = KnobTheme.standard.weather.scale) {
        self.still = still
        lo = Int(scale.loC)
        hi = Int(scale.hiC)
        hasRange = false
        guard let value, value.isFinite else {
            self.value = nil
            self.sub = ""
            return
        }
        let v = Self.whole(value)
        self.value = v
        self.sub = sub
        self.subColor = subColor
        self.cloudBand = cloudBand
        if let mn = todayMinC, let mx = todayMaxC, mn.isFinite, mx.isFinite {
            hasRange = true
            lo = min(Self.whole(mn), v)
            hi = max(Self.whole(mx), v)
            if lo == hi {
                lo -= 1
                hi += 1
            }
        }
    }

    public init(state: WeatherState?, now: Date, theme: KnobTheme.Weather = KnobTheme.standard.weather,
                calendar: Calendar = .current, shownBand: Int? = nil) {
        let look = KnobWeatherLook(state: state, now: now, maxAge: theme.maxAgeS, calendar: calendar)
        let cur = state?.current
        let cloud = Self.reading(cur?.cloudPct, max: theme.cloud.maxPct)
        let uv = Self.reading(cur?.uv, max: theme.uv.max)
        let band = Self.cloudBandKeep(cloud ?? Self.assumedCloud(look.face, theme.cloud), shown: shownBand, theme.cloud)
        let advice = look.night ? nil : uv.flatMap { Self.uvAdvice($0, theme.uv) }
        self.init(value: cur?.tempC, still: look.still, todayMinC: cur?.todayMinC, todayMaxC: cur?.todayMaxC,
                  sub: advice ?? Self.sky(look, band: cloud == nil ? nil : band),
                  subColor: advice == nil ? nil : uv.map { Self.uvColor($0, theme.uv) }, cloudBand: band, scale: theme.scale)
    }

    static func reading(_ v: Double?, max: Int) -> Int? {
        guard let v, v >= 0, v <= Double(max) else { return nil }
        return Int(v.rounded())
    }

    static func assumedCloud(_ face: KnobWeatherLook.Face, _ c: KnobTheme.Weather.Cloud) -> Int {
        switch face {
        case .clearDay, .clearNight: c.clearPct
        case .partlyCloudy: c.partlyPct
        case .overcast, .fog, .rain, .snow, .storm: c.otherPct
        }
    }

    public static func cloudBand(_ cloud: Int, _ c: KnobTheme.Weather.Cloud) -> Int {
        var b = 0
        while b < c.edgesPct.count && cloud >= c.edgesPct[b] { b += 1 }
        return b
    }

    public static func cloudBandKeep(_ cloud: Int, shown: Int?, _ c: KnobTheme.Weather.Cloud) -> Int {
        let b = cloudBand(cloud, c), e = c.edgesPct
        guard let shown, (0...e.count).contains(shown), b != shown else { return b }
        let above = shown == 0 || cloud >= e[shown - 1] - c.hystPct
        let below = shown == e.count || cloud < e[shown] + c.hystPct
        return above && below ? shown : b
    }

    static let skyWords = [["Sunny", "Mostly sunny", "Partly cloudy", "Cloudy", "Overcast"],
                           ["Clear", "Mostly clear", "Partly cloudy", "Cloudy", "Overcast"]]
    static let uvWords = ["Sunglasses on", "Use sunscreen", "Sunscreen & shade", "Avoid the sun"]

    static func uvLevel(_ uv: Int, _ u: KnobTheme.Weather.UV) -> Int { u.levelMax.firstIndex { uv <= $0 } ?? u.levelMax.count }

    public static func uvAdvice(_ uv: Int, _ u: KnobTheme.Weather.UV) -> String? {
        uv < u.adviceFrom ? nil : uvWords[min(uvLevel(uv, u), uvWords.count - 1)]
    }

    public static func uvColor(_ uv: Int, _ u: KnobTheme.Weather.UV) -> RGB { u.colors[min(uvLevel(uv, u), u.colors.count - 1)] }

    static func whole(_ c: Double) -> Int { Int(min(max(c, -99), 99).rounded()) }

    public var fraction: Double {
        guard let value, hi > lo else { return 0 }
        return min(max(Double(value - lo) / Double(hi - lo), 0), 1)
    }

    public func valueText(t: Double? = nil) -> String { value.map { Self.degrees(KnobArrive.count($0, t)) } ?? "--°" }

    public enum Caption: Equatable, Sendable {
        case noData
        case current(sky: String, temp: String)
        case stale(sky: String, temp: String)
    }

    public var caption: Caption {
        guard value != nil else { return .noData }
        return still ? .stale(sky: sub, temp: valueText()) : .current(sky: sub, temp: valueText())
    }

    public func valueColor(_ theme: KnobTheme.Weather, t: Double? = nil) -> RGB {
        guard let value, !still else { return theme.stillColor }
        return Self.color(Double(KnobArrive.count(value, t)), theme.scale)
    }

    static func degrees(_ v: Int) -> String { v < 0 ? "\u{2212}\(-v)°" : "\(v)°" }

    static func sky(_ look: KnobWeatherLook, band: Int? = nil) -> String {
        func amount(_ word: String) -> String {
            switch look.intensity {
            case .light: "Light \(word.lowercased())"
            case .heavy: "Heavy \(word.lowercased())"
            case .none, .moderate: word
            }
        }
        let words = skyWords[look.night ? 1 : 0]
        if let band, ![.fog, .rain, .snow, .storm].contains(look.face) {
            return words[min(max(band, 0), words.count - 1)]
        }
        return switch look.face {
        case .fog: "Fog"
        case .rain: amount("Rain")
        case .snow: amount("Snow")
        case .storm: "Storm"
        case .partlyCloudy: "Partly cloudy"
        case .overcast: "Overcast"
        case .clearDay, .clearNight: look.night ? "Clear" : "Sunny"
        }
    }

    public static func color(_ c: Double, _ scale: KnobTheme.Weather.Scale) -> RGB {
        let t = scale.stopsC.map(Float.init), rgb = scale.colors, x = Float(c)
        guard let first = rgb.first, let last = rgb.last, t.count == rgb.count else { return RGB(r: 0, g: 0, b: 0) }
        if !(x > t[0]) { return first }
        if x >= t[t.count - 1] { return last }
        var i = 1
        while x > t[i] { i += 1 }
        let f = (x - t[i - 1]) / (t[i] - t[i - 1])
        func ch(_ a: UInt8, _ b: UInt8) -> UInt8 {
            let v = (Float(a) + (Float(b) - Float(a)) * f).rounded()
            return UInt8(min(max(v, 0), 255))
        }
        let a = rgb[i - 1], b = rgb[i]
        return RGB(r: ch(a.r, b.r), g: ch(a.g, b.g), b: ch(a.b, b.b))
    }

    public static func mercuryTop(_ c: Double, _ t: KnobTheme.Weather.Thermo) -> Double {
        let m = t.mercuryTop
        return min(max(m.refPx - (c - m.refC) * m.risePx / m.perC, m.minPx), m.maxPx)
    }

    public static func dotPoint(_ k: Int, _ d: KnobTheme.Weather.Dots, center: Double) -> CGPoint {
        let a = (Double(k) - Double(d.faces - 1) / 2) * d.pitchPx / d.radiusPx
        return CGPoint(x: center + d.radiusPx * sin(a), y: center + d.radiusPx * cos(a))
    }
}
