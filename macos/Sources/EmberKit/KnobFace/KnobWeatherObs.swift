import Foundation

struct KnobWeatherObs: Sendable {
    var valid: Bool
    var still: Bool
    var look: KnobWeatherLook
    var provider: String
    var now: Int64
    var faces: WeatherState.Faces?
    var sunrise: Int64?
    var sunset: Int64?

    init(state: WeatherState?, now: Date, theme: KnobTheme.Weather, calendar: Calendar) {
        look = KnobWeatherLook(state: state, now: now, maxAge: theme.maxAgeS, calendar: calendar)
        valid = state?.current != nil
        still = look.still
        provider = state?.provider ?? ""
        self.now = Self.unix(now)
        faces = state?.faces
        sunrise = state?.sun.map { Self.unix($0.sunrise) }
        sunset = state?.sun.map { Self.unix($0.sunset) }
    }

    static func unix(_ d: Date) -> Int64 { Int64(d.timeIntervalSince1970.rounded(.down)) }

    var fresh: Bool { valid && !still }
    var offset: Int64? { faces.map { Int64($0.utcOffsetS) } }
    var pop: Int? { faces?.todayPopPct.map { min(max($0, 0), 100) } }

    static func mod(_ a: Int64, _ m: Int64) -> Int64 { ((a % m) + m) % m }

    static func hhmm(_ t: Int64, offset: Int64) -> String {
        let m = mod(t + offset, 86_400) / 60
        return String(format: "%02d:%02d", Int(m / 60), Int(m % 60))
    }

    struct Spell: Equatable, Sendable {
        var start, end: Int64
        var intensity: Int
        var snow: Bool
    }

    var spells: [Spell] {
        guard let r = faces?.rain else { return [] }
        return Array(r.spells.compactMap { s in
            let a = Self.unix(s.start), b = Self.unix(s.end)
            guard b > a, (1...3).contains(s.intensity) else { return nil }
            return Spell(start: a, end: b, intensity: s.intensity, snow: s.snow)
        }.prefix(4))
    }

    var rainUntil: Int64? { faces?.rain?.until.map(Self.unix) }
    var nowRain: Int? { faces?.rainIntensity.flatMap { (0...3).contains($0) ? $0 : nil } }
    var hasRain: Bool { faces?.rain != nil }

    static func ramp(_ t: Double, _ a: RGB, _ b: RGB) -> RGB {
        let t = min(max(t, 0), 1)
        func ch(_ x: UInt8, _ y: UInt8) -> UInt8 {
            UInt8(min(max((Double(x) + (Double(y) - Double(x)) * t).rounded(), 0), 255))
        }
        return RGB(r: ch(a.r, b.r), g: ch(a.g, b.g), b: ch(a.b, b.b))
    }
}

public struct KnobSubRun: Equatable, Sendable {
    public var text: String
    public var color: RGB?
}

public enum KnobSubMarkup {
    public static func runs(_ s: String) -> [KnobSubRun] {
        var out: [KnobSubRun] = []
        var rest = Substring(s)
        while let open = rest.firstIndex(of: "#") {
            if open > rest.startIndex { out.append(.init(text: String(rest[..<open]), color: nil)) }
            let hex = rest[rest.index(after: open)...].prefix(6)
            guard hex.count == 6, let c = RGB(hex: "#" + hex),
                  let close = rest[rest.index(open, offsetBy: 7)...].firstIndex(of: "#") else {
                out.append(.init(text: String(rest[open...]), color: nil))
                return out
            }
            var text = rest[rest.index(open, offsetBy: 7)..<close]
            if text.first == " " { text = text.dropFirst() }
            out.append(.init(text: String(text), color: c))
            rest = rest[rest.index(after: close)...]
        }
        if !rest.isEmpty { out.append(.init(text: String(rest), color: nil)) }
        return out
    }

    public static func plain(_ s: String) -> String { runs(s).map(\.text).joined() }
}

public enum KnobCount {
    public static func text(hours: Bool, count: Int) -> String {
        guard hours else { return "\(count * 5)" }
        let whole = count / 2, half = count % 2 == 1
        return whole > 0 || !half ? "\(whole)\(half ? "½" : "")" : "½"
    }
}
