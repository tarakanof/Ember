import CoreGraphics
import Foundation

/// The knob bot's geometry in body space (y up, body radius 1): a port of
/// cinder's `bot_shape.c`, which differs from `BotRenderer` in its curved
/// triangle and in drawing eyes as round-capped strokes.
public struct KnobBotShape: Sendable {
    /// A round-capped polyline; `width` is in body radii.
    public struct Stroke: Sendable, Equatable {
        public var points: [CGPoint]
        public var width: Double
    }

    let ringPoints: Int
    private let table: [Double]

    public init(theme: KnobTheme.Bot) {
        ringPoints = theme.ringPoints
        table = Self.triangleTable(theme.triangle)
    }

    /// The body outline for morph `k` (0 circle … 1 curved triangle).
    public func ring(_ k: Double) -> [CGPoint] {
        (0..<ringPoints).map { i in
            let a = Double(i) / Double(ringPoints) * 2 * .pi
            let r = 1 + (triangleRadius(a) - 1) * k
            return CGPoint(x: r * cos(a), y: r * sin(a))
        }
    }

    func triangleRadius(_ a: Double) -> Double {
        let n = Double(table.count)
        var x = (a / (2 * .pi) * n).truncatingRemainder(dividingBy: n)
        if x < 0 { x += n }
        let i = Int(x), f = x - Double(i)
        return table[i % table.count] * (1 - f) + table[(i + 1) % table.count] * f
    }

    /// Three sharp vertices at circumradius R, apex up, joined by arcs bowing
    /// out by the sagitta, sampled into a polar table and lightly blurred.
    static func triangleTable(_ t: KnobTheme.Bot.Triangle) -> [Double] {
        let bins = t.tableBins, R = t.radius, sag = t.sagitta
        var raw = [Double](repeating: 0, count: bins)
        let v = (0..<3).map { k -> (Double, Double) in
            let a = Double.pi / 2 + Double(k) * 2 * .pi / 3
            return (R * cos(a), R * sin(a))
        }
        for k in 0..<3 {
            let (ax, ay) = v[k], (bx, by) = v[(k + 1) % 3]
            let c = hypot(bx - ax, by - ay), rho = (c * c / 4 + sag * sag) / (2 * sag)
            let mx = (ax + bx) / 2, my = (ay + by) / 2, nl = hypot(mx, my), nx = mx / nl, ny = my / nl
            for i in 0...2000 {
                let u = Double(i) / 2000, x = (u - 0.5) * c
                let off = (rho * rho - x * x).squareRoot() - (rho - sag)
                let px = ax + (bx - ax) * u + nx * off, py = ay + (by - ay) * u + ny * off
                var ang = atan2(py, px)
                if ang < 0 { ang += 2 * .pi }
                let bin = Int(ang / (2 * .pi) * Double(bins)) % bins
                raw[bin] = max(raw[bin], hypot(px, py))
            }
        }
        for i in 0..<bins where raw[i] == 0 { raw[i] = raw[(i + bins - 1) % bins] }
        let sig = t.blurDeg / 360 * Double(bins)
        let w = Int((3 * sig).rounded(.up))
        return (0..<bins).map { i in
            var acc = 0.0, ws = 0.0
            for d in -w...w {
                let g = exp(-Double(d * d) / (2 * sig * sig))
                acc += g * raw[(i + d + bins) % bins]
                ws += g
            }
            return acc / ws
        }
    }

    /// Both eyes for a pose, left then right.
    public static func eyes(_ p: BotPose, scale: Double) -> [Stroke] {
        let reach = 0.6 - 0.2 * p.triangle
        var cx = p.gazeX * reach, cy = p.gazeY * reach - 0.12 * p.triangle - 0.1 * p.slump
        let limit = 0.52 - 0.15 * p.triangle, d = hypot(cx, cy)
        if d > limit { cx *= limit / d; cy *= limit / d }
        let fx = (1 - 0.45 * cx * cx).squareRoot(), fy = (1 - 0.45 * cy * cy).squareRoot()
        let straight = 0.06
        let lean = min(max((abs(cx) - straight) / (BotBehavior.rest.x * 0.6 - straight), 0), 1)
        let sep = (p.eyes == .round ? 0.5 : 0.44) * fx * (1 + (scale - 1) * 0.5)
        let deg = Double.pi / 180

        return [(-1.0, p.lidLeft), (1.0, p.lidRight)].map { side, lid in
            func lerp(_ a: Double, _ b: Double) -> Double { a + (b - a) * lid }
            let rise = p.eyes == .dash ? 0.04 * side * lean : 0
            let ex = cx + side * sep / 2, ey = cy + rise, ffx = fx * scale, ffy = fy * scale
            switch p.eyes {
            case .dash:
                return capsule(ex, ey, lerp(0.14, 0.24) * ffx, lerp(0.38, 0.06) * ffy, lerp(27, -6) * lean * deg)
            case .angry:
                return capsule(ex, ey, lerp(0.13, 0.22) * ffx, lerp(0.3, 0.06) * ffy, lerp(55, 10) * deg * -side)
            case .round:
                return capsule(ex, ey, lerp(0.3, 0.34) * ffx, lerp(0.44, 0.05) * ffy, 10 * lean * deg)
            case .happy:
                let w = 0.3 * ffx, h = lerp(0.16, 0.03) * ffy
                let x0 = ex - w / 2, y0 = ey - h / 2, qx = ex, qy = ey + h * 1.5, x1 = ex + w / 2
                let pts = (0..<7).map { i -> CGPoint in
                    let u = Double(i) / 6, v = 1 - u
                    return CGPoint(x: v * v * x0 + 2 * v * u * qx + u * u * x1,
                                   y: v * v * y0 + 2 * v * u * qy + u * u * y0)
                }
                return Stroke(points: pts, width: 0.11 * min(ffx, ffy))
            }
        }
    }

    /// A w × h capsule rotated by `angle` as a two-point round-capped stroke.
    static func capsule(_ cx: Double, _ cy: Double, _ w: Double, _ h: Double, _ angle: Double) -> Stroke {
        let half: Double, dx: Double, dy: Double, width: Double
        if h >= w { half = (h - w) / 2; dx = -sin(angle); dy = cos(angle); width = w }
        else { half = (w - h) / 2; dx = cos(angle); dy = sin(angle); width = h }
        let hl = max(half, 0.002)
        return Stroke(points: [CGPoint(x: cx - dx * hl, y: cy - dy * hl), CGPoint(x: cx + dx * hl, y: cy + dy * hl)],
                      width: width)
    }
}
