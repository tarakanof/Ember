import Testing
import Foundation
@testable import EmberKit

private final class Box<T: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: T
    init(_ v: T) { _value = v }
    var value: T {
        get { lock.withLock { _value } }
        set { lock.withLock { _value = newValue } }
    }
}

private func sessionJSON(_ tool: String, state: String = "running") -> Data {
    Data(#"{"sessions":[{"source":"mbp","tool":"\#(tool)","session":"s","state":"\#(state)","message":""}]}"#.utf8)
}

private let pomoJSON = #"{"phase":"focus","running":true,"paused":false,"remaining_sec":300,"planned_sec":1500,"round":1}"#
private let statsJSON = #"{"today":{"date":"2026-05-29","completed_focus":2,"focus_min":50},"history":[],"streak":3}"#

@MainActor
private func makeModel(now: Date = Date(timeIntervalSince1970: 1_000)) -> LiveModel {
    let clock = ManualClock()
    return LiveModel(now: { now }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
}

@MainActor @Test func unconfiguredModelIsIdle() {
    let m = makeModel()
    m.configure(client: APIClient(baseURL: nil, token: nil))
    #expect(m.connection == .unconfigured)
    #expect(m.snapshot == .loading)
}

@MainActor @Test func refreshPopulatesFeeds() async {
    let client = stubbedClient { req in
        switch req.url!.path {
        case "/state": return (okResponse(req.url!), sessionJSON("claude"))
        case "/v1/pomodoro/state": return (okResponse(req.url!), Data(pomoJSON.utf8))
        case "/v1/pomodoro/stats": return (okResponse(req.url!), Data(statsJSON.utf8))
        case "/v1/apps": return (okResponse(req.url!), Data(#"{"apps":[{"name":"claude","enabled":true}]}"#.utf8))
        default: return (okResponse(req.url!, status: 404), Data())
        }
    }
    let m = makeModel()
    m.configure(client: client)
    #expect(m.connection == .connecting)
    await m.refreshNow(.state, .pomodoroState, .stats, .apps)

    #expect(m.connection == .online(since: Date(timeIntervalSince1970: 1_000)))
    #expect(m.winningSession?.tool == "claude")
    #expect(m.sessions.count == 1)
    #expect(m.pomodoro.value?.phaseEnum == .focus)
    #expect(m.stats.value?.streak == 3)
    #expect(m.apps.value?.first?.name == "claude")
    #expect(m.stats.loadedAt == Date(timeIntervalSince1970: 1_000))
}

@MainActor @Test func pomodoroDisabledIsFeatureOffAndStaysConnected() async {
    let client = stubbedClient { req in
        if req.url!.path.hasPrefix("/v1/pomodoro/") {
            return (okResponse(req.url!, status: 404), Data(#"{"error":"pomodoro feature is not enabled"}"#.utf8))
        }
        return (okResponse(req.url!), sessionJSON("claude"))
    }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.state, .pomodoroState, .stats)
    #expect(m.connection.isOnline)
    #expect(m.pomodoro == .failed(.featureOff, last: nil, lastAt: nil))
    #expect(m.stats.error == .featureOff)
}

@MainActor @Test func a404AfterAValueKeepsTheLastValue() async {
    let fail = Box(false)
    let client = stubbedClient { req in
        if fail.value { return (okResponse(req.url!, status: 404), Data()) }
        return (okResponse(req.url!), Data(statsJSON.utf8))
    }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.stats)
    fail.value = true
    await m.refreshNow(.stats)
    #expect(m.stats.error == .featureOff)
    #expect(m.stats.value?.streak == 3)
    #expect(m.stats.isStale)
}

@MainActor @Test func stateFailuresDegradeThenGoOfflineAfterThree() async {
    let fail = Box(false)
    let client = stubbedClient { req in
        if fail.value { return (okResponse(req.url!, status: 500), Data("boom".utf8)) }
        return (okResponse(req.url!), sessionJSON("claude"))
    }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.state)
    fail.value = true

    await m.refreshNow(.state)
    #expect(m.connection == .degraded(failures: 1))
    await m.refreshNow(.state)
    #expect(m.connection == .degraded(failures: 2))
    #expect(m.winningSession?.tool == "claude")

    await m.refreshNow(.state)
    #expect(m.connection == .offline(since: Date(timeIntervalSince1970: 1_000)))
    #expect(m.snapshot.isStale)
    #expect(m.sessions.first?.tool == "claude")
    #expect(m.winningSession == nil)

    fail.value = false
    await m.refreshNow(.state)
    #expect(m.connection.isOnline)
    #expect(m.winningSession?.tool == "claude")
}

@MainActor @Test func neverConnectedServerGoesOfflineAfterThreeFailures() async {
    let client = stubbedClient { req in (okResponse(req.url!, status: 500), Data()) }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.state)
    await m.refreshNow(.state)
    #expect(m.connection == .connecting)
    await m.refreshNow(.state)
    #expect(m.connection == .offline(since: Date(timeIntervalSince1970: 1_000)))
    #expect(m.snapshot == .failed(.server("HTTP 500"), last: nil, lastAt: nil))
}

@MainActor @Test func rateLimitedStatePollsDontCountTowardOffline() async {
    let client = stubbedClient { req in
        (HTTPURLResponse(url: req.url!, statusCode: 429, httpVersion: nil, headerFields: ["Retry-After": "2"])!, Data())
    }
    let m = makeModel()
    m.configure(client: client)
    for _ in 0..<4 { await m.refreshNow(.state) }
    #expect(m.connection == .connecting)
}

private final class Flag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    func set() { lock.withLock { value = true } }
    var isSet: Bool { lock.withLock { value } }
}

@MainActor @Test func staleResponseFromPreviousServerIsDropped() async throws {
    let started = Flag()
    let gate = DispatchSemaphore(value: 0)
    let oldClient = stubbedClient { req in
        started.set()
        gate.wait()
        return (okResponse(req.url!), sessionJSON("old"))
    }
    let newClient = stubbedClient { req in (okResponse(req.url!), sessionJSON("new")) }
    let m = makeModel()
    m.configure(client: oldClient)
    let stale = Task { await m.refreshNow(.state) }
    while !started.isSet { try await Task.sleep(for: .milliseconds(5)) }

    m.configure(client: newClient)
    await m.refreshNow(.state)
    #expect(m.sessions.first?.tool == "new")

    gate.signal()
    await stale.value
    #expect(m.sessions.first?.tool == "new")
    #expect(m.connection.isOnline)
}

@MainActor @Test func configureResetsValues() async {
    let client = stubbedClient { req in (okResponse(req.url!), Data(statsJSON.utf8)) }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.stats)
    #expect(m.stats.value != nil)
    m.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    #expect(m.stats == .loading)
}

