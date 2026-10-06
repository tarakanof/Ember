public struct PaletteColor: Sendable, Equatable {
    public let name: String
    public let hex: String

    public init(name: String, hex: String) {
        self.name = name
        self.hex = hex
    }
}

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
