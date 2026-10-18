import Testing
import Foundation
@testable import EmberKit

private final class Shared<T: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: T
    init(_ v: T) { _value = v }
    var value: T {
        get { lock.withLock { _value } }
        set { lock.withLock { _value = newValue } }
    }
}

private final class VersionedServer: Sendable {
    let down = Shared(false)
    let versionLost = Shared(false)
    let version = Shared("0.29.0")
    let versionReads = Shared(0)
    let clockOff = Shared(false)
    let healthReads = Shared(0)
    let healthDisabled = Shared<Bool?>(nil)

    func client() -> APIClient {
        stubbedClient { [self] req in
            switch req.url!.path {
            case "/state":
                if down.value { return (okResponse(req.url!, status: 500), Data()) }
                return (okResponse(req.url!), Data(#"{"sessions":[]}"#.utf8))
            case "/version":
                versionReads.value += 1
                if versionLost.value { throw URLError(.timedOut) }
                let off = clockOff.value ? #","clock_disabled":true"# : ""
                let body = #"{"binary":"ember","version":"\#(version.value)","revision":"44143ca","dirty":true\#(off)}"#
                return (okResponse(req.url!), Data(body.utf8))
            case "/v1/clock/health":
                healthReads.value += 1
                guard let disabled = healthDisabled.value else { return (okResponse(req.url!, status: 404), Data()) }
                return (okResponse(req.url!), Data(clockHealthJSON(disabled: disabled).utf8))
            default:
                return (okResponse(req.url!, status: 404), Data())
            }
        }
    }
}

private func clockHealthJSON(disabled: Bool) -> String {
    let device = disabled ? #""disabled":true,"device":null"# : #""device":null"#
    return #"{"generated_at":"2026-09-26T10:00:00Z","publish":{"counting_since":"2026-09-26T09:00:00Z","ok_24h":0,"fail_24h":0,"success_ratio_24h":null,"ok_total":0,"fail_total":0,"retries_total":0,"last_at":null,"last_ok":false},"#
        + device + "}"
}

@MainActor
private func makeModel(at time: Shared<Double> = Shared(1_000)) -> LiveModel {
    let clock = ManualClock()
    return LiveModel(now: { Date(timeIntervalSince1970: time.value) }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
}

@MainActor
private func refreshClockHealth(_ m: LiveModel) async {
    let hold = Task { await m.track(.clockHealth) }
    for _ in 0..<200 where !m.isTracked(.clockHealth) { await Task.yield() }
    await m.refreshNow(.clockHealth)
    hold.cancel()
    await hold.value
}

@MainActor
private func poll(_ m: LiveModel) async {
    await m.refreshNow(.state)
    await m.versionFetchSettled()
}

@MainActor @Test func serverVersionIsReadOnceOnConnect() async {
    let server = VersionedServer()
    let m = makeModel()
    m.configure(client: server.client())
    #expect(m.serverVersion == nil)
    await poll(m)
    #expect(m.serverVersion == "0.29.0")
    await poll(m)
    await poll(m)
    #expect(server.versionReads.value == 1)
}

@MainActor @Test func serverVersionIsReReadWhenTheServerComesBackFromOffline() async {
    let server = VersionedServer()
    let m = makeModel()
    m.configure(client: server.client())
    await poll(m)
    #expect(m.serverVersion == "0.29.0")

    server.down.value = true
    await poll(m)
    #expect(m.connection == .degraded(failures: 1))
    server.down.value = false
    await poll(m)
    #expect(server.versionReads.value == 1)

    server.down.value = true
    for _ in 0..<LiveModel.offlineAfterFailures { await poll(m) }
    #expect(m.connection == .offline(since: Date(timeIntervalSince1970: 1_000)))
    server.version.value = "0.30.0"
    server.down.value = false
    await poll(m)
    #expect(m.connection.isOnline)
    #expect(m.serverVersion == "0.30.0")
    #expect(server.versionReads.value == 2)
}

@MainActor @Test func lostVersionReadIsRetriedOnTheNextGoodPoll() async {
    let server = VersionedServer()
    server.versionLost.value = true
    let m = makeModel()
    m.configure(client: server.client())
    await poll(m)
    #expect(m.connection.isOnline)
    #expect(m.serverVersion == nil)
    server.versionLost.value = false
    await poll(m)
    #expect(m.serverVersion == "0.29.0")
    await poll(m)
    #expect(server.versionReads.value == 2)
}

@MainActor @Test func serverWithoutAVersionRouteIsAskedOnce() async {
    let reads = Shared(0)
    let client = stubbedClient { req in
        if req.url!.path == "/version" {
            reads.value += 1
            return (okResponse(req.url!, status: 404), Data())
        }
        return (okResponse(req.url!), Data(#"{"sessions":[]}"#.utf8))
    }
    let m = makeModel()
    m.configure(client: client)
    for _ in 0..<3 { await poll(m) }
    #expect(m.serverVersion == nil)
    #expect(reads.value == 1)
}

@MainActor @Test func versionIsClearedWhenTheReReadAnswersWithoutOne() async {
    let broken = Shared(false)
    let down = Shared(false)
    let client = stubbedClient { req in
        if req.url!.path == "/version" {
            if broken.value { return (okResponse(req.url!, status: 404), Data()) }
            return (okResponse(req.url!), Data(#"{"version":"0.29.0"}"#.utf8))
        }
        if down.value { return (okResponse(req.url!, status: 500), Data()) }
        return (okResponse(req.url!), Data(#"{"sessions":[]}"#.utf8))
    }
    let m = makeModel()
    m.configure(client: client)
    await poll(m)
    #expect(m.serverVersion == "0.29.0")

    down.value = true
    for _ in 0..<LiveModel.offlineAfterFailures { await poll(m) }
    broken.value = true
    down.value = false
    await poll(m)
    #expect(m.connection.isOnline)
    #expect(m.serverVersion == nil)
}

@MainActor @Test func newServerDropsTheOldVersion() async {
    let first = VersionedServer()
    let m = makeModel()
    m.configure(client: first.client())
    await poll(m)
    #expect(m.serverVersion == "0.29.0")

    let second = VersionedServer()
    second.version.value = "0.28.0"
    m.configure(client: second.client())
    #expect(m.serverVersion == nil)
    await poll(m)
    #expect(m.serverVersion == "0.28.0")
}

@Test func releaseVersionDropsDevBuildsAndPrefix() throws {
    func release(_ json: String) throws -> String? {
        try JSONDecoder().decode(VersionInfo.self, from: Data(json.utf8)).release
    }
    #expect(try release(#"{"version":"0.29.0","revision":"44143ca","dirty":true}"#) == "0.29.0")
    #expect(try release(#"{"version":"v0.29.0"}"#) == "0.29.0")
    #expect(try release(#"{"version":"dev","revision":"44143ca"}"#) == nil)
    #expect(try release(#"{"binary":"ember","revision":"6ad0339"}"#) == nil)
}

@MainActor @Test func clockDisabledIsReadWithTheVersionAndHidesTheClockMenu() async {
    let server = VersionedServer()
    server.clockOff.value = true
    let m = makeModel()
    m.configure(client: server.client())
    #expect(!m.isClockDisabled)
    await poll(m)
    await m.refreshMenuFeeds()
    #expect(m.isClockDisabled)
    #expect(server.healthReads.value == 0)
    let menu = MenuRows.clockMenu(clockDisabled: m.isClockDisabled, usage: m.usage, clockHealth: m.clockHealth,
                                  apps: .loaded([AppToggle(name: "claude", enabled: true)], at: Date()),
                                  matrixPower: nil)
    #expect(!menu.showsActions)
    #expect(menu.power.isEmpty)
    #expect(menu.apps.isEmpty)
    #expect(menu.disabledNotice != nil)
}

@MainActor @Test func clockDisabledFollowsARedeployOnReconnect() async {
    let server = VersionedServer()
    server.clockOff.value = true
    let m = makeModel()
    m.configure(client: server.client())
    await poll(m)
    #expect(m.isClockDisabled)

    server.down.value = true
    for _ in 0..<LiveModel.offlineAfterFailures { await poll(m) }
    server.clockOff.value = false
    server.down.value = false
    await poll(m)
    #expect(server.versionReads.value == 2)
    #expect(!m.isClockDisabled)
}

@MainActor @Test func newerClockHealthClearsAStaleClockDisabledAfterAFastRedeploy() async {
    let server = VersionedServer()
    server.clockOff.value = true
    let time = Shared<Double>(1_000)
    let m = makeModel(at: time)
    m.configure(client: server.client())
    await poll(m)
    #expect(m.isClockDisabled)

    server.clockOff.value = false
    server.healthDisabled.value = false
    time.value = 1_005
    await poll(m)
    await refreshClockHealth(m)
    #expect(server.versionReads.value == 1)
    #expect(!m.isClockDisabled)
}

@MainActor @Test func olderClockHealthDoesNotOverrideAFreshVersionRead() async {
    let server = VersionedServer()
    server.healthDisabled.value = false
    let time = Shared<Double>(1_000)
    let m = makeModel(at: time)
    m.configure(client: server.client())
    await poll(m)
    await refreshClockHealth(m)
    #expect(!m.isClockDisabled)

    server.down.value = true
    for _ in 0..<LiveModel.offlineAfterFailures { await poll(m) }
    server.clockOff.value = true
    server.down.value = false
    time.value = 1_010
    await poll(m)
    #expect(server.versionReads.value == 2)
    #expect(m.isClockDisabled)
}

@MainActor @Test func clockHealthSayingDisabledHidesTheClockMenu() async {
    let server = VersionedServer()
    server.healthDisabled.value = true
    let time = Shared<Double>(1_000)
    let m = makeModel(at: time)
    m.configure(client: server.client())
    await poll(m)
    #expect(!m.isClockDisabled)
    time.value = 1_001
    await refreshClockHealth(m)
    #expect(m.isClockDisabled)
    let menu = MenuRows.clockMenu(clockDisabled: m.isClockDisabled, usage: m.usage, clockHealth: m.clockHealth,
                                  apps: .loaded([AppToggle(name: "claude", enabled: true)], at: Date()),
                                  matrixPower: nil)
    #expect(!menu.showsActions)
    #expect(menu.apps.isEmpty)
}
