import Testing
@testable import AwtrixMenuKit

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
    #expect(SettingsKeys.source == "STATUS_SOURCE")
    #expect(SettingsKeys.serverURL == "STATUS_SERVER_URL")
    #expect(SettingsKeys.token == "STATUS_TOKEN")
    #expect(SettingsKeys.sourceColor == "STATUS_SOURCE_COLOR")
    #expect(SettingsKeys.contextNumber == "STATUS_CONTEXT_NUMBER_ENABLED")
}
