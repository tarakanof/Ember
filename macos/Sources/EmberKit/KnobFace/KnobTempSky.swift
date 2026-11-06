import Foundation

enum KnobTempSky {
    struct Puff: Equatable { var x, y, r: Float }

    struct Cloud: Equatable {
        var back: Bool
        var base: Float
        var puffs: [Puff]
    }

    struct Band: Equatable {
        var tone: Int
        var fadeFrom: Float
        var clouds: [Cloud]
    }

    static let face: Float = 233, rimR: Float = 195, heroFade = 0.82 * rimR, sunC: Float = 116.3, sunCore: Float = 52
    static let nightTone = 3, rayCount = 22
    static let deg = KnobFaceRender.nearestPi / 180

    private static func c(_ x: Float, _ y: Float, _ r: Float) -> Puff { Puff(x: x, y: y, r: r) }

    static let bands: [Band] = [
        Band(tone: 0, fadeFrom: 175, clouds: []),
        Band(tone: 0, fadeFrom: 175, clouds: [
            Cloud(back: false, base: 150, puffs: [c(302.2, 136.2, 18.4), c(341.3, 120.1, 29.9), c(385.0, 133.9, 25.3)])]),
        Band(tone: 0, fadeFrom: 175, clouds: [
            Cloud(back: false, base: 182, puffs: [c(78.5, 163.1, 29.7), c(129.8, 146.9, 35.1), c(175.7, 165.8, 21.6)]),
            Cloud(back: false, base: 160, puffs: [c(298.3, 143.8, 21.6), c(344.2, 124.9, 35.1), c(395.5, 141.1, 29.7)])]),
        Band(tone: 1, fadeFrom: 165, clouds: [
            Cloud(back: true, base: 142, puffs: [c(52.1, 121.0, 33.0), c(109.1, 103.0, 39.0), c(160.1, 124.0, 24.0)]),
            Cloud(back: true, base: 128, puffs: [c(307.9, 110.0, 24.0), c(358.9, 89.0, 39.0), c(415.9, 107.0, 33.0)]),
            Cloud(back: false, base: 190, puffs: [c(70.1, 169.0, 33.0), c(127.1, 151.0, 39.0), c(178.1, 172.0, 24.0)]),
            Cloud(back: false, base: 176, puffs: [c(293.9, 158.0, 24.0), c(344.9, 137.0, 39.0), c(401.9, 155.0, 33.0)])]),
        Band(tone: 2, fadeFrom: 160, clouds: [
            Cloud(back: true, base: 140, puffs: [c(20, 124, 28), c(66, 104, 32), c(118, 110, 30), c(160, 128, 20)]),
            Cloud(back: true, base: 128, puffs: [c(308, 114, 22), c(352, 98, 30), c(400, 92, 32), c(446, 112, 28)]),
            Cloud(back: false, base: 196, puffs: [c(24, 178, 30), c(74, 162, 34), c(128, 168, 30), c(178, 182, 24)]),
            Cloud(back: false, base: 184, puffs: [c(284, 170, 24), c(330, 156, 30), c(380, 150, 34), c(430, 166, 30)])]),
    ]

    static let tones: [(front: RGB, back: RGB, line: RGB)] = KnobCastFace.tones
        + [(RGB(r: 0x3E, g: 0x46, b: 0x52), RGB(r: 0x2E, g: 0x34, b: 0x3D), RGB(r: 0x56, g: 0x60, b: 0x6C))]

    static let stars: [Puff] = [c(214, 70, 1.4), c(262, 62, 1.2), c(305, 95, 1.8), c(352, 126, 1.3), c(392, 186, 1.5),
                                c(320, 168, 1.1), c(404, 254, 1.6), c(396, 300, 1.2), c(62, 262, 1.4), c(74, 312, 1.1)]
    static let starColor = RGB(r: 0xDD, g: 0xE6, b: 0xF0), starAlpha: Float = 0.35
    static let rayColor = RGB(r: 0xFF, g: 0xB5, b: 0x47), sunLight = RGB(r: 0xFF, g: 0xE0, b: 0x7A), sunEdge = RGB(r: 0xF0, g: 0x8A, b: 0x3A)
    static let sunAlpha = 0.88, haloR = 80.0
    static let moonGlow = RGB(r: 0xAF, g: 0xC4, b: 0xE0), moonLight = RGB(r: 0xE8, g: 0xEC, b: 0xF2), moonShade = RGB(r: 0xC9, g: 0xD2, b: 0xDC)

