import Foundation

public struct KnobCastFace: Equatable, Sendable {
    public struct Arc: Equatable, Sendable {
        public var a, n, intensity: Int
    }

    public var have = false
    public var wet = false
    public var dry = false
    public var hasCount = false
    public var hours = false
    public var count = 0
    public var variant = 0
    public var snow = false
    public var nowDeg = 0.0
    public var toDeg = 0.0
    public var arcs: [Arc] = []
    public var label = ""
    public var labelDeg = 0.0
    public var sub = ""

    public init() {}

    public init(state: WeatherState?, now: Date, shown: KnobCastFace? = nil,
                theme: KnobTheme.Weather = KnobTheme.standard.weather, calendar: Calendar = .current) {
        self.init(KnobWeatherObs(state: state, now: now, theme: theme, calendar: calendar), shown: shown, theme.cast)
    }

    static func words(_ snow: Bool, _ v: Int, cap: Bool) -> String {
        let w = [[["light rain", "rain", "heavy rain"], ["light snow", "snow", "heavy snow"]],
                 [["Light rain", "Rain", "Heavy rain"], ["Light snow", "Snow", "Heavy snow"]]]
        return w[cap ? 1 : 0][snow ? 1 : 0][clampIn(v) - 1]
    }

    static func clampIn(_ v: Int) -> Int { min(max(v, 1), 3) }

    struct Scan {
        var now: Int64
        var cur, next: KnobWeatherObs.Spell?
        var wet: Bool
    }

    static func scan(_ o: KnobWeatherObs) -> Scan? {
        guard o.fresh else { return nil }
        var s = Scan(now: o.now, cur: nil, next: nil, wet: false)
        for p in o.spells {
            if p.start <= s.now && s.now < p.end { s.cur = p }
            else if p.start > s.now && (s.next == nil || p.start < s.next!.start) { s.next = p }
        }
        s.wet = (o.nowRain ?? 0) > 0 || s.cur != nil
        if s.wet, let u = o.rainUntil, u > 0, s.now >= u { s.wet = s.cur.map { $0.start >= u } ?? false }
        return s
    }

    static let noForecast = "No forecast", noRain = "No rain for 12 h"

    init(_ o: KnobWeatherObs, shown: KnobCastFace?, _ t: KnobTheme.Weather.Cast) {
        guard let s = Self.scan(o) else { return }
        guard o.hasRain, let off = o.offset else {
            sub = Self.noForecast
            return
        }
        have = true
        let span = Int64(t.spanS), step = Int64(t.stepS), mod = KnobWeatherObs.mod
        let q = s.now - mod(s.now, step)
        var end = s.now - mod(s.now, 3600) + span
        func dial(_ local: Int64) -> Double { Double(mod(local, span)) / 120 }
        nowDeg = dial(s.now + off)
        let spells = o.spells
        for p in spells where p.start >= end { end = q + step + span }
        for p in spells {
            let a = max(p.start, q), b = min(p.end, end)
            if p.end <= s.now || b - a < 60 { continue }
            let fl = (a + off - mod(a + off, 60)) / 60
            arcs.append(Arc(a: Int(mod(fl, 720)), n: Int((b - a) / 60), intensity: Self.clampIn(p.intensity)))
        }
        var at: Int64 = 0
        if s.wet {
            wet = true
            variant = Self.clampIn(s.cur?.intensity ?? ((o.nowRain ?? 0) > 0 ? o.nowRain! : 2))
            snow = s.cur.map(\.snow) ?? (o.look.face == .snow)
            if let shown, shown.have, shown.wet, shown.snow == snow, variant < shown.variant { variant = shown.variant }
            if let u = o.rainUntil, u > s.now { at = u }
        } else if let next = s.next {
            variant = Self.clampIn(next.intensity)
            snow = next.snow
            at = next.start
        } else {
            dry = true
            variant = 1
            hours = true
            count = t.spanS / 1800
            sub = Self.noRain
            return
        }
        guard at != 0 else {
            sub = Self.words(snow, variant, cap: true)
            return
        }
        hasCount = true
        (hours, count) = Self.round(at - s.now)
        let m = Int(mod(at + off, 86_400) / 60)
        label = m % 60 != 0 ? String(format: "%02d:%02d", m / 60, m % 60) : String(format: "%02d", m / 60)
        let now = Float(nowDeg), d = now + Float(at - s.now) / 120
        labelDeg = Double(d.truncatingRemainder(dividingBy: 360))
        if s.wet {
            toDeg = Double(d - 1.5 > now ? d - 1.5 : now)
        } else {
            let cs = 30 * (d / 30).rounded(.down) + 1.5
            toDeg = Double(max(d, cs) + 1.5)
        }
        sub = "#\(t.color.hex.dropFirst()) \(Self.words(snow, variant, cap: false))# \(s.wet ? "until" : "at") "
            + String(format: "%02d:%02d", m / 60, m % 60)
    }

