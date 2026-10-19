import Foundation

extension KnobRainFace {
    struct Drop: Equatable {
        var x, y, len, op, slant: Double
        var tile, level: Int
    }

    struct Sheet: Equatable {
        var box: CGRect
        var tileFrames, delayFrames: Int
        var drops: [Drop]
    }

    static let sheetH = 150, ramp = 0.16

    private static func d(_ x: Double, _ y: Double, _ len: Double, _ op: Double, _ slant: Double, _ tile: Int, _ level: Int) -> Drop {
        Drop(x: x, y: y, len: len, op: op, slant: slant, tile: tile, level: level)
    }

    static let sheets: [Sheet] = [
        Sheet(box: CGRect(x: 58, y: 150, width: 60, height: 182), tileFrames: 20, delayFrames: 6, drops: [
            d(70, 12, 16, 0.9, 3, 0, 1), d(92, 70, 12, 0.6, 3, 0, 2), d(64, 118, 18, 0.8, 5, 0, 1), d(86, 30, 14, 0.75, 3, 1, 1),
            d(66, 88, 16, 1.0, 3, 1, 2), d(96, 128, 12, 0.55, 4, 1, 1), d(82, 42, 14, 0.7, 3, 0, 3), d(100, 104, 12, 0.6, 3, 0, 3),
            d(100, 4, 12, 0.65, 3, 1, 3), d(78, 62, 14, 0.7, 4, 1, 3)]),
        Sheet(box: CGRect(x: 348, y: 150, width: 54, height: 182), tileFrames: 24, delayFrames: 15, drops: [
            d(376, 20, 16, 0.85, 3, 0, 1), d(396, 84, 12, 0.6, 4, 0, 2), d(370, 126, 14, 0.95, 3, 0, 1), d(390, 8, 14, 0.7, 3, 1, 1),
            d(372, 60, 18, 0.9, 5, 1, 2), d(398, 110, 12, 0.55, 3, 1, 1), d(386, 50, 14, 0.7, 3, 0, 3), d(358, 96, 12, 0.6, 3, 0, 3),
            d(358, 34, 12, 0.65, 3, 1, 3), d(384, 136, 14, 0.7, 4, 1, 3)]),
        Sheet(box: CGRect(x: 150, y: 46, width: 166, height: 86), tileFrames: 17, delayFrames: 0, drops: [
            d(172, 10, 16, 0.9, 3, 0, 1), d(246, 64, 14, 0.7, 3, 0, 2), d(298, 112, 12, 0.8, 4, 0, 1), d(205, 128, 16, 0.6, 3, 0, 2),
            d(226, 24, 14, 1.0, 3, 1, 1), d(186, 80, 12, 0.65, 5, 1, 2), d(280, 40, 16, 0.8, 3, 1, 1), d(160, 136, 14, 0.7, 3, 1, 1),
            d(212, 36, 14, 0.7, 3, 0, 3), d(270, 92, 12, 0.6, 3, 0, 3), d(256, 108, 12, 0.65, 4, 1, 3), d(300, 70, 14, 0.7, 3, 1, 3)]),
    ]

    static func sheetOffset(_ s: Sheet, snow: Bool, k: Int) -> Int {
        let frames = 2 * s.tileFrames * (snow ? 2 : 1), period = 2 * sheetH
        let a = (max(k, 0) + s.delayFrames) % frames
        return Int((Double(period) * Double(a) / Double(frames)).rounded()) % period
    }
}

extension KnobCastFace {
    struct Streak: Equatable {
        var x, y, len, op, slant: Double
    }

    struct Sheet: Equatable {
        var box: CGRect
        var h, tileFrames, delayFrames: Int
        var width: Double
        var a, b: [Streak]
    }

    struct Cloud: Equatable {
        var back: Bool
        var base: Double
        var circles: [Puff]
    }

    struct Puff: Equatable {
        var x, y, r: Double
    }

