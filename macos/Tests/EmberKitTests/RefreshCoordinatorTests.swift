import Testing
import Foundation
@testable import EmberKit

/// Records fetches and answers each feed with a scripted result.
@MainActor
private final class FakeFeeds {
    var calls: [Feed] = []
    var results: [Feed: FeedTick] = [:]

    func count(_ feed: Feed) -> Int { calls.filter { $0 == feed }.count }

    func fetch(_ feed: Feed) async -> FeedTick {
        calls.append(feed)
        return results[feed] ?? .ok
    }
}

@MainActor
private func makeCoordinator() -> (RefreshCoordinator, ManualClock, FakeFeeds) {
    let clock = ManualClock()
    let feeds = FakeFeeds()
    let c = RefreshCoordinator(fetch: { await feeds.fetch($0) }, sleep: clock.sleepFn, now: clock.nowFn)
    return (c, clock, feeds)
}

@MainActor @Test func startPollsTiersAAndBImmediatelyButNotC() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    await clock.settle()
    #expect(Set(feeds.calls) == Feed.alwaysOn)
    #expect(feeds.count(.screen) == 0)
    #expect(c.activeFeeds == Feed.alwaysOn)
    c.stop()
}

@MainActor @Test func tierAPollsEvery3sAndTierBEvery60s() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    await clock.advance(by: .seconds(59))
    #expect(feeds.count(.state) == 20)          // t = 0, 3, …, 57
    #expect(feeds.count(.stats) == 1)
    await clock.advance(by: .seconds(1))
    #expect(feeds.count(.stats) == 2)
    #expect(feeds.count(.meetings) == 2)
    c.stop()
}

@MainActor @Test func holdingATierCFeedStartsItAndReleasingStopsIt() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    c.hold([.screen])
    await clock.advance(by: .milliseconds(2500))
    #expect(feeds.count(.screen) == 3)          // t = 0, 1, 2
    c.release([.screen])
    #expect(!c.isActive(.screen))
    await clock.advance(by: .seconds(10))
    #expect(feeds.count(.screen) == 3)
    c.stop()
}

@MainActor @Test func holdsAreRefcounted() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    c.hold([.clockHealth])
    c.hold([.clockHealth, .clockHealth])      // a duplicate in one call counts once
    #expect(c.holdCount(.clockHealth) == 2)
    c.release([.clockHealth])
    #expect(c.isActive(.clockHealth))
    await clock.advance(by: .seconds(16))
    #expect(feeds.count(.clockHealth) == 2)     // t = 0, 15
    c.release([.clockHealth])
    #expect(!c.isActive(.clockHealth))
    #expect(c.holdCount(.clockHealth) == 0)
    c.stop()
}

@MainActor @Test func holdingATierBFeedSpeedsItUpAndReleasingSlowsItDown() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    await clock.advance(by: .seconds(10))
    #expect(feeds.count(.stats) == 1)
    #expect(c.cadence(for: .stats) == .seconds(60))

    c.hold([.stats])
    #expect(c.cadence(for: .stats) == .seconds(30))
    // The loop slept for 60 s at t = 0; the hold wakes it onto 30 s.
    await clock.advance(by: .seconds(21))       // t = 31
    #expect(feeds.count(.stats) == 2)           // fetched at t = 30

    c.release([.stats])
    await clock.advance(by: .seconds(58))       // t = 89: next is t = 90
    #expect(feeds.count(.stats) == 2)
    await clock.advance(by: .seconds(1))
    #expect(feeds.count(.stats) == 3)
    c.stop()
}

@MainActor @Test func tierABacksOffAfterThreeAndTenFailures() async {
    let (c, clock, feeds) = makeCoordinator()
    feeds.results[.state] = .failed(.offline)
    c.start()
    await clock.advance(by: .seconds(7))        // t = 0, 3, 6
    #expect(feeds.count(.state) == 3)
    #expect(c.interval(for: .state) == .seconds(15))
    await clock.advance(by: .seconds(13))       // t = 20; t = 21 is next
    #expect(feeds.count(.state) == 3)
    await clock.advance(by: .seconds(1))
    #expect(feeds.count(.state) == 4)

    await clock.advance(by: .seconds(15 * 6))   // failures 5…10
    #expect(c.consecutiveFailures(.state) == 10)
    #expect(c.interval(for: .state) == .seconds(60))

    feeds.results[.state] = .ok
    await clock.advance(by: .seconds(60))
    #expect(c.consecutiveFailures(.state) == 0)
    #expect(c.interval(for: .state) == .seconds(3))
    c.stop()
}

@MainActor @Test func tierBDoesNotUseTheFailureLadder() {
    var p = FeedPacing()
    for _ in 0..<12 { _ = p.record(.failed(.offline), tier: .b) }
    #expect(p.consecutiveFailures == 12)
    #expect(p.record(.failed(.offline), tier: .b) == .zero)
}

