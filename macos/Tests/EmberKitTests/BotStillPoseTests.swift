import Testing
@testable import EmberKit

@Test func stillShowsTheNewMoodsSettledFaceAtOnce() {
    var bot = BotBehavior(seed: 3, now: 0)
    _ = bot.pose(at: 5)
    _ = bot.setMood(.waiting, at: 5)
    let still = bot.still
    #expect(still.mood == .waiting && still.eyes == .round && still.badge == 1)
    #expect(bot.pose(at: 5.01).eyes != .round || bot.pose(at: 5.01).badge < 1)

    _ = bot.setMood(.error, at: 10)
    #expect(bot.still.eyes == .angry && bot.still.triangle == 1 && bot.still.badge == 0)
}

@Test func stillStaysPutWhileTheMoodChangeAnimates() {
    var bot = BotBehavior(seed: 5, now: 0)
    _ = bot.setMood(.working, at: 1)
    let first = bot.still
    var t = 1.0
    while t < 3 {
        _ = bot.pose(at: t)
        #expect(bot.still == first)
        t += 1.0 / 60
    }
}

@Test func stillFollowsTheBotFallingAsleep() {
    var bot = BotBehavior(seed: 7, now: 0)
    #expect(bot.still.mood == .idle)
    _ = bot.pose(at: BotBehavior.Tuning.mac.sleepAfter + 1)
    #expect(bot.still.mood == .sleepy && bot.still.slump == 1)
    #expect(bot.still.lidLeft == BotBehavior.slumpLid && bot.still.lidRight == BotBehavior.slumpLid)
}

@Test func stillShowsAFinishedPrintsHappyEyes() {
    var bot = BotBehavior(seed: 9, now: 0)
    _ = bot.setMood(.working, at: 0)
    bot.setPrint(.done, at: 1)
    #expect(bot.still.eyes == .happy)
    #expect(bot.still.fill == BotPrint.done.fill && bot.still.printBadge == (BotPrint.done.isAttention ? 1 : 0))
}

@Test func stillLooksAtTheViewerWithNoPassingMotion() {
    var bot = BotBehavior(seed: 11, now: 0)
    _ = bot.setMood(.done, at: 0)
    _ = bot.pose(at: 0.2)
    let still = bot.still
    #expect(still.gazeX == BotBehavior.viewer.x && still.gazeY == BotBehavior.viewer.y)
    #expect(still.blinkLeft == 0 && still.blinkRight == 0 && !still.hide)
    #expect(still.eyeDY == 0 && still.scaleX == 1 && still.scaleY == 1 && still.offsetX == 0 && still.offsetY == 0)
    #expect(still.sparkle == 0)
}
