import Testing
import Foundation
@testable import EmberKit

// MARK: Fingerprint — mirrors the server's internal/discovery probe rules

// A non-empty uid is what makes an _http._tcp host an AWTRIX clock.
@Test func clockFingerprintAcceptsNonEmptyUID() {
    let body = #"{"uid":"116ae8","version":"0.98","bat":100,"ram":118772}"#
    let c = ClockDiscovery.candidate(host: "awtrix_116ae8.local.",
                                     baseURL: "http://10.0.0.9:80",
                                     status: 200, body: Data(body.utf8))
    #expect(c?.uid == "116ae8")
    #expect(c?.version == "0.98")
    #expect(c?.host == "awtrix_116ae8.local.")
    #expect(c?.baseURL == "http://10.0.0.9:80")
}

// An ordinary HTTP server that happens to answer /api/stats with JSON is not a
// clock unless it carries a uid.
@Test func clockFingerprintRejectsEmptyUID() {
    let body = #"{"uid":"","version":"0.98"}"#
    #expect(ClockDiscovery.candidate(host: "h", baseURL: "http://h:80",
                                     status: 200, body: Data(body.utf8)) == nil)
}

@Test func clockFingerprintRejectsMissingUID() {
    let body = #"{"version":"0.98"}"#
    #expect(ClockDiscovery.candidate(host: "h", baseURL: "http://h:80",
                                     status: 200, body: Data(body.utf8)) == nil)
}

@Test func clockFingerprintRejectsNon200() {
    let body = #"{"uid":"116ae8"}"#
    #expect(ClockDiscovery.candidate(host: "h", baseURL: "http://h:80",
                                     status: 404, body: Data(body.utf8)) == nil)
}

@Test func clockFingerprintRejectsUndecodableBody() {
    #expect(ClockDiscovery.candidate(host: "h", baseURL: "http://h:80",
                                     status: 200, body: Data("<html>".utf8)) == nil)
}

