import Testing
import Foundation
@testable import EmberKit

@Test func webURLFromTheServersClockAddress() {
    #expect(DeviceConfig(baseURL: "http://192.168.0.66").webURL?.absoluteString == "http://192.168.0.66")
    #expect(DeviceConfig(baseURL: "http://192.168.0.66/").webURL?.absoluteString == "http://192.168.0.66")
    #expect(DeviceConfig(baseURL: "http://awtrix.local:80").webURL?.absoluteString == "http://awtrix.local:80")
    #expect(DeviceConfig(baseURL: "https://192.168.0.66").webURL?.absoluteString == "https://192.168.0.66")
    #expect(DeviceConfig(baseURL: "  http://192.168.0.66  ").webURL?.absoluteString == "http://192.168.0.66")
}

@Test func webURLIsNilWithoutAnAddress() {
    #expect(DeviceConfig(baseURL: "").webURL == nil)
    #expect(DeviceConfig(baseURL: "   ").webURL == nil)
}

@Test func webURLRefusesNonHTTPSchemes() {
    for bad in ["file:///etc/passwd", "x-apple.systempreferences:foo", "javascript:alert(1)",
                "192.168.0.66", "not a url", "://broken"] {
        #expect(DeviceConfig(baseURL: bad).webURL == nil, "\(bad)")
    }
}
