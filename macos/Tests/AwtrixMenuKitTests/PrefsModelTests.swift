import Testing
@testable import AwtrixMenuKit

@Test func defaultsMatchOldMenu() {
    let p = MenuPrefs.default
    #expect(p.appIcon == "multicolor-rgb")
    #expect(p.trayClaudeGlyph == "aicode")
    #expect(p.trayCodexGlyph == "code")
    #expect(p.trayIdleGlyph == "awtrix")
}

@Test func validateReplacesUnknownWithDefault() {
    var p = MenuPrefs(appIcon: "bogus", trayClaudeGlyph: "code", trayCodexGlyph: "nope", trayIdleGlyph: "awtrix")
    p = p.validated()
    #expect(p.appIcon == "multicolor-rgb")
    #expect(p.trayClaudeGlyph == "code")
    #expect(p.trayCodexGlyph == "code")
    #expect(p.trayIdleGlyph == "awtrix")
}

@Test func glyphForToolMapping() {
    let p = MenuPrefs.default
    #expect(glyphForTool("claude", p) == "aicode")
    #expect(glyphForTool("codex", p) == "code")
    #expect(glyphForTool("", p) == "awtrix")
    #expect(glyphForTool("unknown", p) == "awtrix")
}

@Test func stateColorMatchesPalette() {
    #expect(stateColorRGB("running") == RGB(r: 0x2e, g: 0xe8, b: 0x5e))
    #expect(stateColorRGB("waiting") == RGB(r: 0xff, g: 0xc1, b: 0x4d))
    #expect(stateColorRGB("error")   == RGB(r: 0xff, g: 0x3a, b: 0x3a))
    #expect(stateColorRGB("done")    == RGB(r: 0x4f, g: 0xa9, b: 0xff))
    #expect(stateColorRGB("idle")    == RGB(r: 0x88, g: 0x88, b: 0x88))
}

@Test func listsExposed() {
    #expect(appIconPalettes == ["multicolor-rgb", "cyan-green", "warm-amber", "aurora"])
    #expect(trayGlyphs == ["awtrix", "awtrix-screen", "aicode", "aicode-chat", "code", "code-hex", "pomodoro"])
}
