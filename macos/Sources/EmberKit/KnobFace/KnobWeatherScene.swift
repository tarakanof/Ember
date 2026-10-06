import CoreGraphics
import Foundation

public struct KnobWeatherScene: Sendable {
    public enum Sprite: String, Sendable, CaseIterable {
        case rain, flakeS = "flake_s", flakeL = "flake_l", cloudL = "cloud_l", cloudS = "cloud_s"
        case bolt, moon, star, sun, rays, fog
    }

    public struct Draw: Sendable, Equatable {
        public var sprite: Sprite
        public var frame: Int
        public var x, y: Double
        public var color: RGB?
        public var alpha: Double
    }

    public struct Shape: Sendable {
        public var strokes: [[CGPoint]]
        public var halfWidth: Double
        public var fillCircles: [(CGPoint, Double)] = []
        public var fillRect: CGRect?
    }

    static var rainSlant: Double {
        let s = KnobTheme.standard.weather.scene.rain.slant
        return s[0] / s[1]
    }

    let sc: KnobTheme.Weather.Scene
    let width: Double, height: Double

    public private(set) var look: KnobWeatherLook?
    public private(set) var t = 0.0
    private var rng: UInt32
    private var particles: [Particle] = []
    private var stars: [(Double, Double)] = []
    private var twinkle = 0, twinkleStart = 0.0, nextTwinkle = 0.0
    private var nextBolt = 0.0, boltOn = -1.0, boltOff = -1.0, boltOn2 = -1.0, boltOff2 = -1.0
    private var boltX = 0

    struct Particle: Sendable {
        var x = 0.0, y = 0.0, vx = 0.0, vy = 0.0, phase = 0.0, freq = 0.0, amp = 0.0
        var sprite = Sprite.rain
    }

    public init(seed: UInt32 = 0x5745_4154, theme: KnobTheme.Weather = KnobTheme.standard.weather) {
        rng = seed == 0 ? 0x9E37_79B9 : seed
        sc = theme.scene
        width = theme.sky.widthPx; height = theme.sky.heightPx
    }

    public static func period(_ l: KnobWeatherLook, scene: KnobTheme.Weather.Scene = KnobTheme.standard.weather.scene) -> Double {
        if l.still { return 0 }
        switch l.face {
        case .rain, .storm: return 1 / scene.fps.rain
        case .snow: return 1 / scene.fps.snow
        default: return 1 / scene.fps.other
        }
    }

    public func size(_ s: Sprite) -> CGSize {
        let v = sc.sprites[s.rawValue] ?? [0, 0]
        return CGSize(width: v[0], height: v[1])
    }

    private mutating func range(_ v: [Double]) -> Double { v[0] + frand() * v[1] }

    public mutating func setLook(_ l: KnobWeatherLook) {
        guard l != look else { return }
        look = l
        particles = (0..<dropCount(l)).map { _ in spawn(initial: true) }
        let st = sc.stars, j = st.jitter
        stars = st.x.indices.map { i in
            (st.x[i] + Double(Int(frand() * j)) - (j / 2).rounded(.down),
             st.y[i] + Double(Int(frand() * j)) - (j / 2).rounded(.down))
        }
        twinkle = 0; twinkleStart = t; nextTwinkle = t + st.firstTwinkleS
        boltOn = -1; boltOff = -1; boltOn2 = -1; boltOff2 = -1
        nextBolt = t + sc.bolt.firstS[0] + sc.bolt.firstS[1] * frand()
    }

    public mutating func step(_ dt: Double) {
        guard let look else { return }
        let dt = min(max(dt, 0), sc.maxStepS)
        t += dt
        for i in particles.indices {
            particles[i].x += particles[i].vx * dt
            particles[i].y += particles[i].vy * dt
            if particles[i].y > height - size(particles[i].sprite).height {
                particles[i] = spawn(initial: false)
            }
        }
        if t >= nextTwinkle {
            twinkle = Int(frand() * Double(stars.count)) % max(stars.count, 1)
            twinkleStart = t
            nextTwinkle = t + sc.stars.twinkleS[0] + sc.stars.twinkleS[1] * frand()
        }
        let bo = sc.bolt
        if look.face == .storm && t >= nextBolt {
            boltOn = t; boltOff = t + bo.onS
            if frand() < bo.doubleChance { boltOn2 = t + bo.secondS[0]; boltOff2 = t + bo.secondS[1] }
            else { boltOn2 = -1; boltOff2 = -1 }
            boltX = Int(bo.x[0]) + Int(frand() * bo.x[1])
            nextBolt = t + bo.gapS[0] + bo.gapS[1] * frand()
        }
    }

