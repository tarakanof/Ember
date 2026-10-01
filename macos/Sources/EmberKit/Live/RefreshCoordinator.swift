import Foundation

struct FeedTick: Equatable, Sendable {
    var error: FeedError?
    var retryAfter: Duration?
    var nextDelay: Duration?

    static let ok = FeedTick()

    static func failed(_ error: FeedError, retryAfter: Duration? = nil) -> FeedTick {
        FeedTick(error: error, retryAfter: retryAfter)
    }
}

struct FeedPacing: Sendable {
    private(set) var consecutiveFailures = 0
    private var limiter = RateLimitBackoff(base: .zero)

    static func ladder(failures: Int) -> Duration {
        if failures >= 10 { return .seconds(60) }
        if failures >= 3 { return .seconds(15) }
        return .zero
    }

    mutating func record(_ tick: FeedTick, tier: Feed.Tier) -> Duration {
        var floor = tick.nextDelay ?? .zero
        if tick.error == .rateLimited {
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

@MainActor
final class RefreshCoordinator {
    typealias Fetch = @MainActor (Feed) async -> FeedTick
    typealias Sleep = @Sendable (Duration) async throws -> Void
    typealias Now = @MainActor () -> Duration

    private let fetch: Fetch
    private let sleep: Sleep
    private let now: Now

    private(set) var isStarted = false
    private(set) var isPaused = false
    private var holds: [Feed: Int] = [:]
    private var loops: [Feed: Task<Void, Never>] = [:]
    private var loopIDs: [Feed: Int] = [:]
    private var sleepingLoops: Set<Int> = []
    private var nextLoopID = 0
    private var pacing: [Feed: FeedPacing] = [:]
    private var floors: [Feed: Duration] = [:]
    private var lastTick: [Feed: Duration] = [:]
    private var inFlight: [Feed: (id: Int, task: Task<FeedTick, Never>)] = [:]
    private var nextFetchID = 0

    init(fetch: @escaping Fetch, sleep: @escaping Sleep, now: @escaping Now) {
        self.fetch = fetch
        self.sleep = sleep
        self.now = now
    }

    static func live(fetch: @escaping Fetch) -> RefreshCoordinator {
        let origin = ContinuousClock.now
        return RefreshCoordinator(
            fetch: fetch,
            sleep: { try await Task.sleep(for: $0) },
            now: { ContinuousClock.now - origin })
    }

    // MARK: Introspection

    func holdCount(_ feed: Feed) -> Int { holds[feed, default: 0] }

    func isActive(_ feed: Feed) -> Bool {
        isStarted && (feed.tier != .c || holdCount(feed) > 0)
    }

    var activeFeeds: Set<Feed> { Set(Feed.allCases.filter(isActive)) }

    func cadence(for feed: Feed) -> Duration {
        holdCount(feed) > 0 ? min(feed.baseCadence, feed.heldCadence) : feed.baseCadence
    }

    func interval(for feed: Feed) -> Duration {
        max(cadence(for: feed), floors[feed] ?? .zero)
    }

    func consecutiveFailures(_ feed: Feed) -> Int { pacing[feed]?.consecutiveFailures ?? 0 }

    func lastTickAt(_ feed: Feed) -> Duration? { lastTick[feed] }

    // MARK: Lifecycle

    func start() {
        guard !isStarted else { return }
        isStarted = true
        reconcileAll()
    }

    func stop() {
        isStarted = false
        cancelAll()
    }

    func pause() {
        guard !isPaused else { return }
        isPaused = true
        cancelAll()
    }

    func resume() {
        guard isPaused else { return }
        isPaused = false
        lastTick.removeAll()
        reconcileAll()
    }

    func restart() {
        cancelAll()
        inFlight.removeAll()
        lastTick.removeAll()
        pacing.removeAll()
        floors.removeAll()
        reconcileAll()
    }

    func forgetInFlight() { inFlight.removeAll() }

    // MARK: Holds

    func hold(_ feeds: [Feed]) {
        let set = Set(feeds)
        for f in set { holds[f, default: 0] += 1 }
        for f in set { reconcile(f) }
    }

    func release(_ feeds: [Feed]) {
        let set = Set(feeds)
        for f in set {
            let n = holds[f, default: 0] - 1
            holds[f] = n > 0 ? n : nil
        }
        for f in set { reconcile(f) }
    }

    // MARK: Fetching

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
        if let running = inFlight[feed] { return await running.task.value }
        nextFetchID += 1
        let id = nextFetchID
        let task = Task { () -> FeedTick in
            let result = await self.fetch(feed)
            guard self.inFlight[feed]?.id == id else { return result }
            self.inFlight[feed] = nil
            self.lastTick[feed] = self.now()
            self.floors[feed] = self.pacing[feed, default: FeedPacing()].record(result, tier: feed.tier)
            return result
        }
        inFlight[feed] = (id, task)
        return await task.value
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
            if remaining(feed) <= .zero { await Task.yield() }
        }
    }

    private func cancelAll() {
        for loop in loops.values { loop.cancel() }
        loops.removeAll()
        loopIDs.removeAll()
    }
}
