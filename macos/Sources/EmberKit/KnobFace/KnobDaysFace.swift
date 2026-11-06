import Foundation

public struct KnobDaysFace: Equatable, Sendable {
    public enum Icon: Int, Sendable, CaseIterable { case sun, partly, cloud, fog, rain, snow, storm }

    public struct Row: Equatable, Sendable {
        public var wday: Int
        public var icon: Icon
        public var pop: Int?
        public var lo, hi: Int
    }

    public var rows: [Row] = []
    public var amin = 0
    public var amax = 0

    public init(rows: [Row] = []) {
        self.rows = rows
        axis()
    }

    public init(state: WeatherState?, now: Date, theme: KnobTheme.Weather = KnobTheme.standard.weather,
                calendar: Calendar = .current) {
        let o = KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar)
        guard o.valid, state?.enabled == true, let f = o.faces else { return }
        var kept: [(t: Int64, end: Int64, face: KnobWeatherLook.Face, pop: Int?, lo: Int, hi: Int)] = []
        let days = f.days
        for (i, d) in days.enumerated() where kept.count < 3 {
            let t = KnobWeatherObs.unix(d.start)
            guard let lo = d.minC, let hi = d.maxC, (-99...99).contains(lo), (-99...99).contains(hi), lo <= hi,
                  kept.last.map({ t > $0.t }) ?? true else { continue }
            let face = KnobWeatherLook(provider: o.provider, condition: d.condition ?? "", code: d.conditionCode, night: false).face
            let next = i + 1 < days.count ? KnobWeatherObs.unix(days[i + 1].start) : nil
            kept.append((t, Self.dayEnd(t, KnobWeatherObs.unix(d.end), next: next), face, d.popPct, lo, hi))
        }
        let off = o.offset
        for d in kept {
            let end = d.end > d.t ? d.end : d.t + 86_400
            if o.now >= end { continue }
            let icon = Self.icon(d.face)
            let pop = Self.wet(icon) ? d.pop.flatMap { (0...100).contains($0) ? $0 : nil } : nil
            rows.append(Row(wday: Self.weekday(d.t, offset: off), icon: icon, pop: pop, lo: d.lo, hi: d.hi))
        }
        axis()
    }

    static func dayEnd(_ t: Int64, _ end: Int64, next: Int64?) -> Int64 {
        if end >= t + 79_200 && end <= t + 93_600 { return end }
        if let next, next > t, next <= t + 90_000 { return next }
        return 0
    }

    mutating func axis() {
        guard let first = rows.first else { return }
        amin = rows.map(\.lo).min() ?? first.lo
        amax = rows.map(\.hi).max() ?? first.hi
        if amin == amax { amin -= 1; amax += 1 }
    }

    public var have: Bool { !rows.isEmpty }

    static let names = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]

    public static func name(_ wday: Int) -> String { names[((wday % 7) + 7) % 7] }

    public static func weekday(_ t: Int64, offset: Int64?) -> Int {
        let s = t + 43_200 + (offset ?? 0)
        let day = s >= 0 ? s / 86_400 : -((-s + 86_399) / 86_400)
        return Int((((day + 4) % 7) + 7) % 7)
    }

    public static func icon(_ f: KnobWeatherLook.Face) -> Icon {
        switch f {
        case .clearDay, .clearNight: .sun
        case .partlyCloudy: .partly
        case .fog: .fog
        case .rain: .rain
        case .snow: .snow
        case .storm: .storm
        case .overcast: .cloud
        }
    }

    public static func wet(_ i: Icon) -> Bool { i == .rain || i == .snow || i == .storm }

    public static func rowY(_ n: Int, _ i: Int, _ t: KnobTheme.Weather.Days) -> Int { t.rowYPx + (2 * i - (n - 1)) * t.pitchPx / 2 }

    public func span(_ i: Int, barW: Int, _ t: KnobTheme.Weather.Days) -> (Double, Double) {
        let r = rows[i], k = Double(barW) / Double(amax - amin), h = Double(t.barHPx)
        var a = Double(r.lo - amin) * k, b = Double(r.hi - amin) * k
        if b - a < h {
            let c = (a + b) / 2
            a = c - h / 2; b = c + h / 2
            if a < 0 { b -= a; a = 0 }
            if b > Double(barW) { a -= b - Double(barW); b = Double(barW) }
        }
        return (a, b)
    }

    static let digitW = [24, 13, 21, 21, 24, 21, 22, 22, 23, 22], degreeW = 15, minusW = 21
    static let nameW = [59, 68, 55, 70, 58, 39, 49]

    public static func degWidth(_ v: Int) -> Int {
        var a = abs(v), w = degreeW + (v < 0 ? minusW : 0)
        repeat { w += digitW[a % 10]; a /= 10 } while a > 0
        return w
    }

    public static func nameWidth(_ wday: Int) -> Int { nameW[((wday % 7) + 7) % 7] }

    public struct Cols: Equatable, Sendable {
        public var x0, x1, dayX, dayW, iconX, loX, loW, barX, barW, hiX, hiW: Int
    }

    public func cols(_ t: KnobTheme.Weather.Days, face: Int = 466) -> Cols {
        var dw = 0, lw = 0, hw = 0
        for r in rows {
            dw = max(dw, Self.nameWidth(r.wday)); lw = max(lw, Self.degWidth(r.lo)); hw = max(hw, Self.degWidth(r.hi))
        }
        let day = max(t.dayWPx, dw), lo = max(t.tempWPx, lw), hi = max(t.tempWPx, hw)
        let bar = t.barWPx - (day - t.dayWPx) - (lo - t.tempWPx) - (hi - t.tempWPx)
        let total = day + t.iconPx + lo + bar + hi + 4 * t.gapPx
        let x0 = (face - total) / 2, iconX = x0 + day + t.gapPx, loX = iconX + t.iconPx + t.gapPx
        let barX = loX + lo + t.gapPx, hiX = barX + bar + t.gapPx
        return Cols(x0: x0, x1: hiX + hi, dayX: x0, dayW: day, iconX: iconX, loX: loX, loW: lo, barX: barX, barW: bar,
                    hiX: hiX, hiW: hi)
    }
}
