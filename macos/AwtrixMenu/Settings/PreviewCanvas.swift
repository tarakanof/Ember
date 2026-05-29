import SwiftUI
import AwtrixMenuKit

/// Draws the 32×8 AWTRIX matrix from /v1/preview card frames, scaled to fit and
/// cycling through the cards on a timer (mirroring the device rotation). Each
/// pixel is a filled square; "#000000" (off) renders black on the black panel.
struct PreviewCanvas: View {
    let frames: [CardFrame]
    var width: Int = 32
    var height: Int = 8

    @State private var index = 0
    @State private var timer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect()

    var body: some View {
        Canvas { ctx, size in
            guard !frames.isEmpty else { return }
            let frame = frames[min(index, frames.count - 1)]
            let pw = size.width / CGFloat(width)
            let ph = size.height / CGFloat(height)
            for y in 0..<height {
                for x in 0..<width {
                    let i = y * width + x
                    guard i < frame.pixels.count, let rgb = RGB(hex: frame.pixels[i]) else { continue }
                    let rect = CGRect(x: CGFloat(x) * pw, y: CGFloat(y) * ph, width: pw + 0.5, height: ph + 0.5)
                    ctx.fill(Path(rect), with: .color(Color(rgb)))
                }
            }
        }
        .background(.black)
        .aspectRatio(CGFloat(width) / CGFloat(height), contentMode: .fit)
        .onReceive(timer) { _ in
            guard !frames.isEmpty else { return }
            index = (index + 1) % frames.count
        }
        .onChange(of: frames.count) { _, n in if index >= n { index = 0 } }
    }
}
