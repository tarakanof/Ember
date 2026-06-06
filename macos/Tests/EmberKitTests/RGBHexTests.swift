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

@Test func rgbProducesUppercaseHex() {
    #expect(RGB(r: 0x2e, g: 0xe8, b: 0x5e).hex == "#2EE85E")
    #expect(RGB(r: 0, g: 0, b: 0).hex == "#000000")
    #expect(RGB(r: 255, g: 255, b: 255).hex == "#FFFFFF")
}

@Test func hexRoundTripsThroughRGB() {
    let c = RGB(r: 1, g: 2, b: 3)
    #expect(RGB(hex: c.hex) == c)
}

@Test func sRGBQuantizerClampsAndRounds() {
    #expect(RGB(sRGB: 0, g: 0.5, b: 1) == RGB(r: 0, g: 128, b: 255))   // 127.5 → 128
    #expect(RGB(sRGB: -1, g: 2, b: 0.5) == RGB(r: 0, g: 255, b: 128))  // clamp out-of-range
}
