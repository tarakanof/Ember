import Foundation
import Observation
import OSLog

/// The app's live view of the server: one `Loadable` per feed, kept fresh by a
/// `RefreshCoordinator`. The single source the menu, the Dashboard, the Dock
/// menu and the bot read. Replaces `AppModel`.
///
/// Tiers A and B poll from `start()`. A view that shows a feed holds it for
/// its lifetime with `.task { await live.track(.screen) }`, which starts tier C
/// feeds and speeds up tier B ones while the view is on screen.
@MainActor
@Observable
public final class LiveModel {
    /// `/state` misses in a row before the connection counts as offline and
    /// the snapshot as stale. Until then the last snapshot stays live.
    public static let offlineAfterFailures = 3

    public private(set) var connection: ConnectionHealth = .unconfigured
    public private(set) var snapshot: Loadable<Snapshot> = .loading
    public private(set) var pomodoro: Loadable<PomoState> = .loading
    public private(set) var stats: Loadable<PomoStats> = .loading
    public private(set) var usage: Loadable<UsageSnapshot> = .loading
    public private(set) var meetings: Loadable<MeetingsState> = .loading
    public private(set) var apps: Loadable<[AppToggle]> = .loading
    /// The clock's framebuffer: 24-bit RGB ints, row-major, 32 × 8.
    public private(set) var screen: Loadable<[Int]> = .loading
    public private(set) var clockHealth: Loadable<ClockHealth> = .loading
    public private(set) var weather: Loadable<WeatherState> = .loading
    public private(set) var activity: Loadable<ActivitySummary> = .loading
    public private(set) var workhours: Loadable<WorkHours> = .loading
    public private(set) var heatmap: Loadable<Heatmap> = .loading

    /// The session the menu-bar icon and bot show. nil while the snapshot is
    /// stale: an old "running" must not keep the bot working through an outage.
    public var winningSession: Session? {
        guard case .loaded(let snap, _) = snapshot else { return nil }
        return pickWinning(snap.sessions)
    }

    /// Sessions from the latest snapshot, live or stale.
    public var sessions: [Session] { snapshot.value?.sessions ?? [] }

    @ObservationIgnored private var coordinator: RefreshCoordinator!
    @ObservationIgnored private let clock: @MainActor () -> Date
    @ObservationIgnored private var services: Services?
    /// Bumped by `configure`: a response from the previous server is dropped.
    @ObservationIgnored private var generation = 0
    /// Per-feed request counter: only the newest request's answer is applied,
    /// so a slow poll can't overwrite a newer `refreshNow`.
    @ObservationIgnored private var issued: [Feed: Int] = [:]
    @ObservationIgnored private var stateFailures = 0
    @ObservationIgnored private var firstFailureAt: Date?
    @ObservationIgnored private var mirror = MirrorPoller()
    /// The clock's own address, for reading the screen directly from servers
    /// that predate /v1/device/screen. "" once looked up and unavailable.
    @ObservationIgnored private var clockBaseURL: String?
    /// `start()` was called; a later `configure` with a URL starts polling.
    @ObservationIgnored private var wantsStart = false

    private static let log = Logger(subsystem: "com.ember.Ember", category: "live")

    private struct Services {
        let status: StatusService
        let pomodoro: PomodoroService
        let stats: StatsService
        let usage: UsageService
        let meetings: MeetingsService
        let apps: AppsService
        let device: DeviceService
        let health: HealthService
        let weather: WeatherService
        let activity: ActivityService

        init(client: APIClient) {
            status = StatusService(client: client)
            pomodoro = PomodoroService(client: client)
            stats = StatsService(client: client)
            usage = UsageService(client: client)
            meetings = MeetingsService(client: client)
            apps = AppsService(client: client)
            device = DeviceService(client: client)
            health = HealthService(client: client)
            weather = WeatherService(client: client)
            activity = ActivityService(client: client)
        }
    }

    /// A model on the real clock.
    public convenience init() {
        self.init(now: { Date() }, makeCoordinator: { RefreshCoordinator.live(fetch: $0) })
    }

