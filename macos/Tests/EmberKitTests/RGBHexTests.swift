import Testing
@testable import EmberKit

@Test func parsesSixDigitHex() {
    #expect(RGB(hex: "#2ee85e") == RGB(r: 0x2e, g: 0xe8, b: 0x5e))
    #expect(RGB(hex: "#FFFFFF") == RGB(r: 255, g: 255, b: 255))
    #expect(RGB(hex: "#000000") == RGB(r: 0, g: 0, b: 0))
}

@Test func rejectsBadHex() {
    #expect(RGB(hex: "") == nil)
    #expect(RGB(hex: "2ee85e") == nil)
    #expect(RGB(hex: "#fff") == nil)
    #expect(RGB(hex: "#gggggg") == nil)
    #expect(RGB(hex: "#12345") == nil)
}

@Test func settingsKeysMatchProducerEnv() {
    #expect(SettingsKeys.source == "EMBER_SOURCE")
    #expect(SettingsKeys.serverURL == "EMBER_SERVER_URL")
    #expect(SettingsKeys.token == "EMBER_TOKEN")
    #expect(SettingsKeys.sourceColor == "EMBER_SOURCE_COLOR")
    #expect(SettingsKeys.contextNumber == "EMBER_CONTEXT_NUMBER_ENABLED")
}
