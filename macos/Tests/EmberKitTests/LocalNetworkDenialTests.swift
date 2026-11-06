import Testing
import Foundation
import Network
@testable import EmberKit

private func networkDown(_ code: URLError.Code = .notConnectedToInternet) -> URLError {
    URLError(code, userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 50])
}

@Test func dnsNoAuthAndPolicyDeniedAreRefusals() {
    #expect(LocalNetworkDenial.isDenied(NWError.dns(-65555)))
    #expect(LocalNetworkDenial.isDenied(NWError.dns(-65570)))
    #expect(!LocalNetworkDenial.isDenied(NWError.dns(-65537)))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ECONNREFUSED), host: "192.168.0.2"))
}

@Test func networkDownCountsOnlyForALANHost() {
    #expect(LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN), host: "192.168.0.2", pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN), host: "example.com", pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN), pathStatus: .satisfied))

    #expect(LocalNetworkDenial.isDenied(networkDown(), host: "192.168.0.2", pathStatus: .satisfied))
    #expect(LocalNetworkDenial.isDenied(networkDown(.cannotConnectToHost), host: "ember.local", pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(networkDown(), host: "ember.example.com", pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(networkDown(), host: nil, pathStatus: .satisfied))
}

@Test(arguments: [NWPath.Status.unsatisfied, .requiresConnection, nil])
func networkDownWithoutANetworkIsNotARefusal(status: NWPath.Status?) {
    #expect(!LocalNetworkDenial.isDenied(networkDown(), host: "192.168.0.2", pathStatus: status))
    #expect(!LocalNetworkDenial.isDenied(NWError.posix(.ENETDOWN), host: "192.168.0.2", pathStatus: status))
    #expect(LocalNetworkDenial.isDenied(NWError.dns(-65555), pathStatus: status))
}

@Test func otherURLErrorsAreNotRefusals() {
    #expect(!LocalNetworkDenial.isDenied(URLError(.timedOut), host: "192.168.0.2", pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(URLError(.notConnectedToInternet), host: "192.168.0.2",
                                         pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(
        URLError(.notConnectedToInternet, userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 61]),
        host: "192.168.0.2", pathStatus: .satisfied))
    #expect(!LocalNetworkDenial.isDenied(APIError.transport("x"), host: "192.168.0.2", pathStatus: .satisfied))
}

@Test func streamKeysAreReadFromTheUnderlyingError() {
    let underlying = NSError(domain: "kCFErrorDomainCFNetwork", code: -1009,
                             userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 50])
    let e = URLError(.notConnectedToInternet, userInfo: [NSUnderlyingErrorKey: underlying])
    #expect(LocalNetworkDenial.isDenied(e, host: "10.0.0.5", pathStatus: .satisfied))
}

@Test func pathVerdictDecides() {
    let rule = { (path: LocalNetworkDenial.PathVerdict?, status: NWPath.Status?) in
        LocalNetworkDenial.isDenied(urlCode: NSURLErrorNotConnectedToInternet, streamDomain: 1, streamCode: 50,
                                    path: path, host: "192.168.0.2", pathStatus: status)
    }
    #expect(rule(.localNetworkDenied, .satisfied))
    #expect(rule(.localNetworkDenied, .unsatisfied))
    #expect(!rule(.other, .satisfied))
    #expect(rule(nil, .satisfied))
    #expect(!rule(nil, .unsatisfied))
    #expect(LocalNetworkDenial.isDenied(urlCode: NSURLErrorTimedOut, streamDomain: nil, streamCode: nil,
                                        path: .localNetworkDenied, host: "example.com", pathStatus: nil))
}

@Test(arguments: [
    ("192.168.0.2", true), ("10.1.2.3", true), ("172.16.0.1", true), ("172.31.255.1", true),
    ("169.254.10.1", true), ("ember.local", true), ("nas", true), ("fe80::1%en0", true),
    ("[fd00::1]", true), ("172.32.0.1", false), ("8.8.8.8", false), ("example.com", false),
    ("localhost", false), ("2001:db8::1", false), ("", false),
    ("nas.lan", true), ("ember.home.arpa", true), ("ember.home.arpa.", true), ("clock.internal", true),
    ("ember.local.", true), ("localhost.", false), ("plan.example.com", false), ("internal.example.com", false),
    ("::ffff:192.168.0.2", true), ("[::ffff:10.0.0.1]", true), ("::ffff:8.8.8.8", false),
])
func lanHosts(host: String, lan: Bool) {
    #expect(LocalNetworkDenial.isLANHost(host) == lan)
}

@Test func apiClientMapsARefusalToLocalNetworkDenied() async {
    let client = stubbedClient { _ in throw networkDown() }
    await #expect(throws: APIError.localNetworkDenied) { try await client.send("GET", "/state") }
}

@Test func apiClientWithoutANetworkKeepsTransport() async {
    let client = stubbedClient(pathStatus: .unsatisfied) { _ in throw networkDown() }
    do {
        try await client.send("GET", "/state")
        Issue.record("expected a throw")
    } catch let e as APIError {
        guard case .transport = e else { Issue.record("got \(e)"); return }
    } catch {
        Issue.record("got \(error)")
    }
}

@Test func apiClientKeepsOtherFailuresAsTransport() async {
    let client = stubbedClient { _ in throw URLError(.networkConnectionLost) }
    do {
        try await client.send("GET", "/state")
        Issue.record("expected a throw")
    } catch let e as APIError {
        guard case .transport = e else { Issue.record("got \(e)"); return }
    } catch {
        Issue.record("got \(error)")
    }
}

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
    let online = ConnectionHealth.online(since: since).subtitle(serverHost: "h", offlineReason: .localNetworkDenied)
    #expect(String(localized: online) == "Connected to h")
}

@Test func refusedBrowseAsksForAccessInsteadOfFailing() {
    #expect(BonjourClockBrowser.browseState(for: .failed(.dns(-65555))) == .denied)
    #expect(BonjourClockBrowser.browseState(for: .failed(.dns(-65537))) == .failed)
    #expect(BonjourClockBrowser.browseState(for: .ready) == .ready)
    #expect(BonjourClockBrowser.step(for: .waiting(.dns(-65555))) == .denied)
}
