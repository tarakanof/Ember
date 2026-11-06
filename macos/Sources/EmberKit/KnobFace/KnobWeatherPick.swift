import Foundation

public enum KnobWeatherFaceID: String, CaseIterable, Sendable {
    case temp, rain, cast, wind, days, sun, air

    public var dot: Int { Self.allCases.firstIndex(of: self) ?? 0 }
}

public enum KnobWeatherAuto {
    static let order: [KnobWeatherFaceID] = [.rain, .cast, .wind, .air]

    public static func face(state: WeatherState?, now: Date, shown: KnobWeatherFaceID? = nil,
                            theme: KnobTheme.Weather = KnobTheme.standard.weather,
                            calendar: Calendar = .current) -> KnobWeatherFaceID {
        let o = KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar)
        return order.first { picks($0, o, kept: shown == $0, theme) } ?? .temp
    }

    static func picks(_ f: KnobWeatherFaceID, _ o: KnobWeatherObs, kept: Bool, _ t: KnobTheme.Weather) -> Bool {
        switch f {
        case .rain: KnobRainFace.wet(o) != nil
        case .cast: castSoon(o, kept: kept, t.cast)
        case .wind: windStrong(o, kept: kept, t.wind)
        case .air: airPoor(o, kept: kept, t.air)
        case .temp, .days, .sun: false
        }
    }

    static func castSoon(_ o: KnobWeatherObs, kept: Bool, _ t: KnobTheme.Weather.Cast) -> Bool {
        guard let s = KnobCastFace.scan(o), o.hasRain, o.offset != nil, !s.wet, let next = s.next else { return false }
        return next.start - s.now <= Int64(kept ? t.keepS : t.soonS)
    }

    static func windStrong(_ o: KnobWeatherObs, kept: Bool, _ t: KnobTheme.Weather.Wind) -> Bool {
        guard o.fresh, let kmh = o.faces?.windKmh, (0...t.maxKmh).contains(kmh) else { return false }
        return kmh >= (kept ? t.keepKmh : t.autoKmh)
    }

    static func airPoor(_ o: KnobWeatherObs, kept: Bool, _ t: KnobTheme.Weather.Air) -> Bool {
        guard !o.still, let aqi = o.faces?.aqi, (0...t.max).contains(aqi) else { return false }
        return aqi > (kept ? t.poor - t.hyst : t.poor)
    }
}