    struct Moon: Equatable {
        var cx, cy, r, glow, glowR, illum, rx, lightX, lightY, lightR: Float
        var waxing: Bool
    }

    static func moon(_ f: KnobTempFace) -> Moon {
        let at = heroCenter(f.hero), small = f.hero == .moonSmall
        let r: Float = small ? 34 : 50, il = Float(f.moonIllum) / 100, sgn: Float = f.moonPhase < 50 ? 1 : -1
        return Moon(cx: at.x, cy: at.y, r: r, glow: small ? 0.14 : 0.2, glowR: (r * 1.6).rounded(), illum: il, rx: abs(1 - 2 * il) * r,
                    lightX: at.x - sgn * r * 0.35, lightY: at.y - r * 0.2, lightR: r * 1.2, waxing: sgn > 0)
    }

    static func band(_ f: KnobTempFace) -> Band? {
        guard f.value != nil, let b = f.cloudBand, bands.indices.contains(b) else { return nil }
        return bands[b]
    }

    static func tone(_ f: KnobTempFace, _ b: Band) -> Int { f.night ? nightTone : b.tone }

    static func rim(_ fx: Float, _ fy: Float, _ from: Float) -> Float {
        let dx = fx - face, dy = fy - face
        let d = (dx * dx + dy * dy).squareRoot()
        return d <= from ? 1 : d >= rimR ? 0 : (rimR - d) / (rimR - from)
    }

    static func heroCenter(_ h: KnobTempFace.Hero) -> (x: Float, y: Float) {
        switch h {
        case .moon: (144.6, 144.6)
        case .moonSmall: (300, 72)
        case .none, .sun: (sunC, sunC)
        }
    }

    static let keepOut: [[Float]] = [[190, 88, 286, 202, 18], [76, 208, 390, 339, 24], [84, 356, 166, 410, 12],
                                     [299, 356, 382, 410, 12], [180, 422, 286, 452, 10]]

    static func rbox(_ px: Float, _ py: Float, _ b: [Float]) -> Float {
        let cx = (b[0] + b[2]) / 2, cy = (b[1] + b[3]) / 2
        let qx = abs(px - cx) - ((b[2] - b[0]) / 2 - b[4]), qy = abs(py - cy) - ((b[3] - b[1]) / 2 - b[4])
        let ox = qx > 0 ? qx : 0, oy = qy > 0 ? qy : 0, inner = qx > qy ? qx : qy
        return (ox * ox + oy * oy).squareRoot() + (inner < 0 ? inner : 0) - b[4]
    }

    static func cov(_ d: Float) -> Float { min(max(0.5 - d, 0), 1) }

    static let rayStart = sunCore + 14, rayStep = 360 / Float(rayCount)
    static let rayAngle: [Float] = (0..<rayCount).map { Float($0) * rayStep + 4 }
    static let rayLength: [Float] = {
        let out = atan2f(sunC - face, sunC - face) / deg + 360
        return rayAngle.map { a in abs(fmodf(a - out + 180 + 720, 360) - 180) < 50 ? sunCore + 40 : 330 }
    }()