    struct Side: Equatable {
        var tone: Int
        var fadeFrom: Double
        var clouds: [Cloud]
    }

    static let fps = 15, ramp = 0.08, breathePx = 4, rimR = 195.0

    private static func s(_ x: Double, _ y: Double, _ len: Double, _ op: Double, _ slant: Double) -> Streak {
        Streak(x: x, y: y, len: len, op: op, slant: slant)
    }

    private static func box(_ x0: Double, _ y0: Double, _ x1: Double, _ y1: Double) -> CGRect {
        CGRect(x: x0, y: y0, width: x1 - x0, height: y1 - y0)
    }

    static let sheets: [[Sheet]] = [
        [Sheet(box: box(160, 172, 302, 208), h: 40, tileFrames: 30, delayFrames: 0, width: 3,
               a: [s(176, 6, 5, 0.8, 0), s(214, 22, 5, 1, 0), s(248, 10, 5, 0.7, 0), s(282, 30, 5, 0.9, 0), s(196, 34, 5, 0.6, 0)],
               b: [s(186, 14, 5, 0.9, 0), s(232, 2, 5, 0.75, 0), s(266, 20, 5, 1, 0), s(204, 28, 5, 0.65, 0), s(292, 8, 5, 0.8, 0)])],
        [Sheet(box: box(54, 182, 172, 208), h: 40, tileFrames: 12, delayFrames: 5, width: 4,
               a: [s(72, 4, 10, 0.85, 3), s(118, 17, 10, 1, 3), s(150, 29, 10, 0.7, 3), s(94, 35, 10, 0.9, 4), s(132, 9, 10, 0.75, 3),
                   s(60, 22, 10, 0.8, 3)],
               b: [s(84, 8, 10, 0.8, 3), s(136, 21, 10, 0.95, 3), s(66, 27, 10, 0.65, 3), s(160, 36, 10, 0.85, 4), s(104, 14, 10, 0.9, 3),
                   s(146, 2, 10, 0.7, 3)]),
         Sheet(box: box(298, 174, 406, 208), h: 40, tileFrames: 14, delayFrames: 11, width: 4,
               a: [s(312, 7, 10, 0.9, 3), s(356, 20, 10, 0.75, 3), s(392, 32, 10, 1, 4), s(334, 36, 10, 0.6, 3), s(376, 2, 10, 0.85, 3),
                   s(306, 24, 10, 0.75, 3)],
               b: [s(326, 3, 10, 0.85, 3), s(372, 15, 10, 1, 3), s(398, 27, 10, 0.7, 3), s(344, 31, 10, 0.9, 4), s(388, 10, 10, 0.8, 3),
                   s(316, 37, 10, 0.7, 3)])],
        [Sheet(box: box(58, 172, 200, 208), h: 60, tileFrames: 12, delayFrames: 3, width: 4.5,
               a: [s(70, 4, 16, 0.9, 6), s(102, 22, 16, 1, 6), s(138, 10, 16, 0.75, 6), s(170, 34, 16, 0.95, 6), s(196, 48, 14, 0.7, 5)],
               b: [s(84, 14, 16, 0.85, 6), s(120, 40, 16, 1, 6), s(154, 2, 16, 0.8, 6), s(186, 24, 16, 0.9, 6), s(66, 46, 14, 0.7, 5)]),
         Sheet(box: box(248, 164, 400, 208), h: 60, tileFrames: 14, delayFrames: 9, width: 4.5,
               a: [s(262, 8, 16, 0.9, 6), s(296, 30, 16, 1, 6), s(330, 4, 16, 0.75, 6), s(362, 26, 16, 0.95, 6), s(394, 44, 14, 0.7, 5)],
               b: [s(278, 20, 16, 0.85, 6), s(312, 46, 16, 1, 6), s(346, 14, 16, 0.8, 6), s(380, 36, 16, 0.9, 6), s(256, 50, 14, 0.7, 5)])],
    ]

