import Foundation

/// What one fetch of a feed told the scheduler.
struct FeedTick: Equatable, Sendable {
    /// nil when the fetch succeeded.
    var error: FeedError?
    /// The server's `Retry-After` on a 429.
    var retryAfter: Duration?
    /// A feed-specific minimum wait before the next fetch (the mirror's own
    /// pacing), on top of the cadence.
    var nextDelay: Duration?

    static let ok = FeedTick()

    static func failed(_ error: FeedError, retryAfter: Duration? = nil) -> FeedTick {
        FeedTick(error: error, retryAfter: retryAfter)
    }
}

/// One feed's backoff state: the failure ladder and the rate-limit pacer.
struct FeedPacing: Sendable {
    private(set) var consecutiveFailures = 0
    private var limiter = RateLimitBackoff(base: .zero)

    /// Tier A slows to 15 s after 3 failures in a row and to 60 s after 10, so
    /// an unreachable server costs a request a minute instead of 20.
    static func ladder(failures: Int) -> Duration {
        if failures >= 10 { return .seconds(60) }
        if failures >= 3 { return .seconds(15) }
        return .zero
    }

    /// Records a fetch and returns the minimum wait before the next one,
    /// independent of the feed's cadence.
    mutating func record(_ tick: FeedTick, tier: Feed.Tier) -> Duration {
        var floor = tick.nextDelay ?? .zero
        if tick.error == .rateLimited {
            // A 429 says nothing about the server's health, so the failure
            // ladder is left alone; the pacer doubles while denials continue.
            let wait = limiter.nextDelay(after: .rateLimited(
                retryAfter: tick.retryAfter ?? RateLimitBackoff.fallbackRetryAfter))
            return max(floor, wait)
        }
        _ = limiter.nextDelay(after: .succeeded)
        guard tick.error != nil else {
            consecutiveFailures = 0
            return floor
        }
        consecutiveFailures += 1
        if tier == .a { floor = max(floor, Self.ladder(failures: consecutiveFailures)) }
        return floor
    }
}

/// Runs one polling loop per active feed. Tiers A and B run from `start()`;
/// tier C runs only while held. A feed's interval is its cadence (faster while
/// held) or its backoff, whichever is longer. No SwiftUI, and the clock is
/// injected, so the scheduling is unit-tested.
///
/// A loop re-reads its wait after every sleep, so a `refreshNow` pushes the
/// next poll back, and a hold that speeds a feed up wakes its sleeping loop.
@MainActor
final class RefreshCoordinator {
    /// Fetches a feed, applies the result to the model, reports the outcome.
    typealias Fetch = @MainActor (Feed) async -> FeedTick
    typealias Sleep = @Sendable (Duration) async throws -> Void
    /// Monotonic time since an arbitrary origin.
    typealias Now = @MainActor () -> Duration

    private let fetch: Fetch
    private let sleep: Sleep
    private let now: Now

    private(set) var isStarted = false
    private(set) var isPaused = false
    private var holds: [Feed: Int] = [:]
    private var loops: [Feed: Task<Void, Never>] = [:]
    /// Which loop currently owns each feed, so a cancelled loop that is still
    /// winding down can't clobber its replacement's state.
    private var loopIDs: [Feed: Int] = [:]
    private var sleepingLoops: Set<Int> = []
    private var nextLoopID = 0
    private var pacing: [Feed: FeedPacing] = [:]
    private var floors: [Feed: Duration] = [:]
    private var lastTick: [Feed: Duration] = [:]

    init(fetch: @escaping Fetch, sleep: @escaping Sleep, now: @escaping Now) {
        self.fetch = fetch
        self.sleep = sleep
        self.now = now
    }

    /// A coordinator on the real continuous clock.
    static func live(fetch: @escaping Fetch) -> RefreshCoordinator {
        let origin = ContinuousClock.now
        return RefreshCoordinator(
            fetch: fetch,
            sleep: { try await Task.sleep(for: $0) },
            now: { ContinuousClock.now - origin })
    }

    // MARK: Introspection

    func holdCount(_ feed: Feed) -> Int { holds[feed, default: 0] }

    /// Polling, or would be if not paused.
    func isActive(_ feed: Feed) -> Bool {
        isStarted && (feed.tier != .c || holdCount(feed) > 0)
    }

    var activeFeeds: Set<Feed> { Set(Feed.allCases.filter(isActive)) }

    /// The feed's cadence right now: the held cadence while anything holds it.
    func cadence(for feed: Feed) -> Duration {
        holdCount(feed) > 0 ? min(feed.baseCadence, feed.heldCadence) : feed.baseCadence
    }

