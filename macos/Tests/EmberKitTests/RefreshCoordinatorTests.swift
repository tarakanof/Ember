import Testing
import Foundation
@testable import EmberKit

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
    let c = RefreshCoordinator(fetch: { feed, _ in await feeds.fetch(feed) }, sleep: clock.sleepFn, now: clock.nowFn)
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
    #expect(feeds.count(.state) == 20)
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
    #expect(feeds.count(.screen) == 3)
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
    c.hold([.clockHealth, .clockHealth])
    #expect(c.holdCount(.clockHealth) == 2)
    c.release([.clockHealth])
    #expect(c.isActive(.clockHealth))
    await clock.advance(by: .seconds(16))
    #expect(feeds.count(.clockHealth) == 2)
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
    await clock.advance(by: .seconds(21))
    #expect(feeds.count(.stats) == 2)

    c.release([.stats])
    await clock.advance(by: .seconds(58))
    #expect(feeds.count(.stats) == 2)
    await clock.advance(by: .seconds(1))
    #expect(feeds.count(.stats) == 3)
    c.stop()
}

@MainActor @Test func tierABacksOffAfterThreeAndTenFailures() async {
    let (c, clock, feeds) = makeCoordinator()
    feeds.results[.state] = .failed(.offline)
    c.start()
    await clock.advance(by: .seconds(7))
    #expect(feeds.count(.state) == 3)
    #expect(c.interval(for: .state) == .seconds(15))
    await clock.advance(by: .seconds(13))
    #expect(feeds.count(.state) == 3)
    await clock.advance(by: .seconds(1))
    #expect(feeds.count(.state) == 4)

    await clock.advance(by: .seconds(15 * 6))
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
    #expect(feeds.count(.screen) == 3)
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
    await clock.advance(by: .seconds(30))
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
    for f in [Feed.weather, .activity, .heatmap, .dayHistory, .dayWeeks] {
        #expect(f.tier == .c)
        #expect(f.heldCadence == .seconds(300))
    }
    #expect(Feed.dayState.tier == .c)
    #expect(Feed.dayState.heldCadence == .seconds(15))
    #expect(Feed.alwaysOn == [.state, .pomodoroState, .stats, .usage, .meetings, .apps])
}