    private static func c(_ x: Double, _ y: Double, _ r: Double) -> Puff { Puff(x: x, y: y, r: r) }

    static let clouds: [[Side]] = [
        [Side(tone: 0, fadeFrom: 195, clouds: [Cloud(back: false, base: 170, circles: [c(168.5, 156.5, 19.5), c(203, 140, 27), c(245, 134, 28.5),
                                                                                       c(291.5, 152, 22.5)])])],
        [Side(tone: 1, fadeFrom: 180, clouds: [Cloud(back: false, base: 180, circles: [c(63.5, 161.1, 29.7), c(114.8, 144.9, 35.1), c(160.7, 163.8, 21.6)])]),
         Side(tone: 1, fadeFrom: 180, clouds: [Cloud(back: false, base: 172, circles: [c(305.3, 155.8, 21.6), c(351.2, 136.9, 35.1), c(402.5, 153.1, 29.7)])])],
        [Side(tone: 2, fadeFrom: 160, clouds: [Cloud(back: true, base: 132, circles: [c(20, 116, 28), c(66, 96, 32), c(118, 102, 30), c(166, 118, 22)]),
                                               Cloud(back: false, base: 172, circles: [c(30, 154, 30), c(80, 138, 34), c(134, 144, 30), c(186, 158, 24)])]),
         Side(tone: 2, fadeFrom: 160, clouds: [Cloud(back: true, base: 122, circles: [c(300, 108, 22), c(348, 92, 30), c(400, 86, 32), c(446, 106, 28)]),
                                               Cloud(back: false, base: 162, circles: [c(264, 148, 24), c(314, 134, 30), c(368, 128, 34), c(420, 144, 30)])])],
    ]

    static let tones: [(front: RGB, back: RGB, line: RGB)] = [
        (RGB(r: 0xE6, g: 0xEC, b: 0xF2), RGB(r: 0xE6, g: 0xEC, b: 0xF2), RGB(r: 0xC4, g: 0xCC, b: 0xD4)),
        (RGB(r: 0x8E, g: 0x96, b: 0xA0), RGB(r: 0x73, g: 0x7B, b: 0x86), RGB(r: 0xA8, g: 0xB0, b: 0xBA)),
        (RGB(r: 0x55, g: 0x5D, b: 0x68), RGB(r: 0x3A, g: 0x40, b: 0x48), RGB(r: 0x6A, g: 0x74, b: 0x80)),
    ]

    static let breathe: [[(period: Double, delay: Double)]] = [[(8, 0), (8, 0)], [(8, 0), (9, -4)], [(8, 0), (8.5, -2)]]

    static func sheetOffset(_ s: Sheet, snow: Bool, k: Int) -> Int {
        let frames = 2 * s.tileFrames * (snow ? 2 : 1), period = 2 * s.h
        let a = (max(k, 0) + s.delayFrames) % frames
        return Int((Double(period) * Double(a) / Double(frames)).rounded()) % period
    }

    static func triSteps(_ t: Double, period: Double, delay: Double, n: Int) -> Double {
        var p = fmod(t - delay, period)
        if p < 0 { p += period }
        let half = period / 2, u = p < half ? p / half : (p - half) / half
        let q = (u * Double(n) + 1e-9).rounded(.down) / Double(n)
        return p < half ? q : 1 - q
    }

    static func breathe(variant: Int, side: Int, k: Int) -> Int {
        guard (1...3).contains(variant), (0..<2).contains(side) else { return 0 }
        let b = breathe[variant - 1][side], n = Int((b.period / 2 * Double(fps)).rounded())
        let f = triSteps(Double(max(k, 0)) / Double(fps), period: b.period, delay: b.delay, n: n)
        return Int((Double(-breathePx) + 2 * Double(breathePx) * f).rounded())
    }
}

extension KnobWindFace {
    struct Row: Equatable {
        var dy, x0, len, period, phase: Float
        var steps: Int
    }

    struct Leaf: Equatable {
        var dy, period, phase: Float
    }

