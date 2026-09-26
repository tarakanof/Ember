import Testing
import Foundation
@testable import EmberKit

/// Lock-guarded value the stub handler (URLProtocol thread) reads and writes.
private final class Shared<T: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: T
    init(_ v: T) { _value = v }
    var value: T {
        get { lock.withLock { _value } }
        set { lock.withLock { _value = newValue } }
    }
}

/// A server whose `/state` can fail and whose `/version` can change; counts
/// `/version` reads.
private final class VersionedServer: Sendable {
    let down = Shared(false)
    let versionLost = Shared(false)
    let version = Shared("0.29.0")
    let versionReads = Shared(0)

    func client() -> APIClient {
        stubbedClient { [self] req in
            switch req.url!.path {
            case "/state":
                if down.value { return (okResponse(req.url!, status: 500), Data()) }
                return (okResponse(req.url!), Data(#"{"sessions":[]}"#.utf8))
            case "/version":
                versionReads.value += 1
                if versionLost.value { throw URLError(.timedOut) }
                let body = #"{"binary":"ember","version":"\#(version.value)","revision":"44143ca","dirty":true}"#
                return (okResponse(req.url!), Data(body.utf8))
            default:
                return (okResponse(req.url!, status: 404), Data())
            }
        }
    }
}

/// Only `refreshNow` fetches: the coordinator's clock never advances.
@MainActor
private func makeModel() -> LiveModel {
    let clock = ManualClock()
    return LiveModel(now: { Date(timeIntervalSince1970: 1_000) }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
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
    // Dirty and the commit are left out: just the release.
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

    // A degraded blip isn't a restart: no re-read.
    server.down.value = true
    await poll(m)
    #expect(m.connection == .degraded(failures: 1))
    server.down.value = false
    await poll(m)
    #expect(server.versionReads.value == 1)

    // Offline, then back on a newer build: an upgrade restarts the server.
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