@MainActor @Test func rateLimitWaitsAtLeastRetryAfterAndDoubles() {
    var p = FeedPacing()
    #expect(p.record(.failed(.rateLimited, retryAfter: .seconds(4)), tier: .a) == .seconds(4))
    #expect(p.record(.failed(.rateLimited, retryAfter: .seconds(4)), tier: .a) == .seconds(8))
    // A 429 doesn't count as the server failing.
    #expect(p.consecutiveFailures == 0)
    #expect(p.record(.ok, tier: .a) == .zero)
    #expect(p.record(.failed(.rateLimited, retryAfter: .seconds(2)), tier: .a) == .seconds(2))
}

@MainActor @Test func featureOffFailuresAreRecordedLikeOthers() async {
    let (c, clock, feeds) = makeCoordinator()
    feeds.results[.pomodoroState] = .failed(.featureOff)
    c.start()
    await clock.advance(by: .seconds(7))
    #expect(c.consecutiveFailures(.pomodoroState) == 3)
    #expect(c.interval(for: .pomodoroState) == .seconds(15))
    c.stop()
}

@MainActor @Test func nextDelayHintStretchesTheInterval() async {
    let (c, clock, feeds) = makeCoordinator()
    feeds.results[.screen] = FeedTick(error: .offline, nextDelay: .seconds(3))
    c.start()
    c.hold([.screen])
    await clock.advance(by: .milliseconds(6500))
    #expect(feeds.count(.screen) == 3)          // t = 0, 3, 6
    c.stop()
}

@MainActor @Test func pauseStopsEveryLoopAndResumeFetchesImmediately() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    c.hold([.screen])
    await clock.advance(by: .milliseconds(500))
    let before = feeds.calls.count
    c.pause()
    #expect(c.isPaused)
    await clock.advance(by: .seconds(600))
    #expect(feeds.calls.count == before)

    c.resume()
    await clock.settle()
    let resumed = Set(feeds.calls.dropFirst(before))
    #expect(resumed == Feed.alwaysOn.union([.screen]))
    #expect(c.holdCount(.screen) == 1)
    c.stop()
}

@MainActor @Test func refreshNowFetchesAndPushesTheNextPollBack() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    await clock.advance(by: .seconds(40))
    #expect(feeds.count(.stats) == 1)
    await c.refreshNow([.stats])
    #expect(feeds.count(.stats) == 2)
    await clock.advance(by: .seconds(30))       // t = 70: the t = 60 poll moved to t = 100
    #expect(feeds.count(.stats) == 2)
    await clock.advance(by: .seconds(30))
    #expect(feeds.count(.stats) == 3)
    c.stop()
}

@MainActor @Test func refreshNowSkipsFreshFeedsAndUnheldTierC() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    await clock.advance(by: .seconds(10))
    await c.refreshNow([.stats, .meetings, .screen], ifOlderThan: .seconds(15))
    #expect(feeds.count(.stats) == 1)
    #expect(feeds.count(.screen) == 0)
    await clock.advance(by: .seconds(6))
    await c.refreshNow([.stats], ifOlderThan: .seconds(15))
    #expect(feeds.count(.stats) == 2)
    c.stop()
}

@MainActor @Test func refreshNowWithNoFeedsFetchesEveryActiveOne() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    c.hold([.heatmap])
    await clock.settle()
    feeds.calls.removeAll()
    await c.refreshNow()
    #expect(Set(feeds.calls) == Feed.alwaysOn.union([.heatmap]))
    c.stop()
}

@MainActor @Test func restartForgetsBackoff() async {
    let (c, clock, feeds) = makeCoordinator()
    feeds.results[.state] = .failed(.offline)
    c.start()
    await clock.advance(by: .seconds(7))
    #expect(c.interval(for: .state) == .seconds(15))
    feeds.results[.state] = .ok
    c.restart()
    await clock.settle()
    #expect(c.interval(for: .state) == .seconds(3))
    #expect(feeds.count(.state) == 4)
    c.stop()
}

@MainActor @Test func cadencesMatchTheSpec() {
    #expect(Feed.state.baseCadence == .seconds(3))
    #expect(Feed.pomodoroState.tier == .a)
    #expect(Feed.stats.baseCadence == .seconds(60))
    #expect(Feed.stats.heldCadence == .seconds(30))
    #expect(Feed.usage.heldCadence == .seconds(30))
    #expect(Feed.meetings.heldCadence == .seconds(60))
    #expect(Feed.screen.heldCadence == .seconds(1))
    #expect(Feed.clockHealth.heldCadence == .seconds(15))
    for f in [Feed.weather, .activity, .workhours, .heatmap] {
        #expect(f.tier == .c)
        #expect(f.heldCadence == .seconds(300))
    }
    #expect(Feed.alwaysOn == [.state, .pomodoroState, .stats, .usage, .meetings, .apps])
}

@MainActor @Test func concurrentRequestsForAFeedShareOneFetch() async {
    let clock = ManualClock()
    let feeds = FakeFeeds()
    let c = RefreshCoordinator(fetch: { feed in
        let r = await feeds.fetch(feed)
        try? await clock.sleep(.seconds(1))
        return r
    }, sleep: clock.sleepFn, now: clock.nowFn)
    let a = Task { await c.refreshNow([.stats]) }
    let b = Task { await c.refreshNow([.stats]) }
    await clock.settle()
    await clock.advance(by: .seconds(1))
    await a.value
    await b.value
    #expect(feeds.count(.stats) == 1)
}