@MainActor @Test func pomodoroPhaseChangeRefreshesStats() async throws {
    let phase = Box("focus")
    let statsCalls = LockedBox()
    let client = stubbedClient { req in
        switch req.url!.path {
        case "/v1/pomodoro/state":
            let json = #"{"phase":"\#(phase.value)","running":true,"paused":false,"remaining_sec":1,"planned_sec":1,"round":1}"#
            return (okResponse(req.url!), Data(json.utf8))
        case "/v1/pomodoro/stats":
            statsCalls.add("stats")
            return (okResponse(req.url!), Data(statsJSON.utf8))
        default: return (okResponse(req.url!, status: 404), Data())
        }
    }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.pomodoroState)
    await m.refreshNow(.pomodoroState)
    #expect(statsCalls.paths.isEmpty)
    phase.value = "short_break"
    await m.refreshNow(.pomodoroState)
    for _ in 0..<1000 where statsCalls.paths.isEmpty { try await Task.sleep(for: .milliseconds(5)) }
    #expect(statsCalls.paths == ["stats"])
}

@MainActor @Test func trackHoldsUntilCancelled() async throws {
    let m = makeModel()
    m.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    let view = Task { await m.track(.screen, .clockHealth) }
    for _ in 0..<200 where !m.isTracked(.screen) { await Task.yield() }
    #expect(m.isTracked(.screen))
    #expect(m.isTracked(.clockHealth))
    view.cancel()
    await view.value
    #expect(!m.isTracked(.screen))
}