// Older firmware may omit the version; the clock is still usable.
@Test func clockFingerprintToleratesMissingVersion() {
    let c = ClockDiscovery.candidate(host: "h", baseURL: "http://h:80",
                                     status: 200, body: Data(#"{"uid":"u"}"#.utf8))
    #expect(c?.version == "")
}

// MARK: Base URL shaping — byte-identical to the server's baseURLFor

// The port is always explicit so a clock found here is the same string the
// server would report for it (and the two lists de-dupe cleanly).
@Test func clockBaseURLAlwaysCarriesPort() {
    #expect(ClockDiscovery.baseURL(host: "10.0.0.9", port: 80) == "http://10.0.0.9:80")
}

// Bonjour reports port 0 for a service with no port record; AWTRIX serves HTTP.
@Test func clockBaseURLDefaultsPortEightyWhenAbsent() {
    #expect(ClockDiscovery.baseURL(host: "10.0.0.9", port: 0) == "http://10.0.0.9:80")
}

@Test func clockBaseURLBracketsIPv6() {
    #expect(ClockDiscovery.baseURL(host: "fe80::1%en0", port: 80) == "http://[fe80::1%25en0]:80")
}

// MARK: De-dup and ordering

// The same clock resolves through several endpoints; uid is the device identity.
@Test func clockMergeDeDupesByUID() {
    let a = DiscoveredClock(host: "awtrix.local.", baseURL: "http://10.0.0.9:80", uid: "u", version: "0.98")
    let b = DiscoveredClock(host: "awtrix.local.", baseURL: "http://10.0.0.9:8080", uid: "u", version: "0.98")
    let list = ClockDiscovery.merged(ClockDiscovery.merged([], adding: a), adding: b)
    #expect(list.count == 1)
    #expect(list[0].baseURL == "http://10.0.0.9:80")
}

// Probes land in whatever order the LAN answers; the list must not reshuffle.
@Test func clockMergeOrdersByHost() {
    let z = DiscoveredClock(host: "zulu.local.", baseURL: "http://10.0.0.9:80", uid: "z", version: "")
    let a = DiscoveredClock(host: "alpha.local.", baseURL: "http://10.0.0.5:80", uid: "a", version: "")
    let list = ClockDiscovery.merged(ClockDiscovery.merged([], adding: z), adding: a)
    #expect(list.map(\.host) == ["alpha.local.", "zulu.local."])
}

// MARK: Resolution de-dup (NWBrowser replays the whole result set)

// Each Bonjour instance is connected to once, no matter how many times the
// browse replays it — otherwise a busy _http._tcp LAN opens N connections per
// callback.
@MainActor
@Test func clockDiscoveryClaimsEachServiceOnce() {
    let d = ClockDiscovery()
    #expect(d.claim("awtrix_116ae8._http._tcp.local.") == true)
    #expect(d.claim("awtrix_116ae8._http._tcp.local.") == false)
    #expect(d.claim("printer._http._tcp.local.") == true)
}

// A fresh scan re-resolves everything: stale claims must not hide a clock that
// failed to resolve last time.
@MainActor
@Test func clockDiscoveryClearsClaimsOnStop() {
    let d = ClockDiscovery()
    #expect(d.claim("awtrix._http._tcp.local.") == true)
    d.stop()
    #expect(d.claim("awtrix._http._tcp.local.") == true)
}

// MARK: Probe request shaping (stubbed transport — no LAN traffic)

@Test func clockProbeGetsAPIStatsAndReturnsCandidate() async {
    let host = "stub-\(UUID().uuidString.lowercased()).local"
    StubURLProtocol.register(host: host) { req in
        #expect(req.httpMethod == "GET")
        #expect(req.url?.path == "/api/stats")
        return (okResponse(req.url!), Data(#"{"uid":"116ae8","version":"0.98"}"#.utf8))
    }
    let c = await ClockDiscovery.probe(host: "awtrix.local.",
                                       baseURL: "http://\(host):80",
                                       session: stubSession())
    #expect(c?.uid == "116ae8")
    #expect(c?.baseURL == "http://\(host):80")
}

@Test func clockProbeReturnsNilForNonClock() async {
    let host = "stub-\(UUID().uuidString.lowercased()).local"
    StubURLProtocol.register(host: host) { req in
        (okResponse(req.url!, status: 404), Data())
    }
    let c = await ClockDiscovery.probe(host: "printer.local.",
                                       baseURL: "http://\(host):80",
                                       session: stubSession())
    #expect(c == nil)
}

// MARK: Failure classification — who is unreachable, the server or the clock

// The server maps every clock-side proxy failure to 502; that is the only case
// clock discovery can fix.
@Test func deviceFailureTreats502AsClockUnreachable() {
    #expect(DeviceFailure.classify(APIError.http(status: 502, body: "clock returned 500")) == .clockUnreachable)
}

// No server URL yet: finding a clock would succeed and then fail to save it, so
// this must send the user to Connection instead of to discovery.
@Test func deviceFailureTreatsNotConfiguredAsServerUnreachable() {
    #expect(DeviceFailure.classify(APIError.notConfigured) == .serverUnreachable)
}

// Server down / VPN off — same reasoning as notConfigured.
@Test func deviceFailureTreatsTransportAsServerUnreachable() {
    #expect(DeviceFailure.classify(APIError.transport("could not connect")) == .serverUnreachable)
}

@Test func deviceFailureTreats401AsUnauthorized() {
    #expect(DeviceFailure.classify(APIError.http(status: 401, body: "")) == .unauthorized)
}

// An old server missing the route, or a malformed body, is neither — and must
// not masquerade as an unreachable clock.
@Test func deviceFailureTreatsOtherStatusesAsOther() {
    #expect(DeviceFailure.classify(APIError.http(status: 404, body: "")) == .other)
    #expect(DeviceFailure.classify(APIError.decoding("bad json")) == .other)
    #expect(DeviceFailure.classify(URLError(.badURL)) == .other)
}

// MARK: Config push — pointing the server at a clock the app found

private struct DeviceConfigBody: Decodable { let base_url: String }

@Test func setConfigPutsBaseURLToDeviceConfig() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "PUT")
        #expect(req.url?.path == "/v1/device/config")
        #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer t")
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let obj = try JSONDecoder().decode(DeviceConfigBody.self, from: body)
        #expect(obj.base_url == "http://10.0.0.9:80")
        return (okResponse(req.url!), Data())
    }
    try await DeviceService(client: client).setConfig(baseURL: "http://10.0.0.9:80")
}