    static func rayAlpha(_ fx: Float, _ fy: Float) -> Float {
        let m = rim(fx, fy, heroFade)
        guard m > 0 else { return 0 }
        let vx = fx - sunC, vy = fy - sunC
        let rho = (vx * vx + vy * vy).squareRoot()
        guard !(rho < rayStart - 3 || rho > 300) else { return 0 }
        let phi = atan2f(vy, vx) / deg
        var k = Int(((phi - 4) / rayStep + 0.5).rounded(.down))
        k = (k % rayCount + rayCount) % rayCount
        let da = (phi - rayAngle[k]) * deg
        let along = rho * cosf(da), perp = rho * abs(sinf(da))
        let d = along < rayStart ? hypotf(along - rayStart, perp) : along > rayLength[k] ? hypotf(along - rayLength[k], perp) : perp
        let c = cov(d - (k % 2 == 1 ? 1.5 : 1.75))
        guard c > 0 else { return 0 }
        let g = rho / 300
        let ga: Float = g <= 0.2 ? 0.55 : 0.55 * (1 - (g - 0.2) / 0.8)
        var block: Float = 0
        for b in keepOut { block = max(block, min(max(0.5 - rbox(fx, fy, b) / 24, 0), 1)) }
        return c * ga * m * (1 - block)
    }

    static func alpha8(_ a: Float) -> UInt8 { !(a > 0.002) ? 0 : a > 1 ? 255 : UInt8((a * 255).rounded()) }

    static let rayRange = Int((face - rimR).rounded(.down))..<Int((face + rimR).rounded(.up))

    static func shape(_ cl: Cloud) -> [SIMD2<Float>]? {
        let c = cl.puffs, n = c.count
        guard (1...4).contains(n), c.allSatisfy({ $0.r > 0 && $0.y - $0.r < cl.base }) else { return nil }
        func baseEnd(_ q: Puff, _ side: Float) -> Float? {
            let k = q.r * q.r - (q.y - cl.base) * (q.y - cl.base)
            return k > 0 ? q.x + side * k.squareRoot() : nil
        }
        func insideOther(_ a: Int, _ b: Int, _ x: Float, _ y: Float) -> Bool {
            c.indices.contains { $0 != a && $0 != b && hypotf(x - c[$0].x, y - c[$0].y) <= c[$0].r }
        }
        guard let x0 = baseEnd(c[0], -1), !insideOther(0, 0, x0, cl.base) else { return nil }
        var p = [SIMD2(x0, cl.base)]
        for i in 0..<(n - 1) {
            guard let q = upperCross(c[i], c[i + 1]), q.y < cl.base, q.x > p[p.count - 1].x, !insideOther(i, i + 1, q.x, q.y) else { return nil }
            p.append(q)
        }
        guard let x1 = baseEnd(c[n - 1], 1), x1 > p[p.count - 1].x, !insideOther(n - 1, n - 1, x1, cl.base) else { return nil }
        return p + [SIMD2(x1, cl.base)]
    }

    static func upperCross(_ a: Puff, _ b: Puff) -> SIMD2<Float>? {
        let dx = b.x - a.x, dy = b.y - a.y, d = hypotf(dx, dy)
        guard d < a.r + b.r, d > abs(a.r - b.r) else { return nil }
        let p = (a.r * a.r - b.r * b.r + d * d) / (2 * d), h2 = a.r * a.r - p * p, h = h2 > 0 ? h2.squareRoot() : 0
        let mx = a.x + p * dx / d, my = a.y + p * dy / d
        let y1 = my - h * dx / d, y2 = my + h * dx / d
        return SIMD2(y1 < y2 ? mx + h * dy / d : mx - h * dy / d, min(y1, y2))
    }
}

struct KnobTempDrift: Equatable {
    struct Offset: Equatable { var dx, dy: Int }

    static let swingX = 6.0, swingY = 1.0, minS: Float = 7, maxS: Float = 13, rampS = 2.0, fps = 15.0, backDepth: Float = 0.6
    static let seed: UInt32 = 0x454D_4252, firstStart: UInt32 = 1

    var period: [Float] = [], phaseX: [Float] = [], phaseY: [Float] = [], depth: [Float] = []

