import CoreGraphics
import Foundation

/// The knob's weather sky: sprite geometry, particles and timing for the
/// 200 × 140 sky canvas, ported from cinder's `weather_scene.c`.
/// Deterministic for a seed and a sequence of `step` calls.
public struct KnobWeatherScene: Sendable {
    public enum Sprite: Int, Sendable, CaseIterable {
        case rain, flakeS, flakeL, cloudL, cloudS, bolt, moon, star, sun, rays, fog
    }

    /// One sprite copy, back to front: top-left at (x, y) in sky pixels.
    public struct Draw: Sendable, Equatable {
        public var sprite: Sprite
        public var frame: Int
        public var x, y: Double
        /// nil fills the sprite's solid part (a cloud interior) in black.
        public var color: RGB?
        public var alpha: Double
    }

    /// One sprite frame: round-capped polylines of one width, plus a solid part.
    public struct Shape: Sendable {
        public var strokes: [[CGPoint]]
        public var halfWidth: Double
        /// Cloud interior: circles (cx, cy, r) and a slab, in sprite pixels.
        public var fillCircles: [(CGPoint, Double)] = []
        public var fillRect: CGRect?
    }

    static let rayFrames = 16
    static let width = 200.0, height = 140.0
    static let rainSlant = -4.0 / 14.0
    static let starsX = [28, 158, 44, 150, 178, 14], starsY = [26, 18, 98, 104, 60, 62]

    public private(set) var look: KnobWeatherLook?
    public private(set) var t = 0.0
    private var rng: UInt32
    private var particles: [Particle] = []
    private var stars: [(Int, Int)] = []
    private var twinkle = 0, twinkleStart = 0.0, nextTwinkle = 0.0
    private var nextBolt = 0.0, boltOn = -1.0, boltOff = -1.0, boltOn2 = -1.0, boltOff2 = -1.0
    private var boltX = 0

    struct Particle: Sendable {
        var x = 0.0, y = 0.0, vx = 0.0, vy = 0.0, phase = 0.0, freq = 0.0, amp = 0.0
        var sprite = Sprite.rain
    }

    public init(seed: UInt32 = 0x5745_4154) {
        rng = seed == 0 ? 0x9E37_79B9 : seed
    }

    /// Seconds between frames for a look; 0 for a still one.
    public static func period(_ l: KnobWeatherLook) -> Double {
        if l.still { return 0 }
        switch l.face {
        case .rain, .storm: return 1.0 / 12
        case .snow: return 1.0 / 10
        default: return 1.0 / 4
        }
    }

    /// Switches the face; particles and stars respawn only when it changed.
    public mutating func setLook(_ l: KnobWeatherLook) {
        guard l != look else { return }
        look = l
        particles = (0..<Self.dropCount(l)).map { _ in spawn(initial: true) }
        stars = (0..<Self.starsX.count).map { i in
            (Self.starsX[i] + Int(frand() * 8) - 4, Self.starsY[i] + Int(frand() * 8) - 4)
        }
        twinkle = 0; twinkleStart = t; nextTwinkle = t + 1
        boltOn = -1; boltOff = -1; boltOn2 = -1; boltOff2 = -1
        nextBolt = t + 2 + 3 * frand()
    }

    /// Advances the scene by `dt` seconds (capped at 0.5 like the firmware).
    public mutating func step(_ dt: Double) {
        guard let look else { return }
        let dt = min(max(dt, 0), 0.5)
        t += dt
        for i in particles.indices {
            particles[i].x += particles[i].vx * dt
            particles[i].y += particles[i].vy * dt
            if particles[i].y > Self.height - Self.size(particles[i].sprite).height {
                particles[i] = spawn(initial: false)
            }
        }
        if t >= nextTwinkle {
            twinkle = Int(frand() * Double(stars.count)) % max(stars.count, 1)
            twinkleStart = t
            nextTwinkle = t + 0.8 + 0.8 * frand()
        }
        if look.face == .storm && t >= nextBolt {
            boltOn = t; boltOff = t + 0.25
            if frand() < 0.5 { boltOn2 = t + 0.4; boltOff2 = t + 0.6 } else { boltOn2 = -1; boltOff2 = -1 }
            boltX = 60 + Int(frand() * 70)
            nextBolt = t + 4 + 6 * frand()
        }
    }

    /// True while the storm's bolt is lit.
    public var flash: Bool {
        guard let look, look.face == .storm, !look.still else { return false }
        return (t >= boltOn && t < boltOff) || (t >= boltOn2 && t < boltOff2)
    }

    // MARK: Spawning

    private mutating func next() -> UInt32 {
        var x = rng
        x ^= x << 13; x ^= x >> 17; x ^= x << 5
        rng = x
        return x
    }

    private mutating func frand() -> Double { Double(next() >> 8) / 16_777_216 }

    static func dropCount(_ l: KnobWeatherLook) -> Int {
        switch l.face {
        case .rain: l.intensity == .light ? 5 : l.intensity == .heavy ? 16 : 10
        case .storm: l.intensity == .heavy ? 14 : 10
        case .snow: l.intensity == .light ? 6 : l.intensity == .heavy ? 12 : 9
        default: 0
        }
    }