    /// Tests inject the wall clock and a coordinator on a manual clock.
    init(now: @escaping @MainActor () -> Date,
         makeCoordinator: (@escaping RefreshCoordinator.Fetch) -> RefreshCoordinator) {
        clock = now
        coordinator = makeCoordinator { [weak self] feed in
            guard let self else { return .ok }
            return await self.fetch(feed)
        }
    }

    // MARK: Lifecycle

    /// Points every feed at a new server. Values from the old one are dropped
    /// (they describe another setup); a client without a URL leaves the model
    /// `.unconfigured` and idle.
    public func configure(client: APIClient) {
        generation += 1
        issued.removeAll()
        stateFailures = 0
        firstFailureAt = nil
        mirror = MirrorPoller()
        clockBaseURL = nil
        resetValues()
        guard client.baseURL != nil else {
            services = nil
            connection = .unconfigured
            coordinator.stop()
            return
        }
        services = Services(client: client)
        connection = .connecting
        guard wantsStart else { return }
        if coordinator.isStarted { coordinator.restart() } else { coordinator.start() }
    }

    /// Starts polling tiers A and B. Idempotent; a no-op while unconfigured
    /// (the next `configure` with a URL starts it).
    public func start() {
        wantsStart = true
        guard services != nil else { return }
        coordinator.start()
    }

    /// Stops all polling.
    public func stop() {
        wantsStart = false
        coordinator.stop()
    }

    /// System sleep: stop polling, keep holds.
    public func pause() { coordinator.pause() }

    /// Wake: resume with an immediate fetch of every active feed.
    public func resume() { coordinator.resume() }

    /// Holds the feeds until the calling task is cancelled: use it from a
    /// view's `.task`. Holds are refcounted, so two views can share a feed.
    public func track(_ feeds: Feed...) async {
        await track(feeds)
    }

