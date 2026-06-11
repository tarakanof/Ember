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

// The colour picker (ColorHexPicker / AWTRIXPalette selection-ring match) leans on
// this parser's exact "#RRGGBB only" contract — guard the shapes it must reject and
// the lowercase→uppercase canonicalisation the swatch comparison relies on.
@Test func hexParserGuardsLengthAlphaAndCase() {
    #expect(RGB(hex: "#80FF0000") == nil)        // 8-digit / alpha-prefixed not supported
    #expect(RGB(hex: "#FF0000FF") == nil)        // 8-digit / alpha-suffixed not supported
    #expect(RGB(hex: "#FFF") == nil)             // 3-digit shorthand is not expanded
    #expect(RGB(hex: "#1234567") == nil)         // 7 digits is one too many
    #expect(RGB(hex: "#FF 000") == nil)          // 7 chars but embedded non-hex
    #expect(RGB(hex: "#abcdef")?.hex == "#ABCDEF") // lowercase accepted, canonical uppercase out
}

@Test func settingsKeysMatchProducerEnv() {
    #expect(SettingsKeys.source == "EMBER_SOURCE")
    #expect(SettingsKeys.serverURL == "EMBER_SERVER_URL")
    #expect(SettingsKeys.token == "EMBER_TOKEN")
    #expect(SettingsKeys.sourceColor == "EMBER_SOURCE_COLOR")
    #expect(SettingsKeys.contextNumber == "EMBER_CONTEXT_NUMBER_ENABLED")
    #expect(SettingsKeys.sourceCard == "EMBER_SOURCE_CARD")
    #expect(SettingsKeys.sessionBar == "EMBER_SESSION_BAR")
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
