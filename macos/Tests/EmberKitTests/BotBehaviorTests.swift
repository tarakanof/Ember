import Testing
import CoreGraphics
@testable import EmberKit

/// Steps the behaviour at 60 fps from `from` for `seconds`, collecting poses.
private func run(_ b: inout BotBehavior, from: Double = 0, seconds: Double) -> [(t: Double, pose: BotPose, animating: Bool)] {
    stride(from: from, to: from + seconds, by: 1.0 / 60).map { t in
        let p = b.pose(at: t)
        return (t, p, b.isAnimating)
    }
}

private func blinkCount(_ frames: [(t: Double, pose: BotPose, animating: Bool)]) -> Int {
    var n = 0, shut = false
    for f in frames {
        let closed = max(f.pose.lidLeft, f.pose.lidRight) >= 0.9
        if closed && !shut { n += 1 }
        shut = closed
    }
    return n
}

@Test func moodFollowsSessionState() {
    #expect(BotMood(state: "running") == .working)
    #expect(BotMood(state: "waiting") == .waiting)
    #expect(BotMood(state: "error") == .error)
    #expect(BotMood(state: "done") == .done)
    #expect(BotMood(state: "idle") == .idle)
    #expect(BotMood(state: "") == .idle)
}

@Test func idleBlinkRateIsHumanLike() {
    var b = BotBehavior(seed: 1, now: 0)
    let blinks = blinkCount(run(&b, seconds: 240))
    // ~15–20/min for people; allow the double blinks and lognormal spread.
    #expect((40...100).contains(blinks), "blinks in 4 min: \(blinks)")
}

@Test func workingBlinksLessThanIdle() {
    var idle = BotBehavior(seed: 2, now: 0)
    var work = BotBehavior(seed: 2, now: 0)
    work.setMood(.working, at: 0)
    #expect(blinkCount(run(&work, seconds: 240)) < blinkCount(run(&idle, seconds: 240)))
}

@Test func mostlyStillSoTheFrameLoopCanSleep() {
    var b = BotBehavior(seed: 3, now: 0)
    let frames = run(&b, seconds: 120)
    let busy = Double(frames.filter(\.animating).count) / Double(frames.count)
    #expect(busy < 0.3, "animating \(busy * 100)% of the time")
}

@Test func nextEventIsInTheFutureWhenStill() {
    var b = BotBehavior(seed: 4, now: 0)
    for f in run(&b, seconds: 30) where !f.animating {
        #expect(b.nextEventAt >= f.t || f.t == 30 - 1.0 / 60)
    }
}

@Test func eyesChangeOnlyBehindClosedLids() {
    var b = BotBehavior(seed: 5, now: 0)
    _ = run(&b, seconds: 2)
    b.setMood(.waiting, at: 2)
    let frames = run(&b, from: 2, seconds: 2)
    guard let first = frames.firstIndex(where: { $0.pose.eyes == .round }) else {
        Issue.record("eyes never swapped"); return
    }
    #expect(max(frames[first].pose.lidLeft, frames[first].pose.lidRight) >= 0.9)
    #expect(frames[first].t - 2 < 0.5)
}

@Test func errorMorphsBodyToTriangleAndBack() {
    var b = BotBehavior(seed: 6, now: 0)
    b.setMood(.error, at: 0)
    #expect(run(&b, seconds: 1).last!.pose.triangle == 1)
    b.setMood(.idle, at: 1)
    #expect(run(&b, from: 1, seconds: 1).last!.pose.triangle == 0)
}

@Test func longIdleGetsSleepyAndWakesWithADoubleBlink() {
    var b = BotBehavior(seed: 7, now: 0)
    _ = b.pose(at: BotBehavior.sleepAfter + 1)
    #expect(b.mood == .sleepy)
    let drowsy = run(&b, from: BotBehavior.sleepAfter + 1, seconds: 3).last!.pose
    #expect(drowsy.lidLeft > 0.4)                  // heavy lids

    let t = BotBehavior.sleepAfter + 4
    b.setMood(.working, at: t)
    #expect(blinkCount(run(&b, from: t, seconds: 0.6)) >= 2)
}

@Test func idleRequestDoesNotWakeASleepyBot() {
    var b = BotBehavior(seed: 8, now: 0)
    _ = b.pose(at: BotBehavior.sleepAfter + 1)
    b.setMood(.idle, at: BotBehavior.sleepAfter + 2)
    #expect(b.mood == .sleepy)
}

@Test func reduceMotionKeepsGazeStill() {
    var b = BotBehavior(seed: 9, now: 0)
    b.reduceMotion = true
    let frames = run(&b, seconds: 20)
    #expect(Set(frames.map { $0.pose.gazeX }).count == 1)
    #expect(blinkCount(frames) > 0)                // still blinks
}

@Test func sameSeedSameAnimation() {
    var a = BotBehavior(seed: 10, now: 0), b = BotBehavior(seed: 10, now: 0)
    #expect(run(&a, seconds: 10).map(\.pose) == run(&b, seconds: 10).map(\.pose))
}

@Test func blinkClosesFasterThanItOpens() {
    let close = 0.075, openEnd = 0.26
    #expect(BotBehavior.lid(close, speed: 1) >= 0.99)
    #expect(BotBehavior.lid(openEnd - 0.05, speed: 1) > 0)   // still opening
    #expect(BotBehavior.lid(openEnd + 0.01, speed: 1) == 0)
}

@Test func rendererDrawsBodyAndCutsOutEyes() {
    let size = 64
    let ctx = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size * 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    BotRenderer.draw(BotPose(), in: ctx, rect: CGRect(x: 0, y: 0, width: size, height: size),
                     style: .menuBar(tint: CGColor(gray: 0, alpha: 1)))
    let px = ctx.data!.assumingMemoryBound(to: UInt8.self)
    func alpha(_ x: Int, _ y: Int) -> UInt8 { px[((size - 1 - y) * size + x) * 4 + 3] }   // y-up
    #expect(alpha(size / 2, size / 3) == 255)      // lower body is solid
    #expect(alpha(1, 1) == 0)                      // corner is empty
    // Somewhere in the upper-right quadrant the eyes punch through.
    let holes = (size / 2..<size * 7 / 8).flatMap { x in (size / 2..<size * 7 / 8).map { alpha(x, $0) } }
    #expect(holes.contains(0) && holes.contains(255))
}
