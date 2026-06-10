/// A named color usable as a quick-pick swatch. `hex` is a canonical
/// uppercase `"#RRGGBB"` string (the form `RGB.hex` emits).
public struct PaletteColor: Sendable, Equatable {
    public let name: String
    public let hex: String

    public init(name: String, hex: String) {
        self.name = name
        self.hex = hex
    }
}

/// Curated common colors for the AWTRIX LED matrix — pure-ish primaries that
/// render cleanly on the 32x8 panel. Surfaced as quick-pick swatches in the
/// color picker popover.
public enum AWTRIXPalette {
    public static let colors: [PaletteColor] = [
        PaletteColor(name: "White",  hex: "#FFFFFF"),
        PaletteColor(name: "Red",    hex: "#FF0000"),
        PaletteColor(name: "Orange", hex: "#FF7F00"),
        PaletteColor(name: "Amber",  hex: "#FFC400"),
        PaletteColor(name: "Green",  hex: "#00C800"),
        PaletteColor(name: "Teal",   hex: "#00C8C8"),
        PaletteColor(name: "Blue",   hex: "#2D7FF9"),
        PaletteColor(name: "Purple", hex: "#C800FF"),
        PaletteColor(name: "Gray",   hex: "#808080"),
        PaletteColor(name: "Black",  hex: "#000000"),
    ]
}
