import Testing
@testable import EmberKit

@Test func paletteHasTenColors() {
    #expect(AWTRIXPalette.colors.count == 10)
}

@Test func everyPresetHexIsCanonicalUppercase() {
    for c in AWTRIXPalette.colors {
        // Parses as a valid #RRGGBB...
        let rgb = RGB(hex: c.hex)
        #expect(rgb != nil, "\(c.name) hex \(c.hex) should parse")
        // ...and is already stored in the canonical uppercase form RGB emits.
        #expect(rgb?.hex == c.hex, "\(c.name) hex \(c.hex) should be canonical")
    }
}

@Test func paletteIncludesWhiteAndBlack() {
    let hexes = AWTRIXPalette.colors.map(\.hex)
    #expect(hexes.contains("#FFFFFF"))
    #expect(hexes.contains("#000000"))
}

@Test func presetHexesAreUnique() {
    let hexes = AWTRIXPalette.colors.map(\.hex)
    #expect(Set(hexes).count == hexes.count)
}
