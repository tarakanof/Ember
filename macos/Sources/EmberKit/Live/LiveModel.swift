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
    /// The server's release ("0.29.0", from `GET /version`); nil until read,
    /// and for a dev build. Not a feed: it only changes when the server
    /// restarts, so it's read when the connection comes up (first connect, a
    /// new server, back from offline — an upgrade restarts the server).
    public private(set) var serverVersion: String?

    /// The session the menu-bar icon and bot show. nil while the snapshot is
    /// stale: an old "running" must not keep the bot working through an outage.
    public var winningSession: Session? {
        guard case .loaded(let snap, _) = snapshot else { return nil }
        return pickWinning(snap.sessions)
    }

    /// Sessions from the latest snapshot, live or stale.
    public var sessions: [Session] { snapshot.value?.sessions ?? [] }

    /// Whether the clock's LED matrix is lit: the one value the menu, the
    /// Dashboard and Settings show. The newest observation wins: the
    /// clock-health feed's `matrixPower` (dated when the server probed the
    /// clock, which can be up to 30 s before the fetch), or a report from a
    /// power write, a reboot or a direct read. nil until one of them says.
    public var displayPower: Bool? {
        switch (healthPower, reportedPower) {
        // The health reading must be clearly newer: its dating is only good
        // to about a second (the wire times are whole seconds, plus latency).
        case let (h?, r?): h.at > r.at + Self.healthPowerMargin ? h.on : r.on
        case let (h?, nil): h.on
        case let (nil, r?): r.on
        case (nil, nil): nil
        }
    }

    static let healthPowerMargin: TimeInterval = 2

    /// Taken when a power write or a direct read is issued, and handed back
    /// with its result: a result for a server this model no longer talks to
    /// is dropped, and a read is dated when it was asked.
    public struct DisplayPowerTicket: Sendable, Equatable {
        fileprivate let generation: Int
        fileprivate let issuedAt: Date
    }

    public func displayPowerTicket() -> DisplayPowerTicket {
        DisplayPowerTicket(generation: generation, issuedAt: clock())
    }

    /// A write (or reboot) left the matrix `on`: true from the moment it
    /// returned.
    public func reportDisplayPower(_ on: Bool, written ticket: DisplayPowerTicket) {
        recordPower(on, at: clock(), ticket)
    }

    /// A direct read saw `on`. Dated when it was issued, so a write that
    /// landed while the read was in flight stays newer.
    public func reportDisplayPower(_ on: Bool, read ticket: DisplayPowerTicket) {
        recordPower(on, at: ticket.issuedAt, ticket)
    }

    private func recordPower(_ on: Bool, at: Date, _ ticket: DisplayPowerTicket) {
        guard ticket.generation == generation else { return }
        if let current = reportedPower, current.at > at { return }
        reportedPower = PowerObservation(on: on, at: at)
    }

    private struct PowerObservation: Equatable {
        let on: Bool
        let at: Date
    }

    private var reportedPower: PowerObservation?

    /// The health feed's reading, dated on this Mac's clock: when the value
    /// landed minus the probe's age, which the server reports in its own
    /// time (`generatedAt − checkedAt`), so the two clocks never mix.
    private var healthPower: PowerObservation? {
        guard let health = clockHealth.value, let fetched = clockHealth.loadedAt,
              let device = health.device, let on = device.matrixPower else { return nil }
        let age = max(0, health.generatedAt.timeIntervalSince(device.checkedAt))
        return PowerObservation(on: on, at: fetched.addingTimeInterval(-age))
    }

    @ObservationIgnored private var coordinator: RefreshCoordinator!
    @ObservationIgnored private let clock: @MainActor () -> Date
    /// The configured server's client; nil while unconfigured.
    @ObservationIgnored private var client: APIClient?
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
    @ObservationIgnored private var server: ServerIdentity?
    /// When each feed last fetched successfully. Not observed: a poll that
    /// returns the same value doesn't re-render anything.
    @ObservationIgnored private var fetchedAt: [Feed: Date] = [:]
    /// The connection came up and `serverVersion` hasn't been read since.
    @ObservationIgnored private var versionDue = false
    @ObservationIgnored private var versionFetch: Task<Void, Never>?

    private static let log = Logger(subsystem: "com.ember.Ember", category: "live")

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
    /// `.unconfigured` and idle. A client for the same URL and token (a
    /// Connection save of the source name or colour) changes nothing, so the
    /// menu and bot don't blink back to "Connecting…".
    public func configure(client: APIClient) {
        let identity = ServerIdentity(client)
        // `ServerConnection.reload` is the authoritative identity check; this
        // one only keeps a direct caller (tests) idempotent.
        guard identity != server else { return }
        server = identity
        generation += 1
        coordinator.forgetInFlight()
        fetchedAt.removeAll()
        issued.removeAll()
        stateFailures = 0
        firstFailureAt = nil
        mirror = MirrorPoller()
        clockBaseURL = nil
        resetValues()
        guard client.baseURL != nil else {
            self.client = nil
            connection = .unconfigured
            coordinator.stop()
            return
        }
        self.client = client
        connection = .connecting
        guard wantsStart else { return }
        if coordinator.isStarted { coordinator.restart() } else { coordinator.start() }
    }

    /// Starts polling tiers A and B. Idempotent; a no-op while unconfigured
    /// (the next `configure` with a URL starts it).
    public func start() {
        wantsStart = true
        guard client != nil else { return }
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
        // A mirror appearing re-reads the clock's address, as it did before
        // the feed existed: the first lookup may have failed (no token yet).
        if feeds.contains(.screen), !isTracked(.screen) { clockBaseURL = nil }
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

    /// When the feed last fetched successfully, even if the value didn't
    /// change (`Loadable.loadedAt` is when the value last changed). Not
    /// observable; read it when rendering for another reason.
    public func lastFetched(_ feed: Feed) -> Date? { fetchedAt[feed] }

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
        reportedPower = nil
        serverVersion = nil
        versionDue = false
        versionFetch?.cancel()
        versionFetch = nil
    }

    // MARK: Fetching

    private func fetch(_ feed: Feed) async -> FeedTick {
        guard let c = client else { return .failed(.offline) }
        // Every read the app polls, one route each. `days` is clamped
        // server-side (activity/workhours 1...90, heatmap 7...366).
        switch feed {
        case .state: return await fetchState(c)
        case .pomodoroState: return await fetchPomodoro(c)
        case .stats: return await load(feed, \.stats) { try await c.get("/v1/pomodoro/stats") }
        case .usage: return await load(feed, \.usage) { try await c.get("/v1/usage") }
        case .meetings: return await load(feed, \.meetings) { try await c.get("/v1/meetings/state") }
        case .apps: return await load(feed, \.apps) { try await (c.get("/v1/apps") as AppsList).apps }
        case .screen: return await fetchScreen(DeviceService(client: c))
        case .clockHealth: return await load(feed, \.clockHealth) { try await c.get("/v1/clock/health") }
        case .weather: return await load(feed, \.weather) { try await c.get("/v1/weather/state") }
        case .activity: return await load(feed, \.activity) { try await c.get("/v1/activity/summary", query: Self.days(7)) }
        case .workhours: return await load(feed, \.workhours) { try await c.get("/v1/pomodoro/workhours", query: Self.days(14)) }
        case .heatmap: return await load(feed, \.heatmap) { try await c.get("/v1/pomodoro/heatmap", query: Self.days(84)) }
        }
    }

    private static func days(_ n: Int) -> [URLQueryItem] {
        [URLQueryItem(name: "days", value: String(n))]
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
            if isCurrent(feed, ticket) { applySuccess(feed, keyPath, value) }
            return .ok
        } catch {
            let e = FeedError(error)
            if isCurrent(feed, ticket) { applyFailure(feed, keyPath, e) }
            return .failed(e, retryAfter: (error as? APIError)?.retryAfter)
        }
    }

    /// Publishes a fetched value only when it differs from what's shown, so
    /// an unchanged 3 s poll doesn't invalidate every observer.
    private func applySuccess<T: Sendable & Equatable>(
        _ feed: Feed, _ keyPath: ReferenceWritableKeyPath<LiveModel, Loadable<T>>, _ value: T
    ) {
        let now = clock()
        fetchedAt[feed] = now
        if case .loaded(let current, _) = self[keyPath: keyPath], current == value { return }
        self[keyPath: keyPath] = .loaded(value, at: now)
    }

    /// Keeps the last value, stamped with the last successful fetch so a
    /// stale chip counts from then, not from when the value last changed.
    private func applyFailure<T: Sendable & Equatable>(
        _ feed: Feed, _ keyPath: ReferenceWritableKeyPath<LiveModel, Loadable<T>>, _ e: FeedError
    ) {
        let current = self[keyPath: keyPath]
        let next = Loadable<T>.failed(e, last: current.value, lastAt: fetchedAt[feed] ?? current.loadedAt)
        if next != current { self[keyPath: keyPath] = next }
    }

    private func fetchState(_ c: APIClient) async -> FeedTick {
        let ticket = issue(.state)
        do {
            let snap: Snapshot = try await c.get("/state")
            guard isCurrent(.state, ticket) else { return .ok }
            let now = clock()
            applySuccess(.state, \.snapshot, snap)
            stateFailures = 0
            firstFailureAt = nil
            if !connection.isOnline { versionDue = true }
            if case .online = connection {} else { connection = .online(since: now) }
            if versionDue { fetchVersion(c) }
            return .ok
        } catch {
            let e = FeedError(error)
            guard isCurrent(.state, ticket) else { return .failed(e) }
            recordStateFailure(e)
            return .failed(e, retryAfter: (error as? APIError)?.retryAfter)
        }
    }

    /// Reads `/version` off the `/state` loop, so a lost request doesn't hold
    /// up the 3 s poll. A transport miss or a 429 leaves it due for the next
    /// good `/state`; any answer (even a 404 from a server without the route)
    /// settles it until the connection comes up again.
    private func fetchVersion(_ c: APIClient) {
        guard versionFetch == nil else { return }
        let gen = generation
        versionFetch = Task {
            defer { if gen == self.generation { self.versionFetch = nil } }
            do {
                let info: VersionInfo = try await c.get("/version")
                guard gen == generation else { return }
                serverVersion = info.release
                versionDue = false
            } catch {
                guard gen == generation else { return }
                switch FeedError(error) {
                case .offline, .rateLimited: break
                // The server answered without a version (a rollback to one
                // without the route, say): don't keep showing the old one.
                default:
                    serverVersion = nil
                    versionDue = false
                }
            }
        }
    }

    /// Waits for a `/version` read in flight (tests).
    func versionFetchSettled() async { await versionFetch?.value }

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
            applyFailure(.state, \.snapshot, e)
        } else if connection.isOnline {
            connection = .degraded(failures: stateFailures)
        }
    }

    private func fetchPomodoro(_ c: APIClient) async -> FeedTick {
        let before = pomodoro.value?.phaseEnum
        let tick = await load(.pomodoroState, \.pomodoro) { try await c.get("/v1/pomodoro/state") }
        // A phase change means a session was just completed or abandoned.
        if let before, let after = pomodoro.value?.phaseEnum, before != after {
            Task { await self.refreshNow(.stats) }
        }
        return tick
    }

    /// The mirror's source selection (proxy, else the clock directly) and its
    /// pacing live in `MirrorPoller`; this runs one of its ticks.
    private func fetchScreen(_ device: DeviceService) async -> FeedTick {
        let ticket = issue(.screen)
        if clockBaseURL == nil {
            clockBaseURL = (try? await device.config())?.baseURL ?? ""
        }
        var pixels: [Int]?
        var failure: FeedError?
        var retryAfter: Duration?
        if mirror.probesProxy {
            do {
                pixels = try await device.screen()
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
                applySuccess(.screen, \.screen, pixels)
            } else {
                applyFailure(.screen, \.screen, failure ?? .offline)
            }
        }
        // The pacer already folded any 429 into `next`.
        return FeedTick(error: pixels == nil ? (failure ?? .offline) : nil,
                        retryAfter: pixels == nil ? retryAfter : nil,
                        nextDelay: next)
    }
}
