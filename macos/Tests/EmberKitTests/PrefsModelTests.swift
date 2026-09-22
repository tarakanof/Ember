import Testing
@testable import EmberKit

@Test func defaultsMatchMenu() {
    let p = MenuPrefs.default
    #expect(p.appIcon == "bot")
    #expect(p.trayClaudeGlyph == "claude")
    #expect(p.trayCodexGlyph == "codex")
    #expect(p.trayIdleGlyph == "ember-e-pixel")
    #expect(p.trayStyle == "bot")
    #expect(p.trayTint == "color")
}

@Test func validateReplacesUnknownTrayTint() {
    var p = MenuPrefs.default
    p.trayTint = "rainbow"
    #expect(p.validated().trayTint == "color")
    p.trayTint = "mono"
    #expect(p.validated().trayTint == "mono")
}

@Test func validateReplacesUnknownTrayStyle() {
    var p = MenuPrefs.default
    p.trayStyle = "sparkles"
    #expect(p.validated().trayStyle == "bot")
    p.trayStyle = "glyphs"
    #expect(p.validated().trayStyle == "glyphs")
}

@Test func validateReplacesUnknownWithDefault() {
    var p = MenuPrefs(appIcon: "bogus", trayClaudeGlyph: "codex", trayCodexGlyph: "nope", trayIdleGlyph: "ember")
    p = p.validated()
    #expect(p.appIcon == "bot")
    #expect(p.trayClaudeGlyph == "codex")          // valid glyph kept
    #expect(p.trayCodexGlyph == "codex")           // "nope" → default codex
    #expect(p.trayIdleGlyph == "ember")            // valid glyph kept
}

@Test func glyphForToolMapping() {
    let p = MenuPrefs.default
    #expect(glyphForTool("claude", p) == "claude")
    #expect(glyphForTool("codex", p) == "codex")
    #expect(glyphForTool("", p) == "ember-e-pixel")
    #expect(glyphForTool("unknown", p) == "ember-e-pixel")
}

@Test func stateColorMatchesPalette() {
    #expect(stateColorRGB("running") == RGB(r: 0x2e, g: 0xe8, b: 0x5e))
    #expect(stateColorRGB("waiting") == RGB(r: 0xff, g: 0xc1, b: 0x4d))
    #expect(stateColorRGB("error")   == RGB(r: 0xff, g: 0x3a, b: 0x3a))
    #expect(stateColorRGB("done")    == RGB(r: 0x4f, g: 0xa9, b: 0xff))
    #expect(stateColorRGB("idle")    == RGB(r: 0x88, g: 0x88, b: 0x88))
}

@Test func listsExposed() {
    #expect(appIconPalettes == ["bot", "spark", "pixel-e"])
    #expect(trayStyles == ["bot", "glyphs"])
    #expect(trayGlyphs == ["ember", "ember-e", "ember-e-pixel", "claude", "codex", "pomodoro", "coffee"])
}