@MainActor @Test func concurrentRequestsForAFeedShareOneFetch() async {
    let clock = ManualClock()
    let feeds = FakeFeeds()
    let c = RefreshCoordinator(fetch: { feed, _ in
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

@MainActor @Test func aJoinedFetchIsRecordedBeforeAnyCallerResumes() async {
    let clock = ManualClock()
    var gate: CheckedContinuation<Void, Never>?
    var calls = 0
    let c = RefreshCoordinator(
        fetch: { _, _ in
            calls += 1
            await withCheckedContinuation { gate = $0 }
            return .ok
        },
        sleep: clock.sleepFn, now: clock.nowFn)
    var seen: [Duration?] = []
    let first = Task { await c.tick(.stats); seen.append(c.lastTickAt(.stats)) }
    let second = Task { await c.tick(.stats); seen.append(c.lastTickAt(.stats)) }
    while gate == nil { await Task.yield() }
    for _ in 0..<5 { await Task.yield() }
    gate?.resume()
    _ = await (first.value, second.value)
    #expect(calls == 1)
    #expect(seen.count == 2)
    #expect(seen.allSatisfy { $0 != nil })
    let third = Task { await c.tick(.stats) }
    while calls < 2 { await Task.yield() }
    gate?.resume()
    _ = await third.value
    #expect(calls == 2)
}

@MainActor @Test func aForgottenFetchDoesNotRecordAgainstTheNewServer() async {
    let clock = ManualClock()
    var gate: CheckedContinuation<Void, Never>?
    let c = RefreshCoordinator(
        fetch: { _, _ in
            await withCheckedContinuation { gate = $0 }
            return .failed(.offline)
        },
        sleep: clock.sleepFn, now: clock.nowFn)
    let old = Task { await c.tick(.stats) }
    while gate == nil { await Task.yield() }
    c.forgetInFlight()
    gate?.resume()
    _ = await old.value
    #expect(c.lastTickAt(.stats) == nil)
    #expect(c.consecutiveFailures(.stats) == 0)
}

@MainActor @Test func agedRefreshWaitsOutARetryAfter() async {
    let (c, clock, feeds) = makeCoordinator()
    feeds.results[.usage] = .failed(.rateLimited, retryAfter: .seconds(300))
    await c.refreshNow([.usage], ifOlderThan: .seconds(60))
    #expect(feeds.count(.usage) == 1)
    feeds.results[.usage] = .ok
    await clock.advance(by: .seconds(61))
    await c.refreshNow([.usage], ifOlderThan: .seconds(60))
    #expect(feeds.count(.usage) == 1)
    await clock.advance(by: .seconds(240))
    await c.refreshNow([.usage], ifOlderThan: .seconds(60))
    #expect(feeds.count(.usage) == 2)
}

@MainActor @Test func agedRefreshSkipsAFreshFeedAndFetchesAStaleOne() async {
    let (c, clock, feeds) = makeCoordinator()
    await c.refreshNow([.usage], ifOlderThan: .seconds(60))
    await clock.advance(by: .seconds(59))
    await c.refreshNow([.usage], ifOlderThan: .seconds(60))
    #expect(feeds.count(.usage) == 1)
    await clock.advance(by: .seconds(1))
    await c.refreshNow([.usage], ifOlderThan: .seconds(60))
    #expect(feeds.count(.usage) == 2)
}

@MainActor @Test func overlappingAgedRefreshesMakeOneFetch() async {
    let (c, _, feeds) = makeCoordinator()
    async let first: Void = c.refreshNow([.usage], ifOlderThan: .seconds(60))
    async let second: Void = c.refreshNow([.usage], ifOlderThan: .seconds(60))
    _ = await (first, second)
    #expect(feeds.count(.usage) == 1)
}

@MainActor
private final class ParkingFeeds {
    let clock: ManualClock
    var plain = 0
    var parked = 0
    var cancelled = 0
    var result = FeedTick(serverWaits: true)

    init(clock: ManualClock) { self.clock = clock }

    func fetch(_ feed: Feed, mayWait: Bool) async -> FeedTick {
        guard feed == .state else { return .ok }
        guard mayWait else {
            plain += 1
            return result
        }
        parked += 1
        do { try await clock.sleep(.seconds(25)) } catch {
            cancelled += 1
            return .failed(.offline)
        }
        return result
    }
}

@MainActor
private func makeParkingCoordinator() -> (RefreshCoordinator, ManualClock, ParkingFeeds) {
    let clock = ManualClock()
    let feeds = ParkingFeeds(clock: clock)
    let c = RefreshCoordinator(fetch: { await feeds.fetch($0, mayWait: $1) }, sleep: clock.sleepFn, now: clock.nowFn)
    return (c, clock, feeds)
}

@MainActor @Test func aFeedTheServerHoldsParksAfterItsFirstPlainFetch() async {
    let (c, clock, feeds) = makeParkingCoordinator()
    c.start()
    await clock.settle()
    #expect(feeds.plain == 1)
    #expect(feeds.parked == 0)
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 1)
    await clock.advance(by: .seconds(25))
    #expect(feeds.parked == 1)
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 2)
    #expect(feeds.plain == 1)
    c.stop()
}

@MainActor @Test func aFailedParkedFetchFallsBackToPlainPollingAndTheLadder() async {
    let (c, clock, feeds) = makeParkingCoordinator()
    c.start()
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 1)
    feeds.result = .failed(.offline)
    await clock.advance(by: .seconds(25))
    #expect(c.interval(for: .state) == .seconds(3))
    await clock.advance(by: .seconds(3))
    #expect(feeds.plain == 2)
    #expect(feeds.parked == 1)
    await clock.advance(by: .seconds(3))
    #expect(c.consecutiveFailures(.state) == 3)
    #expect(c.interval(for: .state) == .seconds(15))
    feeds.result = FeedTick(serverWaits: true)
    await clock.advance(by: .seconds(15))
    #expect(c.consecutiveFailures(.state) == 0)
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 2)
    c.stop()
}

@MainActor @Test func refreshNowCancelsAParkedFetchInsteadOfJoiningIt() async {
    let (c, clock, feeds) = makeParkingCoordinator()
    c.start()
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 1)
    await c.refreshNow([.state])
    #expect(feeds.plain == 2)
    await clock.settle()
    #expect(feeds.cancelled == 1)
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 2)
    c.stop()
}

@MainActor @Test func pauseCancelsAParkedFetchAndResumeStartsPlain() async {
    let (c, clock, feeds) = makeParkingCoordinator()
    c.start()
    await clock.advance(by: RefreshCoordinator.longPollGap)
    #expect(feeds.parked == 1)
    c.pause()
    await clock.settle()
    #expect(feeds.cancelled == 1)
    c.resume()
    await clock.settle()
    #expect(feeds.plain == 2)
    #expect(feeds.parked == 1)
    c.stop()
}

@MainActor @Test func forgettingInFlightCancelsAParkedFetchAndForgetsLongPolling() async {
    let (c, clock, feeds) = makeParkingCoordinator()
    c.start()
    await clock.advance(by: RefreshCoordinator.longPollGap)
    c.forgetInFlight()
    await clock.settle()
    #expect(feeds.cancelled == 1)
    #expect(c.interval(for: .state) == .seconds(3))
    c.stop()
}

@MainActor @Test func theCountdownFeedRunsOnlyWhileHeld() async {
    let (c, clock, feeds) = makeCoordinator()
    c.start()
    await clock.settle()
    #expect(feeds.count(.pomodoroCountdown) == 0)
    c.hold([.pomodoroCountdown])
    await clock.settle()
    #expect(feeds.count(.pomodoroCountdown) == 1)
    await clock.advance(by: .seconds(3))
    #expect(feeds.count(.pomodoroCountdown) == 2)
    c.release([.pomodoroCountdown])
    await clock.advance(by: .seconds(30))
    #expect(feeds.count(.pomodoroCountdown) == 2)
    c.stop()
}
