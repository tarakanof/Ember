import Testing
@testable import EmberKit

@MainActor
@Test func discoveryURLIPv4Unbracketed() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "192.168.0.14", port: 3627)
    #expect(f.urlString == "http://192.168.0.14:3627")
}

@MainActor
@Test func discoveryURLHostnameUnbracketed() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "ember.local", port: 3627)
    #expect(f.urlString == "http://ember.local:3627")
}

@MainActor
@Test func discoveryURLIPv6Bracketed() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "2001:db8::1", port: 3627)
    #expect(f.urlString == "http://[2001:db8::1]:3627")
}

@MainActor
@Test func discoveryURLIPv6LinkLocalZoneEncoded() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "fe80::1%en0", port: 3627)
    #expect(f.urlString == "http://[fe80::1%25en0]:3627")
}

@Test func discoveryHoldCountStartsOnFirstAndStopsOnLast() {
    var h = ServerDiscovery.HoldCount()
    let edges = [h.acquire(), h.acquire(), h.release(), h.release()]
    #expect(edges == [true, false, false, true])
    #expect(h.count == 0)
}

@Test func discoveryHoldCountIgnoresExtraRelease() {
    var h = ServerDiscovery.HoldCount()
    let stopped = h.release()
    #expect(!stopped)
    #expect(h.count == 0)
    let started = h.acquire()
    #expect(started)
}
