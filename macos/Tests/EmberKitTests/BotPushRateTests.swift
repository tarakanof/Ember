import Testing
import CoreGraphics
@testable import EmberKit

private let menuBarRadius = BotStyle.menuBar(tint: CGColor(gray: 0, alpha: 1)).bodyRadius(side: 44)

private func menuBarPushesPerMinute(_ mood: BotMood, seed: UInt64, minutes: Double) -> Double {
    var bot = BotBehavior(seed: seed, now: 0)
    bot.setMood(mood, at: 0)
    var shown: BotPose?
    var pushes = 0
    var t = 0.0
    while t < minutes * 60 {
        var key = bot.pose(at: t).quantized(toPixels: menuBarRadius)
        key.badge = 0
        if key != shown {
            shown = key
            pushes += 1
        }
        t += bot.isTransitioning ? 1.0 / 60
            : bot.isAnimating ? 1.0 / 24
            : min(max(bot.nextEventAt - t, 1.0 / 24), 10)
    }
    return Double(pushes) / minutes
}

private let pushBudgetPerMinute: [BotMood: (budget: Double, measured: Double)] = [
    .idle: (680, 224.4),
    .working: (580, 190.1),
    .waiting: (680, 225.8),
    .done: (550, 180.9),
    .error: (990, 327.7),
]

@Test(arguments: [BotMood.idle, .working, .waiting, .done, .error])
func menuBarPushRateStaysInBudget(_ mood: BotMood) throws {
    let budget = try #require(pushBudgetPerMinute[mood]).budget
    let worst = (1...5).map { menuBarPushesPerMinute(mood, seed: $0, minutes: 10) }.max() ?? 0
    #expect(worst <= budget, "\(mood): \(worst) menu-bar pushes per minute, budget \(budget)")
}