@MainActor @Test func screenFallsBackToTheClockWhenTheProxyFails() async {
    let client = stubbedClient { req in
        if req.url!.path == "/v1/device/config" {
            return (okResponse(req.url!), Data(#"{"base_url":"","source":""}"#.utf8))
        }
        return (okResponse(req.url!, status: 404), Data())
    }
    let m = makeModel()
    m.configure(client: client)
    let hold = Task { await m.track(.screen) }
    for _ in 0..<200 where !m.isTracked(.screen) { await Task.yield() }
    await m.refreshNow(.screen)
    #expect(m.screen.error == .featureOff)
    hold.cancel()
}

@MainActor @Test func screenLoadsFromTheProxy() async {
    let pixels = Array(repeating: 0xFF0000, count: 256)
    let body = try! JSONSerialization.data(withJSONObject: ["width": 32, "height": 8, "pixels": pixels])
    let client = stubbedClient { req in
        switch req.url!.path {
        case "/v1/device/screen": return (okResponse(req.url!), body)
        case "/v1/device/config": return (okResponse(req.url!), Data(#"{"base_url":"","source":""}"#.utf8))
        default: return (okResponse(req.url!, status: 404), Data())
        }
    }
    let m = makeModel()
    m.configure(client: client)
    let hold = Task { await m.track(.screen) }
    for _ in 0..<200 where !m.isTracked(.screen) { await Task.yield() }
    await m.refreshNow(.screen)
    #expect(m.screen.value?.count == 256)
    #expect(m.screen.value?.first == 0xFF0000)
    hold.cancel()
}

@MainActor @Test func reconfiguringTheSameServerKeepsEveryValue() async {
    let client = stubbedClient { req in (okResponse(req.url!), sessionJSON("claude")) }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshNow(.state)
    m.configure(client: APIClient(baseURL: client.baseURL, token: client.token))
    #expect(m.connection.isOnline)
    #expect(m.winningSession?.tool == "claude")
}

@MainActor @Test func anUnchangedPollDoesNotNotifyObservers() async {
    let client = stubbedClient { req in (okResponse(req.url!), sessionJSON("claude")) }
    var now = Date(timeIntervalSince1970: 1)
    let clock = ManualClock()
    let m = LiveModel(now: { now }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
    m.configure(client: client)
    await m.refreshNow(.state)
    now = Date(timeIntervalSince1970: 2)
    let changed = Flag()
    withObservationTracking { _ = m.snapshot } onChange: { changed.set() }
    await m.refreshNow(.state)
    #expect(!changed.isSet)
    #expect(m.snapshot.loadedAt == Date(timeIntervalSince1970: 1))
    #expect(m.lastFetched(.state) == Date(timeIntervalSince1970: 2))
}

@MainActor @Test func aFailureCountsStalenessFromTheLastFetch() async {
    let fail = Box(false)
    let client = stubbedClient { req in
        fail.value ? (okResponse(req.url!, status: 500), Data()) : (okResponse(req.url!), Data(statsJSON.utf8))
    }
    var now = Date(timeIntervalSince1970: 10)
    let clock = ManualClock()
    let m = LiveModel(now: { now }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
    m.configure(client: client)
    await m.refreshNow(.stats)
    now = Date(timeIntervalSince1970: 70)
    await m.refreshNow(.stats)
    fail.value = true
    await m.refreshNow(.stats)
    #expect(m.stats.loadedAt == Date(timeIntervalSince1970: 70))
    #expect(m.stats.isStale)
}

@MainActor @Test func aMirrorAppearingRereadsTheClockAddress() async {
    let configReads = LockedBox()
    let client = stubbedClient { req in
        if req.url!.path == "/v1/device/config" {
            configReads.add("config")
            return (okResponse(req.url!, status: 401), Data())
        }
        return (okResponse(req.url!, status: 404), Data())
    }
    let m = makeModel()
    m.configure(client: client)
    for _ in 0..<2 {
        let hold = Task { await m.track(.screen) }
        for _ in 0..<200 where !m.isTracked(.screen) { await Task.yield() }
        await m.refreshNow(.screen)
        hold.cancel()
        await hold.value
    }
    #expect(configReads.paths.count == 2)
}

private func healthJSON(power: Bool, generated: Int, age: Int) -> Data {
    let fmt = { (s: Int) in Date(timeIntervalSince1970: TimeInterval(s)).ISO8601Format() }
    return Data("""
    {"generated_at":"\(fmt(generated))","publish":{"counting_since":"\(fmt(0))","ok_24h":0,"fail_24h":0,\
    "ok_total":0,"fail_total":0,"retries_total":0,"last_ok":true},\
    "device":{"reachable":true,"checked_at":"\(fmt(generated - age))","matrix_power":\(power)}}
    """.utf8)
}

@MainActor @Test func displayPowerIsTheNewestObservation() async {
    let body = Box(healthJSON(power: false, generated: 5_000, age: 0))
    let client = stubbedClient { req in (okResponse(req.url!), body.value) }
    let now = Box(Date(timeIntervalSince1970: 1_000))
    let clock = ManualClock()
    let m = LiveModel(now: { now.value }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
    m.configure(client: client)
    #expect(m.displayPower == nil)
    let hold = Task { await m.track(.clockHealth) }
    for _ in 0..<200 where !m.isTracked(.clockHealth) { await Task.yield() }

    await m.refreshNow(.clockHealth)
    #expect(m.displayPower == false)

    now.value = Date(timeIntervalSince1970: 1_010)
    m.reportDisplayPower(true, written: m.displayPowerTicket())
    #expect(m.displayPower == true)

    now.value = Date(timeIntervalSince1970: 1_015)
    body.value = healthJSON(power: false, generated: 5_015, age: 20)
    await m.refreshNow(.clockHealth)
    #expect(m.displayPower == true)

    now.value = Date(timeIntervalSince1970: 1_016)
    body.value = healthJSON(power: false, generated: 5_016, age: 4)
    await m.refreshNow(.clockHealth)
    #expect(m.displayPower == true)

    now.value = Date(timeIntervalSince1970: 1_045)
    body.value = healthJSON(power: false, generated: 5_045, age: 0)
    await m.refreshNow(.clockHealth)
    #expect(m.displayPower == false)
    hold.cancel()

    m.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    #expect(m.displayPower == nil)
}

@MainActor
private func timedModel(_ now: Box<Date>) -> LiveModel {
    let clock = ManualClock()
    return LiveModel(now: { now.value }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
}

@MainActor @Test func aReadAWriteOvertookDoesNotWin() {
    let now = Box(Date(timeIntervalSince1970: 1_000))
    let m = timedModel(now)
    m.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    let read = m.displayPowerTicket()
    now.value = Date(timeIntervalSince1970: 1_001)
    let write = m.displayPowerTicket()
    now.value = Date(timeIntervalSince1970: 1_002)
    m.reportDisplayPower(false, written: write)
    now.value = Date(timeIntervalSince1970: 1_003)
    m.reportDisplayPower(true, read: read)
    #expect(m.displayPower == false)
}

@MainActor @Test func aResultFromThePreviousServerIsDropped() {
    let now = Box(Date(timeIntervalSince1970: 1_000))
    let m = timedModel(now)
    m.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    let old = m.displayPowerTicket()
    m.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    now.value = Date(timeIntervalSince1970: 1_005)
    m.reportDisplayPower(true, written: old)
    m.reportDisplayPower(true, read: old)
    #expect(m.displayPower == nil)
}

@MainActor @Test func everyFeedAsksForItsRoute() async {
    let seen = LockedBox()
    let client = stubbedClient { req in
        let q = req.url!.query.map { "?\($0)" } ?? ""
        seen.add(req.url!.path + q)
        return (okResponse(req.url!, status: 404), Data())
    }
    let m = makeModel()
    m.configure(client: client)
    let tierC = Feed.allCases.filter { $0.tier == .c }
    let hold = Task { await m.track(tierC) }
    for _ in 0..<200 where !m.isTracked(.heatmap) { await Task.yield() }
    await m.refreshNow(Feed.allCases, ifOlderThan: .zero)
    hold.cancel()
    #expect(Set(seen.paths) == [
        "/state", "/v1/pomodoro/state", "/v1/pomodoro/stats", "/v1/usage", "/v1/meetings/state",
        "/v1/apps", "/v1/device/config", "/v1/device/screen", "/v1/clock/health", "/v1/weather/state",
        "/v1/activity/summary?days=7", "/v1/pomodoro/heatmap?days=84",
        "/v1/day/state", "/v1/day/history?days=28", "/v1/day/weeks?weeks=8",
    ])
}

@MainActor @Test func openingTheMenuReadsNoClockHealth() async {
    let seen = LockedBox()
    let client = stubbedClient { req in
        seen.add(req.url!.path)
        return (okResponse(req.url!, status: 404), Data())
    }
    let m = makeModel()
    m.configure(client: client)
    await m.refreshMenuFeeds()
    #expect(Set(seen.paths) == ["/v1/pomodoro/stats", "/v1/meetings/state", "/v1/usage"])
}

private func tagged(_ url: URL, status: Int = 200, etag: String? = #"W/"a""#, cap: String? = "25") -> HTTPURLResponse {
    var headers: [String: String] = [:]
    if let etag { headers["ETag"] = etag }
    if let cap { headers["X-Ember-View-Wait"] = cap }
    return HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: headers)!
}

@MainActor
private final class FetchHarness {
    let now = Box(Date(timeIntervalSince1970: 1_000))
    var fetch: RefreshCoordinator.Fetch!
    private(set) var model: LiveModel!

    init(_ client: APIClient) {
        let clock = ManualClock()
        let now = self.now
        model = LiveModel(now: { now.value }, makeCoordinator: { [self] in
            fetch = $0
            return RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
        })
        model.configure(client: client)
    }
}

@MainActor @Test func aTaggedFeedSendsTheTagAndWaitsOnlyWhenAsked() async {
    let seen = Box<[URLRequest]>([])
    let client = stubbedClient { req in
        guard req.url?.path == "/state" else { return (okResponse(req.url!, status: 404), Data()) }
        seen.value.append(req)
        if seen.value.count == 1 { return (tagged(req.url!), sessionJSON("claude")) }
        return (tagged(req.url!, status: 304), Data())
    }
    let h = FetchHarness(client)
    let first = await h.fetch(.state, true)
    #expect(first.serverWaits)
    #expect(seen.value[0].value(forHTTPHeaderField: "If-None-Match") == nil)
    #expect(seen.value[0].url?.query == nil)

    let plain = await h.fetch(.state, false)
    #expect(plain.serverWaits)
    #expect(seen.value[1].value(forHTTPHeaderField: "If-None-Match") == #"W/"a""#)
    #expect(seen.value[1].url?.query == nil)

    let parked = await h.fetch(.state, true)
    #expect(parked.serverWaits)
    #expect(seen.value[2].url?.query == "wait=25")
    #expect(h.model.winningSession?.tool == "claude")
    #expect(h.model.connection.isOnline)
}

@MainActor @Test func anOldServerKeepsPlainPollingEvenWhenTheLoopMayWait() async {
    let seen = Box<[URLRequest]>([])
    let client = stubbedClient { req in
        guard req.url?.path == "/state" else { return (okResponse(req.url!, status: 404), Data()) }
        seen.value.append(req)
        return (okResponse(req.url!), sessionJSON("claude"))
    }
    let h = FetchHarness(client)
    let first = await h.fetch(.state, false)
    #expect(!first.serverWaits)
    _ = await h.fetch(.state, true)
    #expect(seen.value[1].value(forHTTPHeaderField: "If-None-Match") == nil)
    #expect(seen.value[1].url?.query == nil)
}

@MainActor @Test func aTagWithoutAWaitCapDoesNotLongPoll() async {
    let client = stubbedClient { req in (tagged(req.url!, cap: nil), sessionJSON("claude")) }
    let tick = await FetchHarness(client).fetch(.state, false)
    #expect(!tick.serverWaits)
}

@MainActor @Test func aNotModifiedReplyHealsAFailedFeedAndCountsAsAlive() async {
    let step = Box(0)
    let client = stubbedClient { req in
        guard req.url?.path == "/state" else { return (okResponse(req.url!, status: 404), Data()) }
        step.value += 1
        switch step.value {
        case 1: return (tagged(req.url!), sessionJSON("claude"))
        case 2, 3, 4: return (okResponse(req.url!, status: 500), Data("boom".utf8))
        default: return (tagged(req.url!, status: 304), Data())
        }
    }
    let h = FetchHarness(client)
    _ = await h.fetch(.state, false)
    for _ in 0..<3 { _ = await h.fetch(.state, false) }
    #expect(h.model.snapshot.error != nil)
    #expect(!h.model.connection.isOnline)
    h.now.value = Date(timeIntervalSince1970: 1_050)
    _ = await h.fetch(.state, false)
    #expect(h.model.snapshot.error == nil)
    #expect(h.model.snapshot.value?.sessions.first?.tool == "claude")
    #expect(h.model.connection.isOnline)
    #expect(h.model.lastFetched(.state) == Date(timeIntervalSince1970: 1_050))
}

@MainActor @Test func aRunningTimerCountsDownLocallyBetween304s() async {
    let step = Box(0)
    let client = stubbedClient { req in
        step.value += 1
        if step.value == 1 { return (tagged(req.url!), Data(pomoJSON.utf8)) }
        return (tagged(req.url!, status: 304), Data())
    }
    let h = FetchHarness(client)
    _ = await h.fetch(.pomodoroState, false)
    #expect(h.model.pomodoro.value?.remainingSec == 300)
    #expect(h.model.isTracked(.pomodoroCountdown))

    h.now.value = Date(timeIntervalSince1970: 1_100)
    _ = await h.fetch(.pomodoroCountdown, false)
    #expect(h.model.pomodoro.value?.remainingSec == 200)
    #expect(h.model.lastFetched(.pomodoroState) == Date(timeIntervalSince1970: 1_100))

    h.now.value = Date(timeIntervalSince1970: 1_150)
    _ = await h.fetch(.pomodoroState, true)
    #expect(h.model.pomodoro.value?.remainingSec == 150)

    h.now.value = Date(timeIntervalSince1970: 2_000)
    _ = await h.fetch(.pomodoroCountdown, false)
    #expect(h.model.pomodoro.value?.remainingSec == 0)
}

@MainActor @Test func aNewReplyMovesTheCountdownAnchorAndAPausedTimerStopsTheCountdownFeed() async {
    let paused = #"{"phase":"focus","running":true,"paused":true,"remaining_sec":250,"planned_sec":1500,"round":1}"#
    let step = Box(0)
    let client = stubbedClient { req in
        step.value += 1
        return (tagged(req.url!), Data((step.value == 1 ? pomoJSON : paused).utf8))
    }
    let h = FetchHarness(client)
    _ = await h.fetch(.pomodoroState, false)
    #expect(h.model.isTracked(.pomodoroCountdown))
    h.now.value = Date(timeIntervalSince1970: 1_050)
    _ = await h.fetch(.pomodoroState, true)
    #expect(!h.model.isTracked(.pomodoroCountdown))
    h.now.value = Date(timeIntervalSince1970: 1_500)
    _ = await h.fetch(.pomodoroCountdown, false)
    #expect(h.model.pomodoro.value?.remainingSec == 250)
}

@MainActor @Test func aFailedTimerFreezesAndStopsTheCountdownFeed() async {
    let step = Box(0)
    let client = stubbedClient { req in
        step.value += 1
        if step.value == 1 { return (tagged(req.url!), Data(pomoJSON.utf8)) }
        return (okResponse(req.url!, status: 500), Data("boom".utf8))
    }
    let h = FetchHarness(client)
    _ = await h.fetch(.pomodoroState, false)
    h.now.value = Date(timeIntervalSince1970: 1_100)
    _ = await h.fetch(.pomodoroState, false)
    #expect(!h.model.isTracked(.pomodoroCountdown))
    _ = await h.fetch(.pomodoroCountdown, false)
    #expect(h.model.pomodoro.value?.remainingSec == 300)
}

@MainActor @Test func aNewServerStartsWithoutTheOldTagOrCountdown() async {
    let seen = Box<[URLRequest]>([])
    let handler: @Sendable (URLRequest) -> (HTTPURLResponse, Data) = { req in
        seen.value.append(req)
        return (tagged(req.url!), Data(pomoJSON.utf8))
    }
    let h = FetchHarness(stubbedClient(handler: handler))
    _ = await h.fetch(.pomodoroState, false)
    #expect(h.model.isTracked(.pomodoroCountdown))
    h.model.configure(client: stubbedClient(handler: handler))
    #expect(!h.model.isTracked(.pomodoroCountdown))
    _ = await h.fetch(.pomodoroState, false)
    #expect(seen.value[1].value(forHTTPHeaderField: "If-None-Match") == nil)
}
