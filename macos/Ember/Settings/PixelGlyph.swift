import SwiftUI

/// A tiny pixel-art tile rendered the way the device draws it: an 8-row grid of
/// `X` (lit) / `.` (off) strings, painted in one accent colour on black. Used as
/// the per-option pictogram in the Code-agent (Display) tab so each toggle shows
/// *what it adds to the 32×8 matrix*. These mirror the render core's visual
/// language (percent sign, context glass, reset hourglass, bottom bars, text
/// lines) — illustrative, not pixel-exact copies of a live frame.
struct PixelGlyph: View {
    let rows: [String]
    var color: Color
    var cell: CGFloat = 2.0

    private var cols: Int { rows.map(\.count).max() ?? 0 }

    var body: some View {
        Canvas { ctx, _ in
            for (y, row) in rows.enumerated() {
                for (x, ch) in row.enumerated() where ch == "X" {
                    let rect = CGRect(x: CGFloat(x) * cell, y: CGFloat(y) * cell,
                                      width: cell + 0.4, height: cell + 0.4)
                    ctx.fill(Path(rect), with: .color(color))
                }
            }
        }
        .frame(width: CGFloat(cols) * cell, height: CGFloat(rows.count) * cell)
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 3).fill(.black))
    }
}

/// The pictograms for each Code-agent (Display) toggle. Colours echo the device:
/// green for context, amber for rate, blue/white for activity.
enum DisplayPictogram {
    static let green = Color(red: 0.18, green: 0.91, blue: 0.37)
    static let amber = Color(red: 1.0, green: 0.76, blue: 0.30)
    static let blue = Color(red: 0.31, green: 0.66, blue: 1.0)

    // Percent sign (3×5 from the render font) — the "%" number card.
    static let percent = [
        "........",
        ".X...X..",
        "....X...",
        "...X....",
        "..X.....",
        ".X...X..",
        "........",
        "........",
    ]

    // Context-window glass tumbler (the ⌷ pictogram, partially filled).
    static let glass = [
        "........",
        "........",
        "..X.X...",
        "..X.X...",
        "..XXX...",
        "..XXX...",
        "..XXX...",
        "........",
    ]

    // Reset hourglass (the ⧗ pictogram) — the HH:MM reset countdown card.
    static let hourglass = [
        "........",
        "..XXX...",
        "...X....",
        "...X....",
        "...X....",
        "..XXX...",
        "........",
        "........",
    ]

    // A single dimmed bottom bar across the content area (row 7).
    static let bottomBar = [
        "........",
        "........",
        "........",
        "........",
        "........",
        "........",
        "XXXXXX..",
        "........",
    ]

    // Scrolling activity text — three short lines.
    static let textLines = [
        "........",
        "........",
        ".XXXXX..",
        "........",
        ".XXX.X..",
        "........",
        ".XXXX...",
        "........",
    ]

    // Multi-session trail — a segmented bottom bar.
    static let trail = [
        "........",
        "........",
        "........",
        "........",
        "........",
        "........",
        "XX.XX.XX",
        "........",
    ]
}

/// A Code-agent settings toggle with its pictogram on the left and the label
/// after it. The switch stays on the trailing edge (native Form/Toggle layout).
struct PictogramToggle: View {
    let rows: [String]
    let color: Color
    let label: String
    @Binding var isOn: Bool

    var body: some View {
        Toggle(isOn: $isOn) {
            HStack(spacing: 10) {
                PixelGlyph(rows: rows, color: color)
                Text(label)
            }
        }
    }
}
