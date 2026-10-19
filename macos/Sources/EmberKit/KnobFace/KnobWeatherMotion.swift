import Foundation

public enum KnobWeatherMotion: Equatable, Sendable {
    case still, loop
    case arrival(until: Double)

    public static let fps = 15.0

    public init(_ face: KnobFace, reduceMotion: Bool = false) {
        guard !reduceMotion else { self = .still; return }
        switch face {
        case .rain(let f): self = f.dry ? .still : .loop
        case .cast(let f): self = (1...3).contains(f.variant) ? .loop : .still
        case .wind(let f): self = f.have ? .loop : .still
        case .air(let f): self = f.wilted ? .loop : .still
        case .sun(let f): self = f.hero ? .arrival(until: 22 / KnobSunFace.fps) : .still
        case .days(let f): self = f.have ? .arrival(until: 40 / KnobArrive.fps) : .still
        case .bot, .pomodoro, .nowPlaying, .weather: self = .still
        }
    }
}

enum KnobArrive {
    static let seconds = 1.32, fps = 30.0, fadeFps = 15.0, countMax = 30

    static func ease(_ t: Double) -> Double {
        guard t > 0 else { return 0 }
        if t >= seconds { return 1 }
        let p = 1 - t / seconds
        return 1 - p * p * p
    }

    static func quantised(_ t: Double, fps: Double) -> Double { (t * fps).rounded(.down) / fps }

    static func frame(_ t: Double, fps: Double = KnobWeatherMotion.fps) -> Int { Int((t * fps).rounded(.down)) }

    static func spriteOpacity(_ t: Double?) -> Int {
        guard let t else { return 255 }
        return Int((255 * ease(quantised(t, fps: fadeFps))).rounded())
    }

    static func countN(_ v: Int) -> Int { abs(v) <= countMax ? abs(v) : 23 }

    static func countValue(_ v: Int, _ k: Int) -> Int {
        let n = countN(v)
        if k <= 0 || n == 0 { return 0 }
        if k >= n { return v }
        if n == abs(v) { return v < 0 ? -k : k }
        return Int((Double(k) * Double(v) / Double(n)).rounded())
    }

    static func countStep(_ n: Int, _ t: Double) -> Int {
        guard t > 0 else { return 0 }
        if t >= seconds { return n }
        let p = 1 - t / seconds
        return min(Int((Double(n) * (1 - p * p * p) + 1e-9).rounded(.down)), n)
    }
}

extension KnobRainFace {
    struct Frame: Equatable {
        struct Drip: Equatable { var pose, dy, opa: Int }
        struct Splash: Equatable { var dy, opa: Int }
        var drips: [Drip?]
        var splashes: [Splash?]
        var bolt = false
    }

    static let fps = 15, stormFrames = 90, flash = [12, 13, 18, 19, 24, 25], splashDrop = [0, 1, 6]
    static let drips: [(period: Int, start: Int)] = [(24, 5), (32, 13)]
    static let dripSeq: [Frame.Drip] = [.init(pose: 0, dy: 0, opa: 153), .init(pose: 1, dy: 0, opa: 230), .init(pose: 2, dy: 1, opa: 255),
                                        .init(pose: 2, dy: 6, opa: 255), .init(pose: 2, dy: 14, opa: 230), .init(pose: 2, dy: 24, opa: 166),
                                        .init(pose: 2, dy: 34, opa: 89)]
    static let splashSeq: [Frame.Splash] = [.init(dy: 1, opa: 179), .init(dy: 0, opa: 255), .init(dy: -2, opa: 153)]
    static let dripX: [Double] = [107, 359], dripY = 196.0, dripScale = [0.45, 0.75, 1.0]
    static let boltAt = CGPoint(x: 196, y: 54), boltColor = RGB(r: 0xF0, g: 0xD0, b: 0x40)
    static let bolt: [CGPoint] = [(14, 0), (36, 0), (24, 22), (38, 22), (6, 52), (14, 30), (0, 30)].map { CGPoint(x: $0.0, y: $0.1) }

    static func canopyY(_ x: Float) -> Float {
        let u = (x - 233) / 128, k = 1 - u * u
        return 198 - 103 * (k > 0 ? k : 0).squareRoot()
    }

    static func splashSource(_ n: Int) -> Drop { sheets[2].drops[splashDrop[n]] }

    static func splashAt(_ n: Int) -> (x: Int, y: Int) {
        let d = splashSource(n)
        return (Int(d.x) - 8, Int((canopyY(Float(d.x)) - 3).rounded()) - 9)
    }

    static func splashHit(_ n: Int) -> Int {
        let s = sheets[2], d = splashSource(n), period = Float(2 * sheetH), frames = 2 * s.tileFrames
        let ty = Float(d.y) + Float(d.tile * sheetH)
        var t = fmodf(canopyY(Float(d.x)) - 2 - Float(s.box.minY) - ty - Float(d.len), period)
        if t < 0 { t += period }
        return (0..<frames).first { Float(Int((Double(period) * Double($0) / Double(frames)).rounded()) % Int(period)) >= t } ?? 0
    }