    public var flash: Bool {
        guard let look, look.face == .storm, !look.still else { return false }
        return (t >= boltOn && t < boltOff) || (t >= boltOn2 && t < boltOff2)
    }

    private mutating func next() -> UInt32 {
        var x = rng
        x ^= x << 13; x ^= x >> 17; x ^= x << 5
        rng = x
        return x
    }

    private mutating func frand() -> Double { Double(next() >> 8) / 16_777_216 }

    static func level(_ i: KnobWeatherLook.Intensity) -> Int {
        switch i {
        case .light: 0
        case .heavy: 2
        case .none, .moderate: 1
        }
    }

    func dropCount(_ l: KnobWeatherLook) -> Int {
        switch l.face {
        case .rain: sc.drops.rain[Self.level(l.intensity)]
        case .storm: sc.drops.storm[Self.level(l.intensity)]
        case .snow: sc.drops.snow[Self.level(l.intensity)]
        default: 0
        }
    }

    private mutating func spawn(initial: Bool) -> Particle {
        var p = Particle()
        let rn = sc.rain, sn = sc.snow
        let dropTop = rn.top
        if look?.face == .snow {
            p.sprite = frand() < sn.largeShare ? .flakeL : .flakeS
            let z = size(p.sprite)
            p.amp = range(sn.amp)
            p.freq = range(sn.freq)
            p.phase = 2 * .pi * frand()
            p.x = sn.x[0] + p.amp + frand() * (sn.x[1] - sn.x[0] - 2 * p.amp - z.width)
            let ymax = height - z.height
            p.y = initial ? dropTop + frand() * (ymax - dropTop) : dropTop - 2 + 4 * frand()
            p.vy = range(sn.speed)
        } else {
            let slant = rn.slant[0] / rn.slant[1]
            p.sprite = .rain
            p.vy = rn.speed * (1 - rn.speedSpread / 2 + rn.speedSpread * frand())
            p.vx = p.vy * slant
            p.x = rn.x[0] + frand() * (rn.x[1] - rn.x[0])
            let ymax = height - size(.rain).height
            p.y = initial ? dropTop + frand() * (ymax - dropTop) : dropTop + rn.respawnBand * frand()
            if initial { p.x -= (p.y - dropTop) * -slant }
        }
        return p
    }

