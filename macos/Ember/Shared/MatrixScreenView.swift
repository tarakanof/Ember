import SwiftUI
import EmberKit

struct MatrixScreenView: View {
    let pixels: [Int]
    var width: Int = 32
    var height: Int = 8
    var maxPitch: CGFloat = LEDMatrixLayout.maxPitch

    @Environment(\.displayScale) private var scale

    var body: some View {
        LEDMatrixFrame(columns: width, rows: height, scale: scale, maxPitch: maxPitch) {
            Canvas { ctx, size in
                let layout = LEDMatrixLayout(width: size.width, height: size.height,
                                             columns: width, rows: height, scale: scale, maxPitch: maxPitch)
                draw(layout, in: &ctx)
            }
        }
        .accessibilityElement()
        .accessibilityLabel("LED matrix")
        .accessibilityAddTraits(.isImage)
    }

    private func draw(_ layout: LEDMatrixLayout, in ctx: inout GraphicsContext) {
        let radius = layout.cornerRadius
        for y in 0..<height {
            for x in 0..<width {
                let i = y * width + x
                let value = i < pixels.count ? pixels[i] : 0
                let led = Path(roundedRect: layout.led(x: x, y: y), cornerRadius: radius)
                guard value != 0 else {
                    ctx.fill(led, with: .color(.white.opacity(0.06)))
                    continue
                }
                let colour = Self.color(value)
                if layout.showsGlow {
                    let cell = layout.cell(x: x, y: y)
                    ctx.fill(Path(cell), with: .radialGradient(
                        Gradient(colors: [colour.opacity(0.45), colour.opacity(0)]),
                        center: CGPoint(x: cell.midX, y: cell.midY),
                        startRadius: layout.pitch * 0.3, endRadius: layout.pitch * 0.72))
                }
                ctx.fill(led, with: .color(colour))
            }
        }
    }

    private static func color(_ v: Int) -> Color {
        Color(.sRGB,
              red: Double((v >> 16) & 0xff) / 255,
              green: Double((v >> 8) & 0xff) / 255,
              blue: Double(v & 0xff) / 255)
    }
}

private struct LEDMatrixFrame: Layout {
    let columns: Int
    let rows: Int
    let scale: CGFloat
    let maxPitch: CGFloat

    private func layout(_ width: CGFloat?, _ height: CGFloat?) -> LEDMatrixLayout {
        LEDMatrixLayout(width: width, height: height, columns: columns, rows: rows,
                        scale: scale, maxPitch: maxPitch)
    }

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        layout(proposal.width, proposal.height).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let l = layout(bounds.width, bounds.height)
        subviews.first?.place(at: CGPoint(x: bounds.minX + l.origin.x, y: bounds.minY + l.origin.y),
                              proposal: ProposedViewSize(l.size))
    }
}
