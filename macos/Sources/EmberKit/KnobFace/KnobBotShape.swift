import CoreGraphics
import Foundation

public struct KnobBotShape: Sendable {
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

    public static func rimVariants(steps: Int, squash q: Double) -> [(sx: Double, sy: Double)] {
        (0...(2 * steps)).map { k in
            let t = Double(k - steps) / Double(steps)
            return t < 0 ? (1 - 0.10 * q * t, 1 + 0.14 * q * t) : (1 - 0.07 * q * t, 1 + 0.10 * q * t)
        }
    }

    public static func rimVariant(for p: BotPose, variants: [(sx: Double, sy: Double)]) -> Int {
        let base = variants.count / 2
        let ratio = p.scaleY / p.scaleX
        var best = base, bestD = abs(ratio - 1)
        if bestD < 0.015 { return base }
        for (i, v) in variants.enumerated() where abs(ratio - v.sy / v.sx) < bestD {
            best = i; bestD = abs(ratio - v.sy / v.sx)
        }
        return best
    }

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

    public static func eyes(_ p: BotPose, scale: Double, geometry e: KnobTheme.Bot.Eyes) -> [Stroke] {
        let reach = e.reach + e.reachTriangle * p.triangle
        var cx = p.gazeX * reach, cy = p.gazeY * reach - e.dropTriangle * p.triangle - e.dropSlump * p.slump
        let limit = e.limit + e.limitTriangle * p.triangle, d = hypot(cx, cy)
        if d > limit { cx *= limit / d; cy *= limit / d }
        let fx = (1 - e.foreshorten * cx * cx).squareRoot(), fy = (1 - e.foreshorten * cy * cy).squareRoot()
        let lean = min(max((abs(cx) - e.straight) / (e.restX * e.leanAt - e.straight), 0), 1)
        let sep = (p.eyes == .round ? e.sepRound : e.sep) * fx * (1 + (scale - 1) * 0.5)
        let deg = Double.pi / 180

        return [(-1.0, p.lidLeft), (1.0, p.lidRight)].map { side, lid in
            func lerp(_ v: [Double]) -> Double { v[0] + (v[1] - v[0]) * lid }
            let rise = p.eyes == .dash ? e.rise * side * lean : 0
            let ex = cx + side * sep / 2, ey = cy + rise, ffx = fx * scale, ffy = fy * scale
            func capsule(_ c: KnobTheme.Bot.Eyes.Capsule, _ angle: Double) -> Stroke {
                Self.capsule(ex, ey, lerp(c.width) * ffx, lerp(c.height) * ffy, angle, minHalf: e.minHalf)
            }
            switch p.eyes {
            case .dash: return capsule(e.dash, lerp(e.dash.angleDeg) * lean * deg)
            case .angry: return capsule(e.angry, lerp(e.angry.angleDeg) * deg * -side)
            case .round: return capsule(e.round, lerp(e.round.angleDeg) * lean * deg)
            case .happy:
                let w = e.happy.width * ffx, h = lerp(e.happy.height) * ffy
                let x0 = ex - w / 2, y0 = ey - h / 2, qx = ex, qy = ey + h * e.happy.lift, x1 = ex + w / 2
                let n = max(e.happy.points, 2)
                let pts = (0..<n).map { i -> CGPoint in
                    let u = Double(i) / Double(n - 1), v = 1 - u
                    return CGPoint(x: v * v * x0 + 2 * v * u * qx + u * u * x1,
                                   y: v * v * y0 + 2 * v * u * qy + u * u * y0)
                }
                return Stroke(points: pts, width: e.happy.stroke * min(ffx, ffy))
            }
        }
    }

    static func capsule(_ cx: Double, _ cy: Double, _ w: Double, _ h: Double, _ angle: Double,
                        minHalf: Double) -> Stroke {
        let half: Double, dx: Double, dy: Double, width: Double
        if h >= w { half = (h - w) / 2; dx = -sin(angle); dy = cos(angle); width = w }
        else { half = (w - h) / 2; dx = cos(angle); dy = sin(angle); width = h }
        let hl = max(half, minHalf)
        return Stroke(points: [CGPoint(x: cx - dx * hl, y: cy - dy * hl), CGPoint(x: cx + dx * hl, y: cy + dy * hl)],
                      width: width)
    }
}
