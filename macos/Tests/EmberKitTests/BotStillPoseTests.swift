import Testing
@testable import EmberKit

@Test func stillPoseDropsPassingMotionAndKeepsTheMood() {
    var live = BotPose()
    live.mood = .error; live.eyes = .angry
    live.triangle = 1; live.slump = 0.5; live.badge = 1
    live.fill = 0.4; live.printBadge = 1
    live.gazeX = 0.1; live.gazeY = 0.95
    live.lidLeft = 0.9; live.lidRight = 0.2; live.blinkLeft = 0.8; live.blinkRight = 0.1; live.hide = true
    live.eyeDY = -0.2; live.scaleX = 1.2; live.scaleY = 0.8; live.offsetX = 0.3; live.offsetY = -0.1
    live.sparkle = 0.5

    let still = live.still

    #expect(still.mood == .error && still.eyes == .angry)
    #expect(still.triangle == 1 && still.slump == 0.5 && still.badge == 1)
    #expect(still.fill == 0.4 && still.printBadge == 1)
    #expect(still.gazeX == BotBehavior.rest.x && still.gazeY == BotBehavior.rest.y)
    #expect(still.lidLeft == 0.5 * BotBehavior.slumpLid && still.lidRight == still.lidLeft)
    #expect(still.blinkLeft == 0 && still.blinkRight == 0 && !still.hide)
    #expect(still.eyeDY == 0 && still.scaleX == 1 && still.scaleY == 1 && still.offsetX == 0 && still.offsetY == 0)
    #expect(still.sparkle == 0)
}

@Test func stillPoseOfARestingBotIsUnchanged() {
    var bot = BotBehavior(seed: 3, now: 0)
    _ = bot.setMood(.waiting, at: 0)
    let settled = bot.pose(at: 5).still
    #expect(settled.still == settled)
}
