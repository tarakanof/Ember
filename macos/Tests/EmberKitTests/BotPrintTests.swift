import Testing
import CoreGraphics
import Foundation
@testable import EmberKit

private let t0 = Date(timeIntervalSince1970: 1_800_000_000)

private func printer(_ phase: String, _ progress: Int? = nil, event: PrinterEvent? = nil, enabled: Bool = true) -> PrinterState {
    PrinterState(enabled: enabled, phase: phase, progress: progress, event: event)
}

private func finished(_ id: UInt64, at: Date = t0) -> PrinterEvent {
    PrinterEvent(id: id, kind: "finished", at: Int64(at.timeIntervalSince1970))
}

@Test func fillFollowsTheActivePhases() {
    var t = BotPrintTracker()
    for phase in ["heating", "preparing"] {
        #expect(t.update(state: printer(phase, 0), agentState: "idle", enabled: true, now: t0) == .starting, "\(phase)")
    }
    for phase in ["printing", "finishing", "paused"] {
        #expect(t.update(state: printer(phase, 42), agentState: "idle", enabled: true, now: t0) == .printing(percent: 42), "\(phase)")
    }
    #expect(t.update(state: printer("attention", 42), agentState: "running", enabled: true, now: t0) == .attention(percent: 42))
    for phase in ["off", "offline", "idle", "finished", "stopped", "error"] {
        #expect(t.update(state: printer(phase, 100), agentState: "idle", enabled: true, now: t0) == BotPrint.none, "\(phase)")
    }
    #expect(t.update(state: printer("printing", 140), agentState: "idle", enabled: true, now: t0) == .printing(percent: 100))
    #expect(t.update(state: printer("printing"), agentState: "idle", enabled: true, now: t0) == .printing(percent: 0))
    #expect(BotPrint.starting.fill == 0)
}

@Test func theSettingAndThePrinterSwitchTheFillOff() {
    var t = BotPrintTracker()
    #expect(t.update(state: printer("printing", 42), agentState: "idle", enabled: false, now: t0) == BotPrint.none)
    #expect(t.update(state: printer("printing", 42, enabled: false), agentState: "idle", enabled: true, now: t0) == BotPrint.none)
    #expect(t.update(state: nil, agentState: "idle", enabled: true, now: t0) == BotPrint.none)
}

@Test func aWaitingOrFailingAgentHidesFillAndBadge() {
    var t = BotPrintTracker()
    for agent in ["waiting", "error"] {
        #expect(t.update(state: printer("printing", 42), agentState: agent, enabled: true, now: t0) == BotPrint.none)
        #expect(t.update(state: printer("attention", 42), agentState: agent, enabled: true, now: t0) == BotPrint.none)
    }
    #expect(t.update(state: printer("attention", 42), agentState: "done", enabled: true, now: t0) == .attention(percent: 42))
}

@Test func doneShowsOncePerEventForAMinute() {
    var t = BotPrintTracker()
    let s = printer("finished", 100, event: finished(7))
    #expect(t.update(state: s, agentState: "idle", enabled: true, now: t0) == .done)
    #expect(t.playedDoneID == 7)
    #expect(t.doneEndsAt == t0.addingTimeInterval(60))
    #expect(t.update(state: s, agentState: "running", enabled: true, now: t0.addingTimeInterval(59)) == .done)
    #expect(t.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(60)) == BotPrint.none)
    #expect(t.doneEndsAt == nil)
    #expect(t.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(120)) == BotPrint.none)
    let next = printer("finished", 100, event: finished(8, at: t0.addingTimeInterval(600)))
    #expect(t.update(state: next, agentState: "idle", enabled: true, now: t0.addingTimeInterval(601)) == .done)
}

@Test func doneDoesNotReplayAfterARestart() {
    let s = printer("finished", 100, event: finished(7))
    var restarted = BotPrintTracker(playedDoneID: 7)
    #expect(restarted.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(300)) == BotPrint.none)
    var fresh = BotPrintTracker()
    #expect(fresh.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(29 * 60)) == .done)
    var late = BotPrintTracker()
    #expect(late.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(30 * 60)) == BotPrint.none)
    #expect(late.playedDoneID == nil)
}