    struct Geo {
        var mount: CGPoint
        var droop: Double
        var poseFrames: Int
        var ang, stretch: Double
        var quads: [[CGPoint]]
        var rows: [Row]
        var leaves: [Leaf]
    }

    struct Frame: Equatable {
        struct Streak: Equatable { var dx, opa: Int }
        struct LeafAt: Equatable { var x, y, rot, opa: Int }
        var pose = 1
        var streaks: [Streak] = []
        var leaves: [LeafAt] = []
    }

    static let fps = 15, leafBox = 20, leafRots = 12, poseSeq = [0, 1, 2, 1]

    private static func q(_ p: (Double, Double)...) -> [CGPoint] { p.map { CGPoint(x: $0.0, y: $0.1) } }

    static let geo: [Geo] = [
        Geo(mount: CGPoint(x: 240.3, y: 78), droop: 64.3, poseFrames: 10, ang: 5, stretch: 0.012, quads: [
            q((0.76, -22.79), (27.62, -16.04), (21.75, 17.69), (-0.76, 22.79)),
            q((27.62, -16.04), (50.99, 1.03), (45.41, 15.74), (21.75, 17.69)),
            q((50.99, 1.03), (73.5, 12.21), (68.13, 24.42), (45.41, 15.74)),
            q((73.5, 12.21), (95.63, 23.24), (91.23, 33.26), (68.13, 24.42)),
            q((95.63, 23.24), (117.76, 34.27), (114.33, 42.1), (91.23, 33.26))],
            rows: [Row(dy: 40, x0: 14, len: 22, period: 3, phase: 0, steps: 22)], leaves: []),
        Geo(mount: CGPoint(x: 201.7, y: 101), droop: 26.8, poseFrames: 5, ang: 4, stretch: 0.02, quads: [
            q((0, -22.8), (24.7, -20.14), (24.7, 20.14), (0, 22.8)),
            q((24.7, -20.14), (49.4, -17.48), (49.4, 17.48), (24.7, 20.14)),
            q((49.4, -17.48), (75.42, -14.76), (72.78, 14.76), (49.4, 17.48)),
            q((75.42, -14.76), (100.57, -2.6), (96.26, 11.34), (72.78, 14.76)),
            q((100.57, -2.6), (122.69, 10.58), (119.19, 18.38), (96.26, 11.34))],
            rows: [Row(dy: -34, x0: 30, len: 34, period: 1.6, phase: 0, steps: 12), Row(dy: 64, x0: 46, len: 30, period: 1.6, phase: -0.53, steps: 12),
                   Row(dy: 86, x0: 22, len: 26, period: 1.6, phase: -1.07, steps: 12)], leaves: []),
        Geo(mount: CGPoint(x: 191.2, y: 101), droop: 0, poseFrames: 2, ang: 3, stretch: 0.03, quads: [
            q((0, -22.8), (24.7, -20.14), (24.7, 20.14), (0, 22.8)),
            q((24.7, -20.14), (49.4, -17.48), (49.4, 17.48), (24.7, 20.14)),
            q((49.4, -17.48), (74.1, -14.82), (74.1, 14.82), (49.4, 17.48)),
            q((74.1, -14.82), (98.8, -12.16), (98.8, 12.16), (74.1, 14.82)),
            q((98.8, -12.16), (123.5, -9.5), (123.5, 9.5), (98.8, 12.16))],
            rows: [Row(dy: -26, x0: 18, len: 36, period: 0.8, phase: 0, steps: 6), Row(dy: 34, x0: 40, len: 32, period: 0.8, phase: -0.2, steps: 6),
                   Row(dy: 54, x0: 14, len: 30, period: 0.8, phase: -0.4, steps: 6), Row(dy: 72, x0: 52, len: 28, period: 0.8, phase: -0.6, steps: 6)],
            leaves: [Leaf(dy: -10, period: 1.2, phase: 0), Leaf(dy: 64, period: 1.6, phase: -0.7)]),
    ]

