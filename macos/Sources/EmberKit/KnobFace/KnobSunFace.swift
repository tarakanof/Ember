import Foundation

public struct KnobSunFace: Equatable, Sendable {
    public enum Polar: Sendable, Equatable { case none, day, night }
    public enum Phase: Sendable, Equatable { case before, morning, day, evening, after }
    public enum Event: Int, Sendable, CaseIterable { case dawn, blueEnd, rise, goldEnd, goldStart, set, blueStart, dusk }

    public var have = false
    public var polar = Polar.none
    public var t: [Int64] = Array(repeating: 0, count: Event.allCases.count)
    public var offset: Int64 = 0
    public var hasNow = false
    public var phase = Phase.before
    public var fraction = 0.0
    public var hours = false
    public var count = 0
    public var unit: String?
    public var rise = ""
    public var set = ""
    public var sub = ""
    public var subColor: RGB

    public init(state: WeatherState?, now: Date, theme: KnobTheme.Weather = KnobTheme.standard.weather,
                calendar: Calendar = .current) {
        self.init(KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar), theme.sun)
    }

    static let polarNight = "Polar night", midnightSun = "Midnight sun", untilSunrise = "until sunrise",
               daylight = "of daylight today", goldenTo = "golden hour to", sunsetAt = "sunset", golden = "golden hour"

    init(_ o: KnobWeatherObs, _ th: KnobTheme.Weather.Sun) {
        subColor = th.subColor
        guard o.valid else { return }
        hasNow = true
        let now = o.now, day: Int64 = 86_400
        guard let r = o.sunrise, let s = o.sunset, r > 0, s > r, s - r < day, let off = o.offset else {
            if o.sunrise == nil, let f = o.faces, f.moon?.up != nil {
                polar = f.night ? .night : .day
                sub = f.night ? Self.polarNight : Self.midnightSun
            }
            return
        }
        have = true
        offset = off
        let d = ((r + s) / 2 + off) / day
        var late = (now + off) / day - d
        guard late >= 0 && late <= Int64(th.rollDays) else {
            have = false
            return
        }
        late = (now + off - Int64(th.rollGraceS)) / day - d
        t = Self.crossings(r, s, o.faces?.twilight, span: Int64(th.spanMaxS))
        if late > 0 { t = t.map { $0 != 0 ? $0 + late * day : 0 } }
        rise = KnobWeatherObs.hhmm(t[Event.rise.rawValue], offset: off)
        set = KnobWeatherObs.hhmm(t[Event.set.rawValue], offset: off)
        let rr = t[Event.rise.rawValue], st = t[Event.set.rawValue]
        let ge = t[Event.goldEnd.rawValue], gs = t[Event.goldStart.rawValue]
        fraction = frac(now)
        func value(_ secs: Int64, left: Bool) {
            (hours, count) = Self.round(secs, th)
            unit = hours ? (left ? "h left" : "h") : (left ? "min left" : "min")
        }
        func subAt(_ what: String, _ at: Int64, _ c: RGB) {
            sub = what + " " + KnobWeatherObs.hhmm(at, offset: off)
            subColor = c
        }
        if now < rr {
            phase = .before
            value(rr - now, left: false)
            subAt(Self.untilSunrise, rr, th.horizonColor)
        } else if now >= st {
            phase = .after
            value(st - rr, left: false)
            sub = Self.daylight
        } else {
            value(st - now, left: true)
            if now < ge {
                phase = .morning
                subAt(Self.goldenTo, ge, th.goldColor)
            } else if gs != 0 && now >= gs {
                phase = .evening
                subAt(Self.sunsetAt, st, th.horizonColor)
            } else {
                phase = .day
                if gs != 0 { subAt(Self.golden, gs, th.goldColor) } else { subAt(Self.sunsetAt, st, th.horizonColor) }
            }
        }
    }

    static func crossings(_ r: Int64, _ s: Int64, _ tw: WeatherState.Faces.Twilight?, span: Int64) -> [Int64] {
        var t = Array(repeating: Int64(0), count: Event.allCases.count)
        t[Event.rise.rawValue] = r
        t[Event.set.rawValue] = s
        let u = { (d: Date?) in d.map(KnobWeatherObs.unix) ?? 0 }
        var lo = r - span
        func keep(_ e: Event, _ v: Int64, _ hi: Int64) {
            guard v >= lo && v <= hi else { return }
            t[e.rawValue] = v
            lo = v
        }
        keep(.dawn, u(tw?.dawn), r)
        keep(.blueEnd, u(tw?.blueEnd), r)
        lo = r
        keep(.goldEnd, u(tw?.goldenEnd), s)
        keep(.goldStart, u(tw?.goldenStart), s)
        lo = s
        keep(.blueStart, u(tw?.blueStart), s + span)
        keep(.dusk, u(tw?.dusk), s + span)
        return t
    }

    public static func round(_ secs: Int64, _ th: KnobTheme.Weather.Sun) -> (hours: Bool, count: Int) {
        let s = min(secs, Int64(th.countMaxS))
        if s >= 3600 { return (true, Int(s / 1800)) }
        return (false, max(Int((s + 59) / 60), 1))
    }

    public var start: Int64 { t[...Event.rise.rawValue].first { $0 != 0 } ?? 0 }
    public var end: Int64 { t[Event.set.rawValue...].last { $0 != 0 } ?? 0 }

    public func frac(_ at: Int64) -> Double {
        let a = start, b = end
        guard b > a else { return 0 }
        return min(max(Double(at - a) / Double(b - a), 0), 1)
    }

    public var hero: Bool { have || polar == .day }

    public var valueText: String { valueText(t: nil) }

    func valueText(t: Double?) -> String {
        guard have && hasNow && unit != nil else { return "--" }
        let v = KnobArrive.count(count, t)
        return hours ? KnobCount.text(hours: true, count: v) : "\(v)"
    }

    public func valueColor(_ w: KnobTheme.Weather) -> RGB { have && hasNow && unit != nil ? w.sun.valueColor : w.stillColor }

    public func color(at time: Int64, _ th: KnobTheme.Weather.Sun) -> RGB {
        color(at: time, th, stops: colorStops(th))
    }

    func colorStops(_ th: KnobTheme.Weather.Sun) -> [(Int64, RGB)] {
        let x = t, b = Int64(th.blendS)
        var st: [(Int64, RGB)] = []
        func add(_ anchor: Int64, _ d: Int64, _ c: RGB) {
            guard anchor != 0 else { return }
            let at = anchor + d
            if let last = st.last, at <= last.0 { return }
            st.append((at, c))
        }
        func dayEdge(_ golden: Int64, _ noon: Int64) -> RGB {
            let span = abs(golden - noon)
            return KnobWeatherObs.ramp(span <= b ? 1 : Double(b) / Double(span), th.dayEdge, th.dayNoon)
        }
        let rise = x[Event.rise.rawValue], set = x[Event.set.rawValue]
        let noon = rise != 0 && set != 0 ? (rise + set) / 2 : 0
        add(x[Event.dawn.rawValue], 0, th.blueOut)
        add(x[Event.blueEnd.rawValue], 0, th.blueIn)
        add(rise, -300, th.mauve)
        add(rise, 360, th.duskPink)
        add(rise, b, th.horizonColor)
        add(x[Event.goldEnd.rawValue], -b, th.goldColor)
        add(x[Event.goldEnd.rawValue], b, dayEdge(x[Event.goldEnd.rawValue], noon))
        add(noon, 0, th.dayNoon)
        add(x[Event.goldStart.rawValue], -b, dayEdge(x[Event.goldStart.rawValue], noon))
        add(x[Event.goldStart.rawValue], b, th.goldColor)
        add(set, -b, th.horizonColor)
        add(set, -360, th.duskPink)
        add(set, 300, th.mauve)
        add(x[Event.blueStart.rawValue], 0, th.blueIn)
        add(x[Event.dusk.rawValue], 0, th.blueOut)
        return st
    }

    func color(at time: Int64, _ th: KnobTheme.Weather.Sun, stops st: [(Int64, RGB)]) -> RGB {
        guard let first = st.first else { return th.dayNoon }
        if time <= first.0 { return first.1 }
        for i in 1..<st.count where time <= st[i].0 {
            return KnobWeatherObs.ramp(Double(time - st[i - 1].0) / Double(st[i].0 - st[i - 1].0), st[i - 1].1, st[i].1)
        }
        return st[st.count - 1].1
    }

    public func ringColor(_ f: Double, _ w: KnobTheme.Weather) -> RGB {
        ringColor(f, w, stops: colorStops(w.sun))
    }

    func ringColor(_ f: Double, _ w: KnobTheme.Weather, stops: [(Int64, RGB)]) -> RGB {
        guard have else { return w.ring.trackColor }
        let a = start, b = end
        return color(at: a + Int64((Double(b - a) * f).rounded()), w.sun, stops: stops)
    }
}
