import SwiftUI
import EmberKit

/// A native ColorPicker bound to a "#RRGGBB" string. Displays the parsed colour
/// (falls back to white for an unparseable/empty hex) and writes the picked
/// colour back as uppercase "#RRGGBB" on change.
struct ColorHexPicker: View {
    let title: String
    var symbol: String? = nil
    var tint: Color = .gray
    @Binding var hex: String

    private var colorBinding: Binding<Color> {
        Binding(
            get: { (RGB(hex: hex).map(Color.init)) ?? .white },
            set: { hex = RGB($0).hex }
        )
    }

    var body: some View {
        ColorPicker(selection: colorBinding, supportsOpacity: false) {
            if let symbol {
                RowLabel(title, symbol: symbol, tint: tint)
            } else {
                Text(title)
            }
        }
    }
}
