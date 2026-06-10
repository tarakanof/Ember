import SwiftUI
import EmberKit

/// Renders the simulated /v1/preview card frames in the glowing-LED matrix
/// design (via `MatrixScreenView`), cycling through the cards on a timer to
/// mirror the device rotation. Each frame's "#rrggbb" pixels are converted to
/// the 24-bit ints `MatrixScreenView` expects.
struct PreviewCanvas: View {
    let frames: [CardFrame]
    var width: Int = 32
    var height: Int = 8

    @State private var index = 0
    @State private var timer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect()

    private var pixels: [Int] {
        guard !frames.isEmpty else { return Array(repeating: 0, count: width * height) }
        let frame = frames[min(index, frames.count - 1)]
        return frame.pixels.map { hex in
            Int(hex.hasPrefix("#") ? hex.dropFirst() : hex[...], radix: 16) ?? 0
        }
    }

    var body: some View {
        MatrixScreenView(pixels: pixels, width: width, height: height)
            .onReceive(timer) { _ in
                guard !frames.isEmpty else { return }
                index = (index + 1) % frames.count
            }
            .onChange(of: frames.count) { _, n in if index >= n { index = 0 } }
    }
}
