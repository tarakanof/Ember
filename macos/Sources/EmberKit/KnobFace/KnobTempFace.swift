import CoreGraphics
import Foundation

public struct KnobTempFace: Equatable, Sendable {
    public var value: Int?
    public var still: Bool
    public var lo: Int
    public var hi: Int
    public var hasRange: Bool
    public var sub: String

    public init(value: Double?, still: Bool = false, todayMinC: Double? = nil, todayMaxC: Double? = nil, sub: String = "",
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
        let v = Int(min(max(value, -99), 99).rounded())
        self.value = v
        self.sub = sub
        if let mn = todayMinC, let mx = todayMaxC, mn.isFinite, mx.isFinite {
            hasRange = true
            lo = min(Int(mn.rounded()), v)
            hi = max(Int(mx.rounded()), v)
            if lo == hi {
                lo -= 1
                hi += 1
            }
        }
    }

    public init(state: WeatherState?, now: Date, theme: KnobTheme.Weather = KnobTheme.standard.weather,
                calendar: Calendar = .current) {
        let look = KnobWeatherLook(state: state, now: now, maxAge: theme.maxAgeS, calendar: calendar)
        let cur = state?.current
        self.init(value: cur?.tempC, still: look.still, todayMinC: cur?.todayMinC, todayMaxC: cur?.todayMaxC,
                  sub: Self.sky(look), scale: theme.scale)
    }

    public var fraction: Double {
        guard let value, hi > lo else { return 0 }
        return min(max(Double(value - lo) / Double(hi - lo), 0), 1)
    }

    public func valueText() -> String { value.map(Self.degrees) ?? "--°" }

    public func valueColor(_ theme: KnobTheme.Weather) -> RGB {
        guard let value, !still else { return theme.stillColor }
        return Self.color(Double(value), theme.scale)
    }

    static func degrees(_ v: Int) -> String { v < 0 ? "\u{2212}\(-v)°" : "\(v)°" }

    static func sky(_ look: KnobWeatherLook) -> String {
        func amount(_ word: String) -> String {
            switch look.intensity {
            case .light: "Light \(word.lowercased())"
            case .heavy: "Heavy \(word.lowercased())"
            case .none, .moderate: word
            }
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

    public static func gaugePoint(_ fraction: Double, _ theme: KnobTheme.Weather, center: Double) -> CGPoint {
        let a = (theme.ring.fromDeg + theme.ring.sweepDeg * fraction) * .pi / 180
        return CGPoint(x: center + theme.ring.radiusPx * sin(a), y: center - theme.ring.radiusPx * cos(a))
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
