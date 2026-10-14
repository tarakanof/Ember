import CoreGraphics
import Testing
@testable import EmberKit

private let geometry = KnobTheme.standard.bot.eyes

private func frames(_ b: inout BotBehavior, from: Double = 0, seconds: Double) -> [(t: Double, pose: BotPose)] {
    stride(from: from, to: from + seconds, by: 1.0 / 60).map { ($0, b.pose(at: $0)) }
}

@Test func knobEyesTiltAndRiseMirrorWithTheGaze() {
    for eyes in [BotEyes.dash, .round] {
        var right = BotPose(), left = BotPose()
        right.eyes = eyes; left.eyes = eyes
        (right.gazeX, right.gazeY) = (0.67, 0.3)
        (left.gazeX, left.gazeY) = (-0.67, 0.3)
        let r = KnobBotShape.eyes(right, scale: 1.15, geometry: geometry)
        let l = KnobBotShape.eyes(left, scale: 1.15, geometry: geometry)
        for (a, b) in [(r[0], l[1]), (r[1], l[0])] {
            let mirrored = Set(b.points.map { "\(Int((-$0.x * 1e6).rounded())),\(Int(($0.y * 1e6).rounded()))" })
            let points = Set(a.points.map { "\(Int(($0.x * 1e6).rounded())),\(Int(($0.y * 1e6).rounded()))" })
            #expect(points == mirrored, "\(eyes)")
        }
    }
}

@Test func knobLidIsAClipLineFromTheTopThatDroopsAndWidens() {
    var open = BotPose(), shut = BotPose()
    open.eyes = .dash; shut.eyes = .dash
    shut.lidLeft = 1; shut.lidRight = 1
    let o = KnobBotShape.eyes(open, scale: 1, geometry: geometry)[0]
    let s = KnobBotShape.eyes(shut, scale: 1, geometry: geometry)[0]
    #expect(o.lid == 0 && s.lid == 1)
    #expect(abs(s.keep - geometry.lidKeep) < 1e-12)
    #expect(abs(s.width / o.width - (1 + geometry.dash.widen)) < 1e-9)
    let spanO = abs(o.points[1].y - o.points[0].y) + o.width, spanS = abs(s.points[1].y - s.points[0].y) + s.width
    #expect(abs(spanO - spanS) < 0.02)
    let midO = (o.points[0].y + o.points[1].y) / 2, midS = (s.points[0].y + s.points[1].y) / 2
    #expect(abs(midO - midS - geometry.lidDroop) < 1e-9)

    #expect(KnobBotShape.lidLineY([100, 140], width: 20, lid: 0, keep: 0.3) == nil)
    #expect(abs((KnobBotShape.lidLineY([100, 140], width: 20, lid: 1, keep: 0.3) ?? 0) - (151 - 6)) < 1e-9)
    #expect(abs((KnobBotShape.lidLineY([100, 140], width: 20, lid: 0.5, keep: 0) ?? 0) - (89 + 31)) < 1e-9)
}

@Test func knobHappyEyesClipOnlyBehindASwapBlink() {
    var p = BotPose()
    p.eyes = .happy
    p.lidLeft = 1; p.lidRight = 1
    #expect(KnobBotShape.eyes(p, scale: 1, geometry: geometry).allSatisfy { $0.lid == 0 && $0.keep == 0 })
    p.hide = true; p.blinkLeft = 0.9; p.blinkRight = 0.5
    let e = KnobBotShape.eyes(p, scale: 1, geometry: geometry)
    #expect(e[0].lid == 1 && e[1].lid == 0)
}

@Test func knobEyeShapeSwapsOnlyBehindAHidingBlink() {
    for seed in UInt64(0)..<40 {
        var b = BotBehavior(seed: seed, now: 0, tuning: KnobTheme.standard.botTuning)
        var prev: BotPose?
        let moods: [BotMood] = [.working, .waiting, .done, .error, .idle]
        for (i, m) in moods.enumerated() {
            let t0 = 2.0 + Double(i) * 1.37
            for t in stride(from: t0 - 1.37, to: t0, by: 1.0 / 60) {
                let p = b.pose(at: t)
                if let q = prev, q.eyes != p.eyes {
                    #expect(p.hide && min(p.blinkLeft, p.blinkRight) >= geometry.blinkHide, "seed \(seed) t \(t)")
                    #expect(KnobBotShape.eyes(p, scale: 1, geometry: geometry).allSatisfy { $0.lid >= 1 && $0.keep == 0 }
                            || p.eyes == .happy, "seed \(seed) t \(t)")
                }
                prev = p
            }
            b.setMood(m, at: t0)
        }
        let last = frames(&b, from: 9, seconds: 3).last!.pose
        #expect(last.eyes == .dash && !last.hide, "seed \(seed)")
    }
}

@Test func macBotNeverHidesOrHuffs() {
    var b = BotBehavior(seed: 5, now: 0)
    b.setMood(.error, at: 0)
    #expect(frames(&b, seconds: 120).allSatisfy { !$0.pose.hide && $0.pose.eyeDY == 0 })
}