    public func track(_ feeds: [Feed]) async {
        coordinator.hold(feeds)
        defer { coordinator.release(feeds) }
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(3600))
        }
    }

    /// Fetches now (⌘R, the menu opening). No feeds means every active one.
    public func refreshNow(_ feeds: Feed...) async {
        await coordinator.refreshNow(feeds)
    }

    /// Fetches the feeds that are older than `age`, for surfaces that open
    /// often (the menu) and shouldn't refetch what's seconds old.
    public func refreshNow(_ feeds: [Feed], ifOlderThan age: Duration) async {
        await coordinator.refreshNow(feeds, ifOlderThan: age)
    }

    /// Whether a view currently holds the feed.
    public func isTracked(_ feed: Feed) -> Bool { coordinator.holdCount(feed) > 0 }

    private func resetValues() {
        snapshot = .loading
        pomodoro = .loading
        stats = .loading
        usage = .loading
        meetings = .loading
        apps = .loading
        screen = .loading
        clockHealth = .loading
        weather = .loading
        activity = .loading
        workhours = .loading
        heatmap = .loading
    }

    // MARK: Fetching

    private func fetch(_ feed: Feed) async -> FeedTick {
        guard let s = services else { return .failed(.offline) }
        switch feed {
        case .state: return await fetchState(s)
        case .pomodoroState: return await fetchPomodoro(s)
        case .stats: return await load(feed, \.stats) { try await s.stats.stats() }
        case .usage: return await load(feed, \.usage) { try await s.usage.snapshot() }
        case .meetings: return await load(feed, \.meetings) { try await s.meetings.state() }
        case .apps: return await load(feed, \.apps) { try await s.apps.list() }
        case .screen: return await fetchScreen(s)
        case .clockHealth: return await load(feed, \.clockHealth) { try await s.health.clockHealth() }
        case .weather: return await load(feed, \.weather) { try await s.weather.state() }
        case .activity: return await load(feed, \.activity) { try await s.activity.summary(days: 7) }
        case .workhours: return await load(feed, \.workhours) { try await s.stats.workHours(days: 14) }
        case .heatmap: return await load(feed, \.heatmap) { try await s.stats.heatmap(days: 84) }
        }
    }

    /// Issues a request number; `isCurrent` says whether its answer may land.
    private func issue(_ feed: Feed) -> (generation: Int, seq: Int) {
        let seq = issued[feed, default: 0] + 1
        issued[feed] = seq
        return (generation, seq)
    }

    private func isCurrent(_ feed: Feed, _ ticket: (generation: Int, seq: Int)) -> Bool {
        ticket.generation == generation && issued[feed] == ticket.seq && !Task.isCancelled
    }

    private func load<T: Sendable & Equatable>(
        _ feed: Feed,
        _ keyPath: ReferenceWritableKeyPath<LiveModel, Loadable<T>>,
        _ op: @Sendable () async throws -> T
    ) async -> FeedTick {
        let ticket = issue(feed)
        do {
            let value = try await op()
            if isCurrent(feed, ticket) { self[keyPath: keyPath] = .loaded(value, at: clock()) }
            return .ok
        } catch {
            let e = FeedError(error)
            if isCurrent(feed, ticket) { self[keyPath: keyPath] = self[keyPath: keyPath].afterFailure(e) }
            return .failed(e, retryAfter: (error as? APIError)?.retryAfter)
        }
    }

    private func fetchState(_ s: Services) async -> FeedTick {
        let ticket = issue(.state)
        do {
            let snap = try await s.status.fetchSnapshot()
            guard isCurrent(.state, ticket) else { return .ok }
            let now = clock()
            snapshot = .loaded(snap, at: now)
            stateFailures = 0
            firstFailureAt = nil
            if case .online = connection {} else { connection = .online(since: now) }
            return .ok
        } catch {
            let e = FeedError(error)
            guard isCurrent(.state, ticket) else { return .failed(e) }
            recordStateFailure(e)
            return .failed(e, retryAfter: (error as? APIError)?.retryAfter)
        }
    }

    private func recordStateFailure(_ e: FeedError) {
        // A throttled poll isn't evidence the server is down.
        guard e != .rateLimited else { return }
        stateFailures += 1
        let now = clock()
        if firstFailureAt == nil { firstFailureAt = now }
        if stateFailures >= Self.offlineAfterFailures {
            if case .offline = connection {} else {
                connection = .offline(since: firstFailureAt ?? now)
                Self.log.info("server offline after \(self.stateFailures, privacy: .public) failed polls")
            }
            snapshot = snapshot.afterFailure(e)
        } else if connection.isOnline {
            connection = .degraded(failures: stateFailures)
        }
    }

    private func fetchPomodoro(_ s: Services) async -> FeedTick {
        let before = pomodoro.value?.phaseEnum
        let tick = await load(.pomodoroState, \.pomodoro) { try await s.pomodoro.state() }
        // A phase change means a session was just completed or abandoned.
        if let before, let after = pomodoro.value?.phaseEnum, before != after {
            Task { await self.refreshNow(.stats) }
        }
        return tick
    }

    /// The mirror's source selection (proxy, else the clock directly) and its
    /// pacing live in `MirrorPoller`; this runs one of its ticks.
    private func fetchScreen(_ s: Services) async -> FeedTick {
        let ticket = issue(.screen)
        if clockBaseURL == nil {
            clockBaseURL = (try? await s.device.config())?.baseURL ?? ""
        }
        var pixels: [Int]?
        var failure: FeedError?
        var retryAfter: Duration?
        if mirror.probesProxy {
            do {
                pixels = try await s.device.screen()
                mirror.record(.pixels)
            } catch let e as APIError where e.isRateLimited {
                let wait = e.retryAfter ?? RateLimitBackoff.fallbackRetryAfter
                retryAfter = wait
                mirror.record(.throttled(wait))
                failure = .rateLimited
            } catch {
                mirror.record(.failed)
                failure = FeedError(error)
            }
        }
        if mirror.triesDirect(havePixels: pixels != nil), let base = clockBaseURL, !base.isEmpty {
            pixels = try? await DeviceService.directScreen(clockBaseURL: base)
        }
        let next = mirror.endTick(havePixels: pixels != nil)
        if isCurrent(.screen, ticket) {
            if let pixels {
                screen = .loaded(pixels, at: clock())
            } else {
                screen = screen.afterFailure(failure ?? .offline)
            }
        }
        // The pacer already folded any 429 into `next`.
        return FeedTick(error: pixels == nil ? (failure ?? .offline) : nil,
                        retryAfter: pixels == nil ? retryAfter : nil,
                        nextDelay: next)
    }
}