    private mutating func spawn(initial: Bool) -> Particle {
        var p = Particle()
        let dropTop = 50.0
        if look?.face == .snow {
            p.sprite = frand() < 0.4 ? .flakeL : .flakeS
            let z = Self.size(p.sprite)
            p.amp = 4 + 4 * frand()
            p.freq = 0.5 + 0.4 * frand()
            p.phase = 2 * .pi * frand()
            p.x = 40 + p.amp + frand() * (160 - 40 - 2 * p.amp - z.width)
            let ymax = Self.height - z.height
            p.y = initial ? dropTop + frand() * (ymax - dropTop) : dropTop - 2 + 4 * frand()
            p.vy = 18 + 14 * frand()
        } else {
            p.sprite = .rain
            p.vy = 150 * (0.9 + 0.2 * frand())
            p.vx = p.vy * Self.rainSlant
            p.x = 58 + frand() * (154 - 58)
            let ymax = Self.height - Self.size(.rain).height
            p.y = initial ? dropTop + frand() * (ymax - dropTop) : dropTop + 6 * frand()
            if initial { p.x -= (p.y - dropTop) * -Self.rainSlant }
        }
        return p
    }

    // MARK: Draw list

    /// The sprite copies for the current moment, back to front.
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
        func sun(_ cx: Double, _ cy: Double) {
            let step = l.still ? 0 : Int((t * 4).truncatingRemainder(dividingBy: Double(Self.rayFrames)))
            emit(.rays, step, cx - 30, cy - 30, c.rays, 255)
            emit(.sun, 0, cx - 18, cy - 18, c.sun, 255)
        }
        switch l.face {
        case .clearDay:
            sun(100, 70)
        case .clearNight:
            for (i, s) in stars.enumerated() {
                var a = 90.0
                if i == twinkle && !l.still {
                    let dur = nextTwinkle - twinkleStart, k = dur > 0 ? (t - twinkleStart) / dur : 0
                    a += (165 * sin(.pi * min(max(k, 0), 1))).rounded(.towardZero)
                }
                emit(.star, 0, Double(s.0), Double(s.1), c.star, a)
            }
            emit(.moon, 0, 80, 46, c.moon, 255)
        case .partlyCloudy:
            if l.night { emit(.moon, 0, 48, 30, c.moon, 255) } else { sun(70, 52) }
            cloud(.cloudL, 74 + drift(14, 40, 0), 62, c.cloud, 255)
        case .overcast:
            cloud(.cloudS, 22 + drift(10, 50, 1), 24, c.cloud, 150)
            cloud(.cloudL, 66 + drift(16, 36, 0), 52, c.cloud, 255)
        case .fog:
            let amp = [28.0, 22, 30, 18], per = [23.0, 31, 19, 27], ph = [0, 2.1, 4.0, 1.2]
            for i in 0..<4 {
                emit(.fog, 0, 36 + drift(amp[i], per[i], ph[i]), 30 + 22 * Double(i), l.rime ? c.rime : c.fog,
                     i % 2 == 1 ? 140 : 210)
            }
        case .rain, .storm, .snow:
            let storm = l.face == .storm, lit = flash
            if lit { emit(.bolt, 0, Double(boltX), 50, c.bolt, 255) }
            let crgb = l.face == .snow ? c.snowCloud : storm ? (lit ? c.litCloud : c.stormCloud) : c.rainCloud
            cloud(.cloudL, 45 + drift(6, 30, 0), 4, crgb, 255)
            let (rgb, a): (RGB, Double) = l.face == .snow
                ? (c.snow, 230) : (c.rain, l.intensity == .light ? 170 : l.intensity == .heavy ? 255 : 210)
            for p in particles {
                var x = p.x
                if p.amp > 0 { x += p.amp * sin(2 * .pi * (t * p.freq).truncatingRemainder(dividingBy: 1) + p.phase) }
                emit(p.sprite, 0, x, p.y, rgb, a)
            }
        }
        return out
    }

    // MARK: Sprites

    /// A sprite's size in sky pixels.
    public static func size(_ s: Sprite) -> CGSize {
        switch s {
        case .rain: CGSize(width: 8, height: 18)
        case .flakeS: CGSize(width: 6, height: 6)
        case .flakeL: CGSize(width: 14, height: 14)
        case .cloudL: CGSize(width: 110, height: 56)
        case .cloudS: CGSize(width: 68, height: 36)
        case .bolt: CGSize(width: 20, height: 40)
        case .moon: CGSize(width: 40, height: 40)
        case .star: CGSize(width: 10, height: 10)
        case .sun: CGSize(width: 36, height: 36)
        case .rays: CGSize(width: 60, height: 60)
        case .fog: CGSize(width: 128, height: 10)
        }
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

    /// The geometry of one sprite frame.
    public static func shape(_ s: Sprite, frame: Int) -> Shape {
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