    public func draws(colors c: KnobTheme.Weather.Colors) -> [Draw] {
        guard let l = look else { return [] }
        var out: [Draw] = []
        func emit(_ s: Sprite, _ f: Int, _ x: Double, _ y: Double, _ rgb: RGB?, _ a: Double) {
            out.append(Draw(sprite: s, frame: f, x: (x + 0.5).rounded(.down), y: (y + 0.5).rounded(.down),
                            color: l.still && rgb != nil ? c.still : rgb, alpha: min(max(a, 0), 255) / 255))
        }
        func cloud(_ s: Sprite, _ x: Double, _ y: Double, _ rgb: RGB, _ a: Double) {
            emit(s, 1, x, y, nil, 255)
            emit(s, 0, x, y, rgb, a)
        }
        func drift(_ amp: Double, _ period: Double, _ phase: Double) -> Double {
            amp * sin(2 * .pi * (t / period).truncatingRemainder(dividingBy: 1) + phase)
        }
        func drifting(_ s: Sprite, _ d: KnobTheme.Weather.Scene.Drift, _ rgb: RGB) {
            cloud(s, d.x + drift(d.amp, d.periodS, d.phase), d.y, rgb, d.alpha)
        }
        func sun(_ at: [Double]) {
            let rays = size(.rays), disc = size(.sun)
            let step = l.still ? 0 : Int((t * sc.rayStepsPerS).truncatingRemainder(dividingBy: Double(sc.rayFrames)))
            emit(.rays, step, at[0] - rays.width / 2, at[1] - rays.height / 2, c.rays, 255)
            emit(.sun, 0, at[0] - disc.width / 2, at[1] - disc.height / 2, c.sun, 255)
        }
        let ly = sc.layout
        switch l.face {
        case .clearDay:
            sun(ly.clearSun)
        case .clearNight:
            for (i, s) in stars.enumerated() {
                var a = sc.stars.alpha
                if i == twinkle && !l.still {
                    let dur = nextTwinkle - twinkleStart, k = dur > 0 ? (t - twinkleStart) / dur : 0
                    a += (sc.stars.twinkleAlpha * sin(.pi * min(max(k, 0), 1))).rounded(.towardZero)
                }
                emit(.star, 0, s.0, s.1, c.star, a)
            }
            emit(.moon, 0, ly.nightMoon[0], ly.nightMoon[1], c.moon, 255)
        case .partlyCloudy:
            if l.night { emit(.moon, 0, ly.partlyMoon[0], ly.partlyMoon[1], c.moon, 255) } else { sun(ly.partlySun) }
            drifting(.cloudL, ly.partlyCloud, c.cloud)
        case .overcast:
            drifting(.cloudS, ly.overcastBack, c.cloud)
            drifting(.cloudL, ly.overcastFront, c.cloud)
        case .fog:
            let f = ly.fog
            for i in f.amp.indices {
                emit(.fog, 0, f.x + drift(f.amp[i], f.periodS[i], f.phase[i]), f.y + f.step * Double(i),
                     l.rime ? c.rime : c.fog, f.alpha[i % f.alpha.count])
            }
        case .rain, .storm, .snow:
            let storm = l.face == .storm, lit = flash
            if lit { emit(.bolt, 0, Double(boltX), sc.bolt.y, c.bolt, 255) }
            let crgb = l.face == .snow ? c.snowCloud : storm ? (lit ? c.litCloud : c.stormCloud) : c.rainCloud
            drifting(.cloudL, ly.precipCloud, crgb)
            let (rgb, a): (RGB, Double) = l.face == .snow
                ? (c.snow, sc.dropAlpha.snow) : (c.rain, sc.dropAlpha.rain[Self.level(l.intensity)])
            for p in particles {
                var x = p.x
                if p.amp > 0 { x += p.amp * sin(2 * .pi * (t * p.freq).truncatingRemainder(dividingBy: 1) + p.phase) }
                emit(p.sprite, 0, x, p.y, rgb, a)
            }
        }
        return out
    }

    static let cloudCircles: [(CGPoint, Double)] = [
        (CGPoint(x: 30, y: 34), 16), (CGPoint(x: 58, y: 25), 21), (CGPoint(x: 86, y: 35), 15),
    ]
    static let cloudSlab = CGRect(x: 30, y: 30, width: 56, height: 20)

    static func cloudInside(_ x: Double, _ y: Double) -> Bool {
        for (c, r) in cloudCircles where (x - c.x) * (x - c.x) + (y - c.y) * (y - c.y) <= r * r { return true }
        return cloudSlab.minX <= x && x <= cloudSlab.maxX && cloudSlab.minY <= y && y <= cloudSlab.maxY
    }

    static let cloudOutline: [CGPoint] = (0...72).map { k in
        let a = 2 * Double.pi * Double(k % 72) / 72, ca = cos(a), sa = sin(a)
        var r = 0.0
        while r < 80 && cloudInside(58 + (r + 0.25) * ca, 36 + (r + 0.25) * sa) { r += 0.25 }
        return CGPoint(x: 58 + r * ca, y: 36 + r * sa)
    }

    static func arc(_ cx: Double, _ cy: Double, _ r: Double, _ a0: Double, _ sweep: Double, _ n: Int) -> [CGPoint] {
        (0..<n).map { k in
            let a = a0 + sweep * Double(k) / Double(n - 1)
            return CGPoint(x: cx + r * cos(a), y: cy + r * sin(a))
        }
    }