@Test func knobErrorShakesAfterTheSwapThenHuffs() {
    let fx = KnobTheme.standard.bot.error
    var b = BotBehavior(seed: 11, now: 0, tuning: KnobTheme.standard.botTuning)
    b.setMood(.error, at: 1)
    let f = frames(&b, from: 0, seconds: 180)
    let swap = try! #require(f.first { $0.pose.eyes == .angry }).t
    let shake = f.filter { $0.t >= swap + fx.shakeAtS && $0.t < swap + fx.shakeAtS + fx.shakeS }.map(\.pose.offsetX)
    var turns = 0
    for i in 2..<shake.count where (shake[i] - shake[i - 1]) * (shake[i - 1] - shake[i - 2]) < 0 { turns += 1 }
    #expect(turns >= 5)
    #expect((shake.max() ?? 0) - (shake.min() ?? 0) > fx.shakeAmp)

    let huffs = f.filter { $0.pose.eyeDY < -fx.huffDip * 0.9 }
    #expect(!huffs.isEmpty)
    #expect(huffs.allSatisfy { min($0.pose.lidLeft, $0.pose.lidRight) >= fx.huffLid * 0.9 })
    #expect(huffs.allSatisfy { $0.t > swap + fx.shakeAtS + fx.huffMedianS / 2 })

    var calm = BotBehavior(seed: 11, now: 0, tuning: KnobTheme.standard.botTuning)
    calm.reduceMotion = true
    calm.setMood(.error, at: 1)
    #expect(frames(&calm, seconds: 180).allSatisfy { $0.pose.eyeDY == 0 && $0.pose.offsetX == 0 })
}

@Test func knobErrorGazeHoldsTheViewerMoreThanTheMacBot() {
    func viewerShare(_ tuning: BotBehavior.Tuning) -> Double {
        var b = BotBehavior(seed: 3, now: 0, tuning: tuning)
        b.setMood(.error, at: 0)
        let f = frames(&b, seconds: 300)
        return Double(f.filter { abs($0.pose.gazeX) < 0.12 && abs($0.pose.gazeY - 0.1) < 0.12 }.count) / Double(f.count)
    }
    let knob = viewerShare(KnobTheme.standard.botTuning), mac = viewerShare(.mac)
    #expect(knob > 0.3)
    #expect(knob > mac)
}

@Test func macSleepyBlinkAddsNoFrame() {
    var b = BotBehavior(seed: 9, now: 0)
    var shown: BotPose?, lidShown: BotPose?, pushes = 0, lidPushes = 0
    for (_, p) in frames(&b, from: 0, seconds: 360) {
        let q = p.quantized(toPixels: 16)
        var lidOnly = q
        lidOnly.blinkLeft = 0; lidOnly.blinkRight = 0
        if q != shown { pushes += 1; shown = q }
        if lidOnly != lidShown { lidPushes += 1; lidShown = lidOnly }
        #expect(!p.hide)
    }
    #expect(b.mood == .sleepy)
    #expect(pushes == lidPushes)
}

@Test func knobSwapAt20FPSHappensOnlyOnAShutFrame() {
    for seed in UInt64(0)..<40 {
        var b = BotBehavior(seed: seed, now: 0, tuning: KnobTheme.standard.botTuning)
        var prev: BotPose?
        for (i, m) in [BotMood.working, .waiting, .done, .error, .idle].enumerated() {
            let t0 = 2.0 + Double(i) * 1.5
            for t in stride(from: t0 - 1.5, to: t0, by: 1.0 / 20) {
                let p = b.pose(at: t)
                if let q = prev, q.eyes != p.eyes {
                    #expect(p.hide && min(p.blinkLeft, p.blinkRight) >= geometry.blinkHide, "seed \(seed) t \(t)")
                }
                prev = p
            }
            b.setMood(m, at: t0)
        }
        var last = BotPose()
        for t in stride(from: 9.5, to: 12, by: 1.0 / 20) { last = b.pose(at: t) }
        #expect(last.eyes == .dash && !last.hide, "seed \(seed)")
    }
}

@Test func knobReduceMotionKeepsARunningShakeAndStopsHuffs() {
    let fx = KnobTheme.standard.bot.error
    func started() -> (BotBehavior, Double) {
        var b = BotBehavior(seed: 11, now: 0, tuning: KnobTheme.standard.botTuning)
        b.setMood(.error, at: 1)
        let f = frames(&b, from: 0, seconds: 1.2)
        let swap = f.first { $0.pose.eyes == .angry }!.t
        let mid = swap + fx.shakeAtS + fx.shakeS / 4
        _ = frames(&b, from: 1.2, seconds: mid - 1.2)
        return (b, mid)
    }
    var (calm, mid) = started()
    var (loud, _) = started()
    calm.reduceMotion = true
    let a = calm.pose(at: mid + 0.02), b = loud.pose(at: mid + 0.02)
    #expect(a.offsetX == b.offsetX)
    #expect(frames(&calm, from: mid + 1, seconds: 300).allSatisfy { $0.pose.eyeDY == 0 })
    #expect(frames(&loud, from: mid + 1, seconds: 300).contains { $0.pose.eyeDY != 0 })
}

@Test func macReduceMotionReassignInErrorKeepsTheNextEventFinite() {
    var b = BotBehavior(seed: 4, now: 0)
    b.setMood(.error, at: 0)
    _ = frames(&b, seconds: 2)
    b.reduceMotion = false
    _ = b.pose(at: 2)
    #expect(b.nextEventAt.isFinite && b.nextEventAt > 2)
    b.reduceMotion = true
    b.reduceMotion = false
    _ = b.pose(at: 2.1)
    #expect(b.nextEventAt.isFinite && b.nextEventAt > 2.1)
}
