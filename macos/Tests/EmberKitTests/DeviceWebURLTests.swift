import Testing
import Foundation
@testable import EmberKit

// The clock serves its own web UI at the root of the address the server is
// driving (verified: 200 text/html "AWTRIX NG"). The Device tab opens it, so
// the address has to survive the trip into a URL — including the forms the
// server can hand back.
@Test func webURLFromTheServersClockAddress() {
    #expect(DeviceConfig(baseURL: "http://192.168.0.66").webURL?.absoluteString == "http://192.168.0.66")
    #expect(DeviceConfig(baseURL: "http://192.168.0.66/").webURL?.absoluteString == "http://192.168.0.66")
    #expect(DeviceConfig(baseURL: "http://awtrix.local:80").webURL?.absoluteString == "http://awtrix.local:80")
    #expect(DeviceConfig(baseURL: "https://192.168.0.66").webURL?.absoluteString == "https://192.168.0.66")
    #expect(DeviceConfig(baseURL: "  http://192.168.0.66  ").webURL?.absoluteString == "http://192.168.0.66")
}

// No address (clock not configured / never discovered) means no button.
@Test func webURLIsNilWithoutAnAddress() {
    #expect(DeviceConfig(baseURL: "").webURL == nil)
    #expect(DeviceConfig(baseURL: "   ").webURL == nil)
}

// Anything that isn't a plain http(s) address is refused rather than handed to
// NSWorkspace — opening a file:// or a custom scheme from a value that arrived
// over the network is not something a button should do.
@Test func webURLRefusesNonHTTPSchemes() {
    for bad in ["file:///etc/passwd", "x-apple.systempreferences:foo", "javascript:alert(1)",
                "192.168.0.66", "not a url", "://broken"] {
        #expect(DeviceConfig(baseURL: bad).webURL == nil, "\(bad)")
    }
}