    func frame(k raw: Int) -> Frame {
        let k = max(raw, 0), snow = kind == .snow, top = Self.sheets[2]
        let drips = Self.drips.map { d -> Frame.Drip? in
            let j = k % d.period - d.start
            return snow || j < 0 || j >= Self.dripSeq.count ? nil : Self.dripSeq[j]
        }
        let splashes = Self.splashDrop.indices.map { n -> Frame.Splash? in
            guard !snow, Self.splashSource(n).level <= intensity else { return nil }
            let frames = 2 * top.tileFrames, a = (k + top.delayFrames) % frames
            let j = ((a - Self.splashHit(n)) % frames + frames) % frames
            return j < Self.splashSeq.count ? Self.splashSeq[j] : nil
        }
        return Frame(drips: drips, splashes: splashes, bolt: kind == .storm && Self.flash.contains(k % Self.stormFrames))
    }
}

extension KnobCastFace {
    struct Frame: Equatable {
        var k: Int
        var dx: [Int]
        var gloom, gate, label, sprite: Int
    }

    static let slideS = 0.96, gateS = 0.96, gateEndS = 1.14, labelEndS = 1.32, gloomSteps = 4

    static func ramp(_ t: Double, _ t0: Double, _ t1: Double) -> Int {
        let f = t <= t0 ? 0 : t >= t1 ? 1 : (t - t0) / (t1 - t0)
        return Int((255 * f).rounded())
    }

    static func gateOpacity(_ t: Double) -> Int { ramp(t, gateS, gateEndS) }

    static func labelOpacity(_ t: Double) -> Int { ramp(t, slideS, labelEndS) }

    static func gloom(variant: Int, k: Int) -> Int {
        guard variant == 3 else { return 255 }
        let f = triSteps(Double(max(k, 0)) / Double(fps), period: 3.2, delay: 0, n: gloomSteps)
        return Int((255 * (1 - 0.3 * f)).rounded())
    }

    func frame(t: Double?) -> Frame {
        let k = t.map { KnobArrive.frame($0) } ?? 0, fade = t.map { _ in Double(k) / Double(Self.fps) }
        let sheets = (1...3).contains(variant) ? Self.sheets[variant - 1] : []
        return Frame(k: k, dx: sheets.indices.map { Self.breathe(variant: variant, side: $0, k: k) },
                     gloom: Self.gloom(variant: variant, k: k), gate: fade.map(Self.gateOpacity) ?? 255,
                     label: fade.map(Self.labelOpacity) ?? 255, sprite: KnobArrive.spriteOpacity(t))
    }
}

extension KnobSunFace {
    struct Frame: Equatable { var dy, rays: Int }

    static let fps = 15.0, risePx = 64, raysOnS = 0.72, raysInS = 1.44
    static let rest = Frame(dy: 0, rays: 255)

    static func frame(t: Double) -> Frame {
        let dy = Int((Double(risePx) * (1 - KnobArrive.ease(t))).rounded())
        let tf = t > 0 ? (t * fps + 1e-9).rounded(.down) / fps : 0
        let p = (tf - raysOnS) / (raysInS - raysOnS)
        return Frame(dy: dy, rays: !(p > 0) ? 0 : p >= 1 ? 255 : Int((255 * p).rounded()))
    }
}

extension KnobDaysFace {
    struct Frame: Equatable { var opa, dy, lo, hi: [Int] }

    static let slidePx = 12, staggerS = 0.08, inS = 0.42

    static func arrive(_ i: Int, _ t: Double) -> (opa: Int, dy: Int) {
        let p = (t - Double(i) * staggerS) / inS
        let u = !(p > 0) ? 0 : p >= 1 ? 1 : 1 - (1 - p) * (1 - p) * (1 - p)
        return (Int((255 * u).rounded()), Int((Double(slidePx) * (1 - u)).rounded()))
    }

    func frame(t: Double) -> Frame {
        let a = rows.indices.map { Self.arrive($0, t) }
        func count(_ v: Int) -> Int { KnobArrive.countValue(v, KnobArrive.countStep(KnobArrive.countN(v), t)) }
        return Frame(opa: a.map(\.opa), dy: a.map(\.dy), lo: rows.map { count($0.lo) }, hi: rows.map { count($0.hi) })
    }

    var rest: Frame { Frame(opa: rows.map { _ in 255 }, dy: rows.map { _ in 0 }, lo: rows.map(\.lo), hi: rows.map(\.hi)) }
}

extension KnobAirFace {
    struct Petal: Equatable { var x, y, rot, opa: Int }

    static let fps = 15.0, petalFrames = 105, petalPhase = 30, petalBox = 20, petalAt = CGPoint(x: 250, y: 202)

    static func ease(_ p: Double) -> Double {
        guard p > 0 else { return 0 }
        if p >= 1 { return 1 }
        var lo = 0.0, hi = 1.0
        for _ in 0..<40 {
            let u = (lo + hi) / 2, v = 1 - u
            if 3 * v * v * u * 0.42 + 3 * v * u * u * 0.58 + u * u * u < p { lo = u } else { hi = u }
        }
        let u = (lo + hi) / 2
        return 3 * (1 - u) * u * u + u * u * u
    }

    static func petal(k: Int) -> Petal {
        guard k >= 0 else { return Petal(x: 0, y: 0, rot: 0, opa: 0) }
        let t = Double((k + petalPhase) % petalFrames) / Double(petalFrames)
        let u = t < 0.62 ? ease(t / 0.62) : 1
        let op = t < 0.08 ? ease(t / 0.08) : t < 0.9 ? 1 : 1 - ease((t - 0.9) / 0.1)
        return Petal(x: Int((petalAt.x + 14 * (1 - u)).rounded()) - petalBox / 2, y: Int((petalAt.y - 58 * (1 - u)).rounded()) - petalBox / 2,
                     rot: Int((5 * u).rounded()), opa: Int((255 * op).rounded()))
    }
}