    /// Time between polls: cadence or backoff, whichever is longer.
    func interval(for feed: Feed) -> Duration {
        max(cadence(for: feed), floors[feed] ?? .zero)
    }

    func consecutiveFailures(_ feed: Feed) -> Int { pacing[feed]?.consecutiveFailures ?? 0 }

    /// When the feed last finished a fetch, on the injected clock.
    func lastTickAt(_ feed: Feed) -> Duration? { lastTick[feed] }

    // MARK: Lifecycle

    /// Starts tiers A and B. Idempotent.
    func start() {
        guard !isStarted else { return }
        isStarted = true
        reconcileAll()
    }

    /// Stops every loop; holds are kept.
    func stop() {
        isStarted = false
        cancelAll()
    }

    /// Stops polling without forgetting holds (system sleep).
    func pause() {
        guard !isPaused else { return }
        isPaused = true
        cancelAll()
    }

    /// Resumes after `pause()` with an immediate fetch of every active feed.
    func resume() {
        guard isPaused else { return }
        isPaused = false
        lastTick.removeAll()
        reconcileAll()
    }

    /// Forgets timing and backoff and refetches every active feed now (a new
    /// server).
    func restart() {
        cancelAll()
        lastTick.removeAll()
        pacing.removeAll()
        floors.removeAll()
        reconcileAll()
    }

    // MARK: Holds

    /// Adds one hold on each feed (duplicates count once).
    func hold(_ feeds: [Feed]) {
        let set = Set(feeds)
        for f in set { holds[f, default: 0] += 1 }
        for f in set { reconcile(f) }
    }

    /// Drops one hold on each feed; a tier C feed with no holds stops.
    func release(_ feeds: [Feed]) {
        let set = Set(feeds)
        for f in set {
            let n = holds[f, default: 0] - 1
            holds[f] = n > 0 ? n : nil
        }
        for f in set { reconcile(f) }
    }

    // MARK: Fetching

    /// Fetches the feeds now, concurrently, and returns when all are done. No
    /// feeds means every active one. `ifOlderThan` skips feeds fetched more
    /// recently than that. An unheld tier C feed is skipped: nobody shows it.
    func refreshNow(_ feeds: [Feed] = [], ifOlderThan age: Duration? = nil) async {
        let candidates = feeds.isEmpty ? activeFeeds : Set(feeds)
        let due = candidates.filter { f in
            if f.tier == .c && holdCount(f) == 0 { return false }
            guard let age, let t = lastTick[f] else { return true }
            return now() - t >= age
        }
        let running = due.map { f in Task { await self.tick(f) } }
        for task in running { _ = await task.value }
    }

    @discardableResult
    func tick(_ feed: Feed) async -> FeedTick {
        let result = await fetch(feed)
        // A cancelled fetch (hold released, sleep) says nothing about the server.
        guard !Task.isCancelled else { return result }
        lastTick[feed] = now()
        floors[feed] = pacing[feed, default: FeedPacing()].record(result, tier: feed.tier)
        return result
    }

    // MARK: Loops

    private func remaining(_ feed: Feed) -> Duration {
        guard let t = lastTick[feed] else { return .zero }
        return interval(for: feed) - (now() - t)
    }

    private func reconcileAll() {
        for f in Feed.allCases { reconcile(f) }
    }

    private func reconcile(_ feed: Feed) {
        guard isActive(feed), !isPaused else {
            loops[feed]?.cancel()
            loops[feed] = nil
            loopIDs[feed] = nil
            return
        }
        if let loop = loops[feed] {
            // A loop mid-fetch re-reads its wait when the fetch ends; only a
            // sleeping one has to be woken to pick up a new cadence.
            guard let id = loopIDs[feed], sleepingLoops.contains(id) else { return }
            loop.cancel()
        }
        startLoop(feed)
    }

    private func startLoop(_ feed: Feed) {
        nextLoopID += 1
        let id = nextLoopID
        loopIDs[feed] = id
        loops[feed] = Task { [weak self] in await self?.runLoop(feed, id: id) }
    }

    private func runLoop(_ feed: Feed, id: Int) async {
        while !Task.isCancelled {
            let wait = remaining(feed)
            if wait > .zero {
                sleepingLoops.insert(id)
                defer { sleepingLoops.remove(id) }
                do { try await sleep(wait) } catch { return }
                continue
            }
            await tick(feed)
        }
    }

    private func cancelAll() {
        for loop in loops.values { loop.cancel() }
        loops.removeAll()
        loopIDs.removeAll()
    }
}
