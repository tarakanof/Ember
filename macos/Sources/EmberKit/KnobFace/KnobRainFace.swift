import Foundation

public struct KnobRainFace: Equatable, Sendable {
    public enum Kind: Sendable, Equatable { case rain, snow, storm }

    public var dry: Bool
    public var kind: Kind = .rain
    public var intensity: Int
    public var pop: Int?
    public var untilMin: Int?
    public var sub: String = ""

    public init(dry: Bool, kind: Kind = .rain, intensity: Int, pop: Int? = nil, untilMin: Int? = nil, sub: String = "") {
        self.dry = dry; self.kind = kind; self.intensity = intensity; self.pop = pop; self.untilMin = untilMin; self.sub = sub
    }

    public init(state: WeatherState?, now: Date, shown: KnobRainFace? = nil,
                theme: KnobTheme.Weather = KnobTheme.standard.weather, calendar: Calendar = .current) {
        let o = KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar)
        var r = Self.wet(o) ?? Self.dry(o)
        r.keep(shown)
        self = r
    }

    static func word(_ k: Kind, _ i: Int) -> String {
        switch k {
        case .storm: "Storm"
        case .snow: i == 1 ? "Light snow" : i == 3 ? "Heavy snow" : "Snow"
        case .rain: i == 1 ? "Light rain" : i == 3 ? "Heavy rain" : "Rain"
        }
    }

    static let dryWord = "Dry now"

    static func intensity(_ i: KnobWeatherLook.Intensity) -> Int {
        switch i {
        case .none: 0
        case .light: 1
        case .moderate: 2
        case .heavy: 3
        }
    }

    static func wet(_ o: KnobWeatherObs) -> KnobRainFace? {
        guard o.fresh else { return nil }
        let l = o.look, now = o.now, spells = o.spells
        let sp = spells.last { $0.start <= now && now < $0.end }
        let storm = l.face == .storm
        let nowRain = o.nowRain
        var wet: Bool
        if nowRain != nil || o.hasRain { wet = (nowRain ?? 0) > 0 || sp != nil } else { wet = l.face == .rain || l.face == .snow }
        let until = o.rainUntil ?? 0
        let passed = until > 0 && now >= until
        if wet && passed { wet = sp.map { $0.start >= until } ?? false }
        guard wet || storm else { return nil }
        let kind: Kind = storm ? .storm : l.face == .snow ? .snow : l.face == .rain ? .rain : sp?.snow == true ? .snow : .rain
        let raw = sp?.intensity ?? ((nowRain ?? 0) > 0 ? nowRain! : intensity(l.intensity))
        var r = KnobRainFace(dry: false, kind: kind, intensity: min(max(raw, 1), 3), pop: o.pop)
        if wet && !passed && until > 0, let off = o.offset {
            let m = Int(KnobWeatherObs.mod(until + off, 86_400) / 60)
            r.untilMin = m
            r.sub = String(format: "until %02d:%02d", m / 60, m % 60)
        } else {
            r.sub = word(kind, r.intensity)
        }
        return r
    }

    static func dry(_ o: KnobWeatherObs) -> KnobRainFace {
        guard o.fresh else { return KnobRainFace(dry: true, intensity: 1) }
        return KnobRainFace(dry: true, intensity: 1, pop: o.pop, sub: dryWord)
    }

    mutating func keep(_ shown: KnobRainFace?) {
        guard let shown, !dry, !shown.dry, kind == shown.kind, intensity < shown.intensity else { return }
        intensity = shown.intensity
        if untilMin == nil { sub = Self.word(kind, intensity) }
    }

    public static func color(_ intensity: Int, _ r: KnobTheme.Weather.Rain) -> RGB {
        r.colors[min(max(intensity, 1), r.colors.count) - 1]
    }

    public var valueText: String { pop.map(String.init) ?? "--" }

    public func valueColor(_ w: KnobTheme.Weather) -> RGB { pop == nil ? w.stillColor : Self.color(intensity, w.rain) }

    public var fraction: Double { Double(pop ?? 0) / 100 }

    public func ringColor(_ t: Double, _ w: KnobTheme.Weather) -> RGB {
        pop == nil ? w.ring.trackColor : KnobWeatherObs.ramp(t, w.rain.ringFrom, Self.color(intensity, w.rain))
    }
}