@Test func aWaitingAgentDefersDoneAndCutsItShort() {
    var t = BotPrintTracker()
    let s = printer("finished", 100, event: finished(7))
    #expect(t.update(state: s, agentState: "waiting", enabled: true, now: t0) == BotPrint.none)
    #expect(t.playedDoneID == nil)
    #expect(t.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(90)) == .done)
    #expect(t.update(state: s, agentState: "error", enabled: true, now: t0.addingTimeInterval(100)) == BotPrint.none)
    #expect(t.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(110)) == BotPrint.none)
}

private func frames(_ b: inout BotBehavior, from: Double, seconds: Double) -> [BotPose] {
    stride(from: from, to: from + seconds, by: 1.0 / 60).map { b.pose(at: $0) }
}

@Test func fillIsQuantizedToWholeMenuBarPixels() {
    let r = 16.0
    var keys: [BotPose] = []
    for percent in 0...100 {
        var p = BotPose()
        p.fill = BotPrint.printing(percent: percent).fill
        let key = p.quantized(toPixels: r)
        let span = 2 * r * (1 - BotRenderer.printRim)
        #expect(abs(key.fill * span - (key.fill * span).rounded()) < 1e-9)
        if keys.last != key { keys.append(key) }
    }
    #expect(keys.count == 29, "pushes over a whole print: \(keys.count)")
    var a = BotPose(), b = BotPose()
    a.fill = 0.41; b.fill = 0.42
    #expect(a.quantized(toPixels: r) == b.quantized(toPixels: r))
    b.fill = 0.45
    #expect(a.quantized(toPixels: r) != b.quantized(toPixels: r))
}

@Test func printingAddsNoRunningAnimation() {
    var b = BotBehavior(seed: 3, now: 0)
    _ = frames(&b, from: 0, seconds: 2)
    b.setPrint(.printing(percent: 42), at: 2)
    let poses = frames(&b, from: 2, seconds: 30)
    #expect(poses.allSatisfy { $0.fill == 0.42 && $0.printBadge == 0 && $0.sparkle == 0 })
    #expect(poses.allSatisfy { $0.offsetY < 0.1 })
}

@Test func attentionShowsAStaticBadge() {
    var b = BotBehavior(seed: 4, now: 0)
    b.setPrint(.attention(percent: 61), at: 0)
    let poses = frames(&b, from: 0, seconds: 20)
    #expect(poses.allSatisfy { $0.printBadge == 1 && $0.fill == 0.61 })
    b.setPrint(.printing(percent: 61), at: 20)
    #expect(b.pose(at: 20).printBadge == 0)
}