    public static func round(_ secs: Int64) -> (hours: Bool, count: Int) {
        let s = max(secs, 0), m5 = (s + 150) / 300
        if m5 < 12 { return (false, max(Int(m5), 1)) }
        return (true, max(Int((s + 900) / 1800), 2))
    }

    public var valueText: String { valueText(t: nil) }

    func valueText(t: Double?) -> String {
        guard hasCount || dry else { return "--" }
        let k = t.map { KnobArrive.countStep(count, KnobArrive.quantised($0, fps: KnobArrive.fps)) } ?? count, v = min(max(k, 0), count)
        return dry && v == count ? "\(count / 2)+" : KnobCount.text(hours: hours, count: v)
    }

    public var unit: String? { hasCount || dry ? (hours ? "h" : "min") : nil }

    public func valueColor(_ w: KnobTheme.Weather) -> RGB {
        !hasCount && !dry ? w.stillColor : dry ? w.cast.dryColor : w.cast.color
    }

    public static func ringPoint(_ deg: Double, radius: Double = 222, center: Double = 233) -> CGPoint {
        let a = Float(deg) * KnobFaceRender.nearestPi / 180, r = Float(radius), c = Float(center)
        return CGPoint(x: Double(c + r * sin(a)), y: Double(c - r * cos(a)))
    }

    public static func labelAnchor(_ deg: Double) -> (CGPoint, align: Int) {
        let d = Float(deg), lc = (d.truncatingRemainder(dividingBy: 360) + 360).truncatingRemainder(dividingBy: 360)
        var r: Float = 188, align = 0
        if lc > 45 && lc < 135 { align = -1; r = 198 } else if lc > 225 && lc < 315 { align = 1; r = 198 }
        return (ringPoint(Double(lc), radius: Double(r)), align)
    }

    public static func labelPlace(_ deg: Double, w: Double, h: Double, avoid: CGRect, gap: Double) -> CGPoint? {
        let (p, align) = labelAnchor(deg)
        let ax = Float(p.x), ay = Float(p.y), fw = Float(w), fh = Float(h)
        let left = align < 0 ? ax - fw : align == 0 ? ax - fw / 2 : ax
        let x = Double(left.rounded()), y = Double((ay - fh / 2).rounded())
        let toLeft: Bool = x + w + gap <= avoid.minX, toRight: Bool = x >= avoid.maxX + gap
        let above: Bool = y + h + gap <= avoid.minY, below: Bool = y >= avoid.maxY + gap
        let clear = toLeft || toRight || above || below
        return clear ? CGPoint(x: x, y: y) : nil
    }

    public struct Segment: Equatable, Sendable {
        public var from, to: Double
        public var intensity: Int
    }

    public static func cells() -> [(Double, Double)] {
        (0..<12).map { (k: Int) -> (Double, Double) in
            let lo: Double = 30 * Double(k) + 1.5
            return (lo, lo + 27)
        }
    }

    public var segments: [Segment] {
        var out: [Segment] = []
        for (lo, hi) in Self.cells() {
            for arc in arcs {
                let a0 = Double(arc.a) * 0.5, a1 = a0 + Double(arc.n) * 0.5
                for w in [0.0, 360] {
                    let s0 = max(a0, lo + w), s1 = min(a1, hi + w)
                    if s1 - s0 > 0.2 { out.append(Segment(from: s0 - w, to: s1 - w, intensity: arc.intensity)) }
                }
            }
        }
        return out
    }
}
