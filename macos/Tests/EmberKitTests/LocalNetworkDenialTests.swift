import Testing
import Foundation
import Network
@testable import EmberKit

/// The error URLSession gave the rebuilt ad-hoc app on-device: -1009 over
/// POSIX ENETDOWN (`HTTP load failed … error code: -1009 [1:50]`).
private func networkDown(_ code: URLError.Code = .notConnectedToInternet) -> URLError {
    URLError(code, userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 50])
}

// MARK: Classifier

@Test func dnsNoAuthAndPolicyDeniedAreRefusals() {
    #expect(LocalNetworkDenial.isDenied(NWError.dns(-65555)))
    #expect(LocalNetworkDenial.isDenied(NWError.dns(-65570)))
    #expect(!LocalNetworkDenial.isDenied(NWError.dns(-65537)))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ECONNREFUSED), host: "192.168.0.2"))
}

// Wi-Fi off gives ENETDOWN for any host; only a LAN host means a refusal.
@Test func networkDownCountsOnlyForALANHost() {
    #expect(LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN), host: "192.168.0.2"))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN), host: "example.com"))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN)))

    #expect(LocalNetworkDenial.isDenied(networkDown(), host: "192.168.0.2"))
    #expect(LocalNetworkDenial.isDenied(networkDown(.cannotConnectToHost), host: "ember.local"))
    #expect(!LocalNetworkDenial.isDenied(networkDown(), host: "ember.example.com"))
    #expect(!LocalNetworkDenial.isDenied(networkDown(), host: nil))
}

@Test func otherURLErrorsAreNotRefusals() {
    #expect(!LocalNetworkDenial.isDenied(URLError(.timedOut), host: "192.168.0.2"))
    #expect(!LocalNetworkDenial.isDenied(URLError(.notConnectedToInternet), host: "192.168.0.2"))
    #expect(!LocalNetworkDenial.isDenied(
        URLError(.notConnectedToInternet, userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 61]),
        host: "192.168.0.2"))
    #expect(!LocalNetworkDenial.isDenied(APIError.transport("x"), host: "192.168.0.2"))
}

// The stream keys can sit on the underlying CFNetwork error only.
@Test func streamKeysAreReadFromTheUnderlyingError() {
    let underlying = NSError(domain: "kCFErrorDomainCFNetwork", code: -1009,
                             userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 50])
    let e = URLError(.notConnectedToInternet, userInfo: [NSUnderlyingErrorKey: underlying])
    #expect(LocalNetworkDenial.isDenied(e, host: "10.0.0.5"))
}

// The failed path's reason, when known, decides over the ENETDOWN guess.
@Test func pathVerdictDecides() {
    let rule = { (path: LocalNetworkDenial.PathVerdict?) in
        LocalNetworkDenial.isDenied(urlCode: NSURLErrorNotConnectedToInternet, streamDomain: 1, streamCode: 50,
                                    path: path, host: "192.168.0.2")
    }
    #expect(rule(.localNetworkDenied))
    #expect(!rule(.other))
    #expect(rule(nil))
    #expect(LocalNetworkDenial.isDenied(urlCode: NSURLErrorTimedOut, streamDomain: nil, streamCode: nil,
                                        path: .localNetworkDenied, host: "example.com"))
}

@Test(arguments: [
    ("192.168.0.2", true), ("10.1.2.3", true), ("172.16.0.1", true), ("172.31.255.1", true),
    ("169.254.10.1", true), ("ember.local", true), ("nas", true), ("fe80::1%en0", true),
    ("[fd00::1]", true), ("172.32.0.1", false), ("8.8.8.8", false), ("example.com", false),
    ("localhost", false), ("2001:db8::1", false), ("", false),
])
func lanHosts(host: String, lan: Bool) {
    #expect(LocalNetworkDenial.isLANHost(host) == lan)
}

// MARK: Error mapping

@Test func apiClientMapsARefusalToLocalNetworkDenied() async {
    let client = stubbedClient { _ in throw networkDown() }
    await #expect(throws: APIError.localNetworkDenied) { try await client.send("GET", "/state") }
}

@Test func apiClientKeepsOtherFailuresAsTransport() async {
    let client = stubbedClient { _ in throw URLError(.timedOut) }
    do {
        try await client.send("GET", "/state")
        Issue.record("expected a throw")
    } catch let e as APIError {
        guard case .transport = e else { Issue.record("got \(e)"); return }
    } catch {
        Issue.record("got \(error)")
    }
}

// A refused request never left the Mac: a reminder fire can be retried.
@Test func refusedIdempotentPostIsNotSent() async {
    let client = stubbedClient { _ in throw networkDown() }
    await #expect(throws: RequestNotSent(underlying: .localNetworkDenied)) {
        try await client.postIdempotent("/v1/reminders/fire", body: ["x": 1], key: "k")
    }
}

@Test func refusalReachesEveryReachabilityReport() {
    #expect(FeedError(APIError.localNetworkDenied) == .localNetworkDenied)
    #expect(FeedError(RequestNotSent(underlying: .localNetworkDenied)) == .localNetworkDenied)
    #expect(FeedError.localNetworkDenied.isUnreachable)
    #expect(FeedError.offline.isUnreachable)
    #expect(!FeedError.unauthorized.isUnreachable)
    #expect(String(localized: FeedError.localNetworkDenied.message) == "Local Network access is off for Ember")
    #expect(ConnectionProbe.result(for: APIError.localNetworkDenied) == .localNetworkDenied)
}

@Test func offlineHeaderAndSubtitleNameTheRefusal() {
    let since = Date(timeIntervalSince1970: 0)
    let header = MenuRows.header(connection: .offline(since: since), hasEverLoaded: true, winning: nil,
                                 offlineReason: .localNetworkDenied)
    #expect(String(localized: header.title) == "Offline — Local Network access is off for Ember")
    let plain = MenuRows.header(connection: .offline(since: since), hasEverLoaded: false, winning: nil,
                                offlineReason: .offline)
    #expect(String(localized: plain.title) == "Offline")
    let subtitle = ConnectionHealth.offline(since: since).subtitle(serverHost: "192.168.0.2",
                                                                   offlineReason: .localNetworkDenied)
    #expect(String(localized: subtitle) == "Offline: Local Network access is off")
    // Online, a stale reason is ignored.
    let online = ConnectionHealth.online(since: since).subtitle(serverHost: "h", offlineReason: .localNetworkDenied)
    #expect(String(localized: online) == "Connected to h")
}

// MARK: Browses

@Test func refusedBrowseAsksForAccessInsteadOfFailing() {
    #expect(BonjourClockBrowser.browseState(for: .failed(.dns(-65555))) == .waiting)
    #expect(BonjourClockBrowser.browseState(for: .failed(.dns(-65537))) == .failed)
    #expect(BonjourClockBrowser.browseState(for: .ready) == .ready)
    #expect(BonjourClockBrowser.step(for: .waiting(.dns(-65555))) == .denied)
}
