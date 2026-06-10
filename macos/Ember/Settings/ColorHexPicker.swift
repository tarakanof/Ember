import SwiftUI
import EmberKit

/// A compact colour-swatch button bound to a "#RRGGBB" string. Tapping opens a
/// popover with the AWTRIX common-colour swatches plus a native colour-wheel
/// button (the macOS colour panel). Falls back to white for an unparseable or
/// empty hex; every path writes the value back as uppercase "#RRGGBB".
struct ColorHexPicker: View {
    let title: String
    var symbol: String? = nil
    var tint: Color = .gray
    @Binding var hex: String

    @State private var showPopover = false

    private var colorBinding: Binding<Color> {
        Binding(
            get: { (RGB(hex: hex).map(Color.init)) ?? .white },
            // The wheel can produce wide-gamut colours, but the AWTRIX matrix is
            // 8-bit sRGB and the wire format is "#RRGGBB", so RGB(_:) deliberately
            // quantises into that space on writeback. This coercion is intended.
            set: { hex = RGB($0).hex }
        )
    }

    var body: some View {
        LabeledContent {
            Button { showPopover.toggle() } label: {
                SwatchCircle(color: colorBinding.wrappedValue, diameter: 22)
            }
            .buttonStyle(.plain)
            .popover(isPresented: $showPopover, arrowEdge: .bottom) {
                ColorSwatchPopover(hex: $hex, colorBinding: colorBinding) {
                    showPopover = false
                }
                .padding(12)
            }
        } label: {
            if let symbol {
                RowLabel(title, symbol: symbol, tint: tint)
            } else {
                Text(title)
            }
        }
    }
}

/// A filled colour circle with a thin outline so white/black stay visible, plus
/// an optional accent selection ring.
private struct SwatchCircle: View {
    let color: Color
    var diameter: CGFloat = 24
    var selected: Bool = false

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: diameter, height: diameter)
            .overlay(Circle().strokeBorder(Color.primary.opacity(0.15), lineWidth: 1))
            .overlay {
                Circle()
                    .strokeBorder(Color.accentColor, lineWidth: 2)
                    .padding(-3)
                    .opacity(selected ? 1 : 0)
            }
            .padding(3)
    }
}

/// The popover strip: a native colour-wheel button, a divider, then the AWTRIX
/// common-colour swatches in a single row. All controls drive the same hex.
private struct ColorSwatchPopover: View {
    @Binding var hex: String
    let colorBinding: Binding<Color>
    let onPick: () -> Void

    private var currentRGB: RGB? { RGB(hex: hex) }

    var body: some View {
        HStack(spacing: 8) {
            ColorPicker("Colour wheel", selection: colorBinding, supportsOpacity: false)
                .labelsHidden()

            Divider().frame(height: 24)

            ForEach(AWTRIXPalette.colors, id: \.hex) { preset in
                let presetRGB = RGB(hex: preset.hex)
                Button {
                    hex = preset.hex
                    onPick()
                } label: {
                    SwatchCircle(
                        color: presetRGB.map(Color.init) ?? .white,
                        diameter: 24,
                        selected: presetRGB == currentRGB
                    )
                }
                .buttonStyle(.plain)
                .help(preset.name)
            }
        }
    }
}
