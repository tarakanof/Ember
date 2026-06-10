import SwiftUI

/// Live mirror of the clock's 32×8 LED matrix (24-bit RGB ints, row-major, as
/// served by /v1/device/screen). Lit pixels are drawn as rounded squares over a
/// soft blurred halo so they read as glowing LEDs; unlit pixels keep a faint
/// grid dot so the panel shape stays visible.
struct MatrixScreenView: View {
    let pixels: [Int]
    /// 0…1 multiplier mirroring the matrix's effective brightness; lit pixels
    /// keep a small floor so a heavily dimmed display still reads as "on".
    var dim: Double = 1
    var width: Int = 32
    var height: Int = 8

    var body: some View {
        Canvas { ctx, size in
            let pw = size.width / CGFloat(width)
            let ph = size.height / CGFloat(height)
            func cell(_ x: Int, _ y: Int) -> CGRect {
                CGRect(x: CGFloat(x) * pw, y: CGFloat(y) * ph, width: pw, height: ph)
            }
            func color(_ v: Int) -> Color {
                Color(.sRGB,
                      red: Double((v >> 16) & 0xff) / 255,
                      green: Double((v >> 8) & 0xff) / 255,
                      blue: Double(v & 0xff) / 255)
            }
            // Glow pass: a blurred halo behind every lit pixel.
            ctx.drawLayer { glow in
                glow.addFilter(.blur(radius: pw * 0.55))
                for y in 0..<height {
                    for x in 0..<width {
                        let i = y * width + x
                        guard i < pixels.count, pixels[i] != 0 else { continue }
                        let halo = cell(x, y).insetBy(dx: -pw * 0.15, dy: -ph * 0.15)
                        glow.fill(Path(ellipseIn: halo), with: .color(color(pixels[i]).opacity(0.55 * dim)))
                    }
                }
            }
            // Crisp pixel pass.
            for y in 0..<height {
                for x in 0..<width {
                    let i = y * width + x
                    guard i < pixels.count else { continue }
                    let r = cell(x, y).insetBy(dx: pw * 0.12, dy: ph * 0.12)
                    let p = Path(roundedRect: r, cornerRadius: pw * 0.18)
                    if pixels[i] == 0 {
                        ctx.fill(p, with: .color(.white.opacity(0.05)))
                    } else {
                        ctx.fill(p, with: .color(color(pixels[i]).opacity(max(dim, 0.12))))
                    }
                }
            }
        }
        .aspectRatio(CGFloat(width) / CGFloat(height), contentMode: .fit)
    }
}