    init(back: [Bool], entropy: UInt32 = seed, starts: UInt32 = firstStart) {
        let n = min(back.count, 4)
        guard n > 0 else { return }
        var s = entropy ^ (starts &* 0x2545_F491)
        let pp = Self.perm(&s, n), px = Self.perm(&s, n)
        let slot = (Self.maxS - Self.minS) / Float(n)
        for i in 0..<n {
            period.append(Self.minS + slot * (Float(pp[i]) + 0.15 + 0.7 * Self.unit(&s)))
            phaseX.append((Float(px[i]) + 0.15 + 0.7 * Self.unit(&s)) / Float(n))
            phaseY.append(Self.unit(&s))
            depth.append(back[i] ? Self.backDepth : 1)
        }
    }

    init(_ band: KnobTempSky.Band) { self.init(back: band.clouds.map(\.back)) }

    static func next(_ s: inout UInt32) -> UInt32 {
        s &+= 0x9E37_79B9
        var z = s
        z = (z ^ (z >> 16)) &* 0x85EB_CA6B
        z = (z ^ (z >> 13)) &* 0xC2B2_AE35
        return z ^ (z >> 16)
    }

    static func unit(_ s: inout UInt32) -> Float { Float(next(&s) >> 8) / 16_777_216 }

    static func perm(_ s: inout UInt32, _ n: Int) -> [Int] {
        var p = Array(0..<n)
        for i in stride(from: n - 1, to: 0, by: -1) { p.swapAt(i, Int(next(&s) % UInt32(i + 1))) }
        return p
    }

    var count: Int { period.count }

    func path(_ i: Int, _ t: Double) -> Offset {
        guard period.indices.contains(i), t > 0, t.isFinite else { return Offset(dx: 0, dy: 0) }
        let ramp = t < Self.rampS ? t / Self.rampS : 1, p = fmod(t / Double(period[i]), 1.0)
        let k = Double(depth[i]) * ramp
        return Offset(dx: Int((k * Self.swingX * sin(2 * Double.pi * (p + Double(phaseX[i])))).rounded()),
                      dy: Int((k * Self.swingY * sin(2 * Double.pi * (p + Double(phaseY[i])))).rounded()))
    }

    static let window = 900

    var rest: [Offset] { Array(repeating: Offset(dx: 0, dy: 0), count: count) }

    func offsets(t: Double) -> [Offset] {
        guard count > 0, t >= 0, t.isFinite else { return rest }
        let last = Int((t * Self.fps).rounded(.down))
        return replay((last > Self.window ? anchor(from: last - Self.window, to: last) : nil) ?? (0, rest, 0), to: last)
    }

    private func targets(_ f: Int) -> [Offset] { (0..<count).map { path($0, Double(f) / Self.fps) } }

    private static func toward(_ a: Offset, _ b: Offset) -> Offset {
        Offset(dx: a.dx + (b.dx > a.dx ? 1 : 0) - (b.dx < a.dx ? 1 : 0), dy: a.dy + (b.dy > a.dy ? 1 : 0) - (b.dy < a.dy ? 1 : 0))
    }

    private func replay(_ s: (next: Int, at: [Offset], turn: Int), to last: Int) -> [Offset] {
        var at = s.at, turn = s.turn
        guard s.next <= last else { return at }
        for f in s.next...last {
            for k in 0..<count {
                let i = (turn + k) % count, to = path(i, Double(f) / Self.fps)
                guard to != at[i] else { continue }
                at[i] = Self.toward(at[i], to)
                turn = (i + 1) % count
                break
            }
        }
        return at
    }

    func anchor(from a: Int, to last: Int) -> (next: Int, at: [Offset], turn: Int)? {
        var behind = 2 * Int(Self.swingX) * count, prev = targets(a)
        for f in (a + 1)...last {
            let to = targets(f), moved = (0..<count).filter { to[$0] != prev[$0] }
            if behind == 0, moved.count == 1 {
                var at = prev
                at[moved[0]] = Self.toward(at[moved[0]], to[moved[0]])
                return (f + 1, at, (moved[0] + 1) % count)
            }
            let arrivals = moved.reduce(0) { $0 + max(abs(to[$1].dx - prev[$1].dx), abs(to[$1].dy - prev[$1].dy)) }
            behind = max(behind + arrivals - 1, 0)
            prev = to
        }
        return nil
    }
}
