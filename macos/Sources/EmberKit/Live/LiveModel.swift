import Foundation
import Observation
import OSLog

/// The app's live view of the server: one `Loadable` per feed, kept fresh by a
/// `RefreshCoordinator`.
@MainActor
@Observable
public final class LiveModel {
    /// `/state` misses in a row before the connection counts as offline and
    /// the snapshot as stale.
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
    /// and for a dev build.
    public private(set) var serverVersion: String?

    /// The session the menu-bar icon and bot show.
    public var winningSession: Session? {
        guard case .loaded(let snap, _) = snapshot else { return nil }
        return pickWinning(snap.sessions)
    }

    /// Sessions from the latest snapshot, live or stale.
    public var sessions: [Session] { snapshot.value?.sessions ?? [] }

    /// Whether the clock's LED matrix is lit: the one value the menu, the
    /// Dashboard and Settings show.
    public var displayPower: Bool? {
        switch (healthPower, reportedPower) {
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

    /// A direct read saw `on`.
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

    private var healthPower: PowerObservation? {
        guard let health = clockHealth.value, let fetched = clockHealth.loadedAt,
              let device = health.device, let on = device.matrixPower else { return nil }
        let age = max(0, health.generatedAt.timeIntervalSince(device.checkedAt))
        return PowerObservation(on: on, at: fetched.addingTimeInterval(-age))
    }

    @ObservationIgnored private var coordinator: RefreshCoordinator!
    @ObservationIgnored private let clock: @MainActor () -> Date
    @ObservationIgnored private var client: APIClient?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var issued: [Feed: Int] = [:]
    @ObservationIgnored private var stateFailures = 0
    @ObservationIgnored private var firstFailureAt: Date?
    @ObservationIgnored private var mirror = MirrorPoller()
    @ObservationIgnored private var clockBaseURL: String?
    @ObservationIgnored private var wantsStart = false
    @ObservationIgnored private var server: ServerIdentity?
    @ObservationIgnored private var fetchedAt: [Feed: Date] = [:]
    @ObservationIgnored private var versionDue = false
    @ObservationIgnored private var versionFetch: Task<Void, Never>?

    private static let log = Logger(subsystem: "com.ember.Ember", category: "live")

    /// A model on the real clock.
    public convenience init() {
        self.init(now: { Date() }, makeCoordinator: { RefreshCoordinator.live(fetch: $0) })
    }

    init(now: @escaping @MainActor () -> Date,
         makeCoordinator: (@escaping RefreshCoordinator.Fetch) -> RefreshCoordinator) {
        clock = now
        coordinator = makeCoordinator { [weak self] feed in
            guard let self else { return .ok }
            return await self.fetch(feed)
        }
    }

    // MARK: Lifecycle

    /// Points every feed at a new server.
    public func configure(client: APIClient) {
        let identity = ServerIdentity(client)
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

    /// Starts polling tiers A and B.
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
    /// view's `.task`.
    public func track(_ feeds: Feed...) async {
        await track(feeds)
    }

    public func track(_ feeds: [Feed]) async {
        if feeds.contains(.screen), !isTracked(.screen) { clockBaseURL = nil }
        coordinator.hold(feeds)
        defer { coordinator.release(feeds) }
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(3600))
        }
    }

    /// Fetches now (⌘R, the menu opening).
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
    /// change (`Loadable.loadedAt` is when the value last changed).
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

    private func applySuccess<T: Sendable & Equatable>(
        _ feed: Feed, _ keyPath: ReferenceWritableKeyPath<LiveModel, Loadable<T>>, _ value: T
    ) {
        let now = clock()
        fetchedAt[feed] = now
        if case .loaded(let current, _) = self[keyPath: keyPath], current == value { return }
        self[keyPath: keyPath] = .loaded(value, at: now)
    }

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
                case .offline, .timedOut, .localNetworkDenied, .rateLimited: break
                default:
                    serverVersion = nil
                    versionDue = false
                }
            }
        }
    }

    func versionFetchSettled() async { await versionFetch?.value }

    private func recordStateFailure(_ e: FeedError) {
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
        if let before, let after = pomodoro.value?.phaseEnum, before != after {
            Task { await self.refreshNow(.stats) }
        }
        return tick
    }

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
        return FeedTick(error: pixels == nil ? (failure ?? .offline) : nil,
                        retryAfter: pixels == nil ? retryAfter : nil,
                        nextDelay: next)
    }
}
