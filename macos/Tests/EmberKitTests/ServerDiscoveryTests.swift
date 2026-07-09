import Testing
@testable import EmberKit

// IPv4 literals are used verbatim — no brackets.
@MainActor
@Test func discoveryURLIPv4Unbracketed() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "192.168.0.14", port: 3627)
    #expect(f.urlString == "http://192.168.0.14:3627")
}

// Hostnames are used verbatim — no brackets.
@MainActor
@Test func discoveryURLHostnameUnbracketed() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "ember.local", port: 3627)
    #expect(f.urlString == "http://ember.local:3627")
}

// Global IPv6 literals must be bracketed to form a valid URL.
@MainActor
@Test func discoveryURLIPv6Bracketed() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "2001:db8::1", port: 3627)
    #expect(f.urlString == "http://[2001:db8::1]:3627")
}

// Link-local IPv6 keeps its zone id, percent-encoded per RFC 6874:
// fe80::1%en0 -> http://[fe80::1%25en0]:3627
@MainActor
@Test func discoveryURLIPv6LinkLocalZoneEncoded() {
    let f = ServerDiscovery.Found(id: "a", name: "a", host: "fe80::1%en0", port: 3627)
    #expect(f.urlString == "http://[fe80::1%25en0]:3627")
}