    static let moonOutline: [CGPoint] = {
        let c1x = 20.0, c1y = 20.0, R = 15.0, c2x = 28.0, c2y = 14.0, r = 13.0
        let dx = c2x - c1x, dy = c2y - c1y, d = hypot(dx, dy)
        let a = (R * R - r * r + d * d) / (2 * d), h = (R * R - a * a).squareRoot()
        let mx = c1x + a * dx / d, my = c1y + a * dy / d
        let px = mx + h * dy / d, py = my - h * dx / d, qx = mx - h * dy / d, qy = my + h * dx / d
        let tau = 2 * Double.pi
        func ccw(_ from: Double, _ to: Double) -> Double {
            let v = (to - from).truncatingRemainder(dividingBy: tau)
            return v < 0 ? v + tau : v
        }
        let p1 = atan2(py - c1y, px - c1x), q1 = atan2(qy - c1y, qx - c1x)
        var sw1 = ccw(p1, q1)
        if ccw(p1, atan2(dy, dx)) <= sw1 { sw1 -= tau }
        let q2 = atan2(qy - c2y, qx - c2x), p2 = atan2(py - c2y, px - c2x)
        var sw2 = ccw(q2, p2)
        if !(ccw(q2, atan2(-dy, -dx)) <= sw2) { sw2 -= tau }
        let pts = arc(c1x, c1y, R, p1, sw1, 40) + arc(c2x, c2y, r, q2, sw2, 32)
        return pts + [pts[0]]
    }()

    public static func shape(_ s: Sprite, frame: Int, rayFrames: Int = 16) -> Shape {
        func line(_ x0: Double, _ y0: Double, _ x1: Double, _ y1: Double) -> [CGPoint] {
            [CGPoint(x: x0, y: y0), CGPoint(x: x1, y: y1)]
        }
        switch s {
        case .rain:
            return Shape(strokes: [line(6, 2, 6 + 14 * rainSlant, 16)], halfWidth: 0.9)
        case .flakeS:
            return Shape(strokes: [line(3, 3, 3, 3)], halfWidth: 1.6)
        case .flakeL:
            return Shape(strokes: (0..<3).map { i in
                let a = Double.pi / 2 + Double(i) * .pi / 3, c = cos(a) * 5.2, s = sin(a) * 5.2
                return line(7 - c, 7 - s, 7 + c, 7 + s)
            }, halfWidth: 0.8)
        case .cloudL, .cloudS:
            let sc = s == .cloudS ? 0.6 : 1.0, oy = s == .cloudS ? 2.0 : 0.0
            if frame == 0 {
                return Shape(strokes: [cloudOutline.map { CGPoint(x: sc * $0.x, y: oy + sc * $0.y) }],
                             halfWidth: s == .cloudS ? 1.2 : 1.5)
            }
            return Shape(strokes: [], halfWidth: 0,
                         fillCircles: cloudCircles.map { (CGPoint(x: sc * $0.0.x, y: oy + sc * $0.0.y), sc * $0.1) },
                         fillRect: CGRect(x: sc * cloudSlab.minX, y: oy + sc * cloudSlab.minY,
                                          width: sc * cloudSlab.width, height: sc * cloudSlab.height))
        case .bolt:
            return Shape(strokes: [[CGPoint(x: 13, y: 3), CGPoint(x: 5, y: 20), CGPoint(x: 11, y: 20),
                                    CGPoint(x: 5, y: 37)]], halfWidth: 1.4)
        case .moon:
            return Shape(strokes: [moonOutline], halfWidth: 1.4)
        case .star:
            return Shape(strokes: [line(5, 1.3, 5, 8.7), line(1.3, 5, 8.7, 5)], halfWidth: 0.7)
        case .sun:
            return Shape(strokes: [arc(18, 18, 13, 0, 2 * .pi, 49)], halfWidth: 1.5)
        case .rays:
            let base = Double(frame % rayFrames) * (.pi / 4) / Double(rayFrames)
            return Shape(strokes: (0..<8).map { i in
                let a = base + Double(i) * .pi / 4, c = cos(a), s = sin(a)
                return line(30 + 21 * c, 30 + 21 * s, 30 + 27 * c, 30 + 27 * s)
            }, halfWidth: 1.4)
        case .fog:
            return Shape(strokes: [(0..<24).map { k in
                let x = 5 + 118 * Double(k) / 23
                return CGPoint(x: x, y: 5 + 1.6 * sin(2 * .pi * x / 40))
            }], halfWidth: 1.4)
        }
    }
}