    static let arm = 22.0, poleTop = 26.0, baseY = 205.0

    static func geo(level: Int) -> Geo { geo[min(max(level, 0), geo.count - 1)] }

    public static func sockQuads(level: Int, pose: Int = 1) -> [[CGPoint]] {
        let g = geo(level: level)
        let (a, sx, sy): (Double, Double, Double) = pose == 0 ? (-g.ang, 1 - g.stretch, 1.02) : pose == 2 ? (g.ang, 1 + g.stretch, 0.96) : (0, 1, 1)
        let r = (a + g.droop) * .pi / 180, c = cos(r), s = sin(r), m = g.mount
        return g.quads.map { q in
            q.map { p in
                let x = p.x * sx, y = p.y * sy
                return CGPoint(x: m.x + x * c - y * s, y: m.y + x * s + y * c)
            }
        }
    }

    static func mount(level: Int) -> CGPoint { geo(level: level).mount }

    private static func stepped(_ f: Double, _ n: Int) -> Double { (f * Double(n) + 1e-6).rounded(.down) / Double(n) }

    private static func animT(_ k: Int, period: Float, phase: Float) -> Double {
        let frames = Double((period * Float(fps)).rounded())
        var t = fmod(Double(k) - Double(phase) * Double(fps), frames)
        if t < 0 { t += frames }
        return t / Double(fps)
    }

    static func frame(level: Int, k: Int?) -> Frame {
        var f = Frame()
        guard let k, k >= 0 else { return f }
        let g = geo(level: level)
        f.pose = poseSeq[(k / g.poseFrames) % 4]
        f.streaks = g.rows.map { w in
            let half = Double(w.period) / 2, t = animT(k, period: w.period, phase: w.phase), first = t < half
            let u = stepped((first ? t : t - half) / half, w.steps)
            return Frame.Streak(dx: Int((first ? -24 + 40 * u : 16 + 40 * u).rounded()), opa: Int((255 * 0.75 * (first ? u : 1 - u)).rounded()))
        }
        f.leaves = g.leaves.map { l in
            let n = Int(Double(l.period / 2 * Float(fps)).rounded()), p = animT(k, period: l.period, phase: l.phase) / Double(l.period)
            let (tx, ty, rot): (Double, Double, Double)
            if p < 0.5 {
                let u = stepped(p / 0.5, n)
                (tx, ty, rot) = (70 * u, -10 * u, 160 * u)
            } else {
                let u = stepped((p - 0.5) / 0.5, n)
                (tx, ty, rot) = (70 + 70 * u, -10 + 16 * u, 160 + 170 * u)
            }
            let op = p < 0.12 ? stepped(p / 0.12, n) : p < 0.88 ? 1 : 1 - stepped((p - 0.88) / 0.12, n)
            let cx = Double(Float(g.mount.x) - 6 + 8) + tx, cy = Double(Float(g.mount.y) + l.dy) + ty
            return Frame.LeafAt(x: Int(cx.rounded()) - leafBox / 2, y: Int(cy.rounded()) - leafBox / 2,
                                rot: Int((rot / (180 / Double(leafRots))).rounded()) % leafRots, opa: Int((255 * op).rounded()))
        }
        return f
    }

    static func streakStart(level: Int, row r: Int) -> CGPoint {
        let g = geo(level: level), w = g.rows[r]
        return CGPoint(x: Double(Float(g.mount.x) + w.x0), y: Double(Float(g.mount.y) + w.dy))
    }

    static func leafOutline() -> [CGPoint] {
        let ts = (0...32).map { Double($0) / 32 }
        func x(_ t: Double) -> Double { 12 * t + 12 * t * t - 8 * t * t * t - 8 }
        func hw(_ t: Double) -> Double { 15 * t * (1 - t) }
        return ts.map { CGPoint(x: x($0), y: -hw($0)) } + ts.reversed().map { CGPoint(x: x($0), y: hw($0)) }
    }
}
