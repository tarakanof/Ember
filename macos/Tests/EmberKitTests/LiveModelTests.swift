import Testing
import Foundation
@testable import EmberKit

/// Lock-guarded mutable value the stub handler (URLProtocol thread) reads.
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

/// A model whose coordinator runs on a manual clock that tests never advance,
/// so only `refreshNow` fetches.
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

// Pomodoro 404s while the feature is off. That's `.featureOff` for its feeds,
// not a connection problem.
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
    // The snapshot is still treated as live: the bot keeps its state.
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

/// Lock-guarded flag the stub handler flips.
private final class Flag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    func set() { lock.withLock { value = true } }
    var isSet: Bool { lock.withLock { value } }
}

// A slow response from the server the model was configured for before a
// Connection change must not land after, and overwrite, the new server's.
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
    // The proxy 404s and device/config has no clock: no pixels, offline.
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