@Test func doneHopsSparklesAndSwapsToHappyEyesBehindALid() {
    for seed in UInt64(0)..<20 {
        var b = BotBehavior(seed: seed, now: 0)
        b.setMood(.working, at: 0)
        _ = frames(&b, from: 0, seconds: 3)
        b.setPrint(.done, at: 3)
        let poses = frames(&b, from: 3, seconds: 2)
        #expect(poses.allSatisfy { $0.fill == 1 })
        #expect(poses.contains { $0.offsetY > 0.1 }, "seed \(seed): no hop")
        #expect(poses.contains { $0.sparkle > 0 }, "seed \(seed): no sparkle")
        #expect(poses.last?.eyes == .happy, "seed \(seed)")
        #expect(poses.last?.sparkle == 0)
        var open = true
        var prev = poses[0].eyes
        for p in poses {
            if p.eyes != prev { #expect(!open || p.hide || max(p.blinkLeft, p.blinkRight) >= 0.9, "seed \(seed): swap with open eyes") }
            open = max(p.blinkLeft, p.blinkRight) < 0.9
            prev = p.eyes
        }
        #expect(b.mood == .working)
        b.setPrint(.none, at: 5)
        let after = frames(&b, from: 5, seconds: 1.5)
        #expect(after.last?.eyes == .dash && after.last?.fill == 0, "seed \(seed)")
    }
}

@Test func doneWakesASleepyBot() {
    var b = BotBehavior(seed: 9, now: 0)
    _ = frames(&b, from: 0, seconds: BotBehavior.sleepAfter + 1)
    #expect(b.mood == .sleepy)
    b.setPrint(.done, at: BotBehavior.sleepAfter + 1)
    let poses = frames(&b, from: BotBehavior.sleepAfter + 1, seconds: 2)
    #expect(b.mood == .idle)
    #expect(poses.last?.eyes == .happy)
}

@Test func reduceMotionKeepsTheFillButDropsHopAndSparkle() {
    var b = BotBehavior(seed: 5, now: 0)
    b.reduceMotion = true
    _ = frames(&b, from: 0, seconds: 2)
    b.setPrint(.done, at: 2)
    let poses = frames(&b, from: 2, seconds: 3)
    #expect(poses.allSatisfy { $0.fill == 1 && $0.offsetY < 0.05 && $0.sparkle == 0 })
    #expect(poses.last?.eyes == .happy)

    var mid = BotBehavior(seed: 6, now: 0)
    mid.setPrint(.done, at: 0)
    _ = frames(&mid, from: 0, seconds: 0.2)
    mid.reduceMotion = true
    #expect(frames(&mid, from: 0.2, seconds: 2).allSatisfy { $0.sparkle == 0 })
}

private func render(_ pose: BotPose, style: BotStyle, size: Int = 44) -> (Int, Int) -> (r: UInt8, g: UInt8, b: UInt8, a: UInt8) {
    let ctx = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size * 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    BotRenderer.draw(pose, in: ctx, rect: CGRect(x: 0, y: 0, width: size, height: size), style: style)
    let data = Data(bytes: ctx.data!, count: size * size * 4)
    return { x, y in
        let i = ((size - 1 - y) * size + x) * 4
        return (data[i], data[i + 1], data[i + 2], data[i + 3])
    }
}

private func isOrange(_ p: (r: UInt8, g: UInt8, b: UInt8, a: UInt8)) -> Bool {
    p.a == 255 && p.r > 230 && (90...120).contains(p.g) && p.b < 70
}

@Test func rendererFillsTheBallFromTheBottomInPrusaOrange() {
    var pose = BotPose()
    pose.fill = 0.42
    let px = render(pose, style: .menuBar(tint: CGColor(gray: 1, alpha: 1)))
    let center = 22, bodyBottom = 22 - 16 - 2
    #expect(isOrange(px(center, bodyBottom + 6)))
    #expect(!isOrange(px(center - 8, bodyBottom + 22)))
    #expect(px(center - 8, bodyBottom + 22).a == 255)
    #expect(!isOrange(px(center, bodyBottom + 1)), "the rim keeps the mood colour")
}

@Test func monochromeFillIsATemplateShade() {
    var pose = BotPose()
    pose.fill = 0.42
    let px = render(pose, style: .menuBar(tint: CGColor(gray: 0, alpha: 1), template: true))
    let shade = px(22, 10).a
    #expect((80...100).contains(shade), "fill alpha \(shade)")
    #expect(px(14, 26).a == 255)
}

@Test func attentionBadgeSitsTopRight() {
    var pose = BotPose()
    pose.printBadge = 1
    let c = BotRenderer.printBadgeAt, r = 16.0
    let bx = Int(22 + c.x * r), by = Int(22 - 0.12 * r + c.y * r)
    let colored = render(pose, style: .menuBar(tint: CGColor(gray: 1, alpha: 1)))
    #expect(isOrange(colored(bx + 3, by)))
    let mono = render(pose, style: .menuBar(tint: CGColor(gray: 0, alpha: 1), template: true))
    #expect(mono(bx + 3, by).a == 255)
    #expect(mono(bx, by + 1).a < 128, "the ! is cut out of the template badge")
    let plain = render(BotPose(), style: .menuBar(tint: CGColor(gray: 1, alpha: 1)))
    #expect(plain(bx + 3, by).a == 0)
}

@Test func onlyAVisibleChangeCountsAsANewLook() {
    let r = 16.0
    #expect(BotPrint.printing(percent: 41).looksLike(.printing(percent: 42), radius: r))
    #expect(!BotPrint.printing(percent: 42).looksLike(.printing(percent: 45), radius: r))
    #expect(!BotPrint.printing(percent: 42).looksLike(.attention(percent: 42), radius: r))
    #expect(!BotPrint.printing(percent: 100).looksLike(.done, radius: r))
    #expect(!BotPrint.none.looksLike(.printing(percent: 0), radius: r))
    #expect(BotPrint.done.looksLike(.done, radius: r))
}

@Test func attentionOrANewPrintEndsDoneEarly() {
    for next in [printer("attention", 100, event: finished(7)), printer("heating", 0, event: finished(7))] {
        var t = BotPrintTracker()
        #expect(t.update(state: printer("finished", 100, event: finished(7)), agentState: "idle", enabled: true, now: t0) == .done)
        let shown = t.update(state: next, agentState: "idle", enabled: true, now: t0.addingTimeInterval(10))
        #expect(shown == (next.phase == "attention" ? .attention(percent: 100) : .starting))
        #expect(t.doneEndsAt == nil)
        #expect(t.update(state: printer("finished", 100, event: finished(7)), agentState: "idle", enabled: true, now: t0.addingTimeInterval(20)) == BotPrint.none)
    }
}

@Test func theLevelSpansTheWholeInnerBall() {
    let style = BotStyle.menuBar(tint: CGColor(gray: 1, alpha: 1))
    func orangeRows(_ percent: Int) -> Int {
        var pose = BotPose()
        pose.fill = BotPrint.printing(percent: percent).fill
        let px = render(pose, style: style)
        return (0..<44).filter { y in (14...30).contains { x in isOrange(px(x, y)) } }.count
    }
    #expect(orangeRows(0) == 0)
    #expect(orangeRows(4) > 0, "4 % already shows a level")
    #expect(orangeRows(95) < orangeRows(100))
}

@Test func doneOnAnIdleBotIsNoMoodMorphAndDelaysSleep() {
    var b = BotBehavior(seed: 10, now: 0)
    _ = frames(&b, from: 0, seconds: 200)
    b.setPrint(.done, at: 200)
    _ = b.pose(at: 200.05)
    #expect(!b.isTransitioning)
    b.setPrint(.none, at: 260)
    _ = frames(&b, from: 260, seconds: 200)
    #expect(b.mood == .idle, "sleep counts from the done")
    _ = frames(&b, from: 460, seconds: 50)
    #expect(b.mood == .sleepy)
}

@Test func aMissingPrinterStateHoldsAPlayingDone() {
    var t = BotPrintTracker()
    #expect(t.update(state: printer("finished", 100, event: finished(7)), agentState: "idle", enabled: true, now: t0) == .done)
    #expect(t.update(state: nil, agentState: "idle", enabled: true, now: t0.addingTimeInterval(20)) == .done)
    #expect(t.update(state: printer("off", enabled: false), agentState: "idle", enabled: true, now: t0.addingTimeInterval(30)) == .done)
    #expect(t.update(state: nil, agentState: "idle", enabled: true, now: t0.addingTimeInterval(61)) == BotPrint.none)
    #expect(t.doneEndsAt == nil)
    var off = BotPrintTracker()
    _ = off.update(state: printer("finished", 100, event: finished(7)), agentState: "idle", enabled: true, now: t0)
    #expect(off.update(state: nil, agentState: "idle", enabled: false, now: t0.addingTimeInterval(5)) == BotPrint.none, "the setting hides it at once")
}

@Test func thePlayedDoneIDSurvivesARestart() throws {
    let suite = "BotPrintDoneStoreTests-\(UUID().uuidString)"
    let defaults = try #require(UserDefaults(suiteName: suite))
    defer { defaults.removePersistentDomain(forName: suite) }
    let store = BotPrintDoneStore(defaults: defaults)
    #expect(store.load() == nil)
    var first = BotPrintTracker(playedDoneID: store.load())
    let s = printer("finished", 100, event: finished(1_800_000_000_123))
    #expect(first.update(state: s, agentState: "idle", enabled: true, now: t0) == .done)
    store.save(try #require(first.playedDoneID))
    var restarted = BotPrintTracker(playedDoneID: BotPrintDoneStore(defaults: defaults).load())
    #expect(restarted.update(state: s, agentState: "idle", enabled: true, now: t0.addingTimeInterval(120)) == BotPrint.none)
}

@Test func aShownPrintAddsNoFrameLoopWakes() {
    for seed in UInt64(0)..<10 {
        for shown in [BotPrint.printing(percent: 42), .attention(percent: 42), .starting] {
            var plain = BotBehavior(seed: seed, now: 0)
            var printing = BotBehavior(seed: seed, now: 0)
            printing.setPrint(shown, at: 0)
            for t in stride(from: 0.0, to: 120, by: 1.0 / 24) {
                _ = plain.pose(at: t)
                _ = printing.pose(at: t)
                #expect(plain.isAnimating == printing.isAnimating && plain.nextEventAt == printing.nextEventAt
                        && plain.isTransitioning == printing.isTransitioning, "seed \(seed) \(shown) t \(t)")
                if plain.isAnimating != printing.isAnimating { break }
            }
        }
    }
}
