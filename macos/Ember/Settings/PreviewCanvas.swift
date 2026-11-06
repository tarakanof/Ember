import SwiftUI
import EmberKit

struct PreviewCanvas: View {
    let frames: [CardFrame]
    var width: Int = 32
    var height: Int = 8
    var maxPitch: CGFloat = LEDMatrixLayout.maxPitch

    @State private var index = 0

    private var pixels: [Int] {
        guard !frames.isEmpty else { return Array(repeating: 0, count: width * height) }
        let frame = frames[min(index, frames.count - 1)]
        return frame.pixels.map { hex in
            Int(hex.hasPrefix("#") ? hex.dropFirst() : hex[...], radix: 16) ?? 0
        }
    }

    var body: some View {
        MatrixScreenView(pixels: pixels, width: width, height: height, maxPitch: maxPitch)
            .overlay(alignment: .bottomTrailing) {
                if frames.count > 1 {
                    Text("\(min(index, frames.count - 1) + 1) of \(frames.count)")
                        .font(.caption2).monospacedDigit()
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(.black.opacity(0.65), in: Capsule())
                        .padding(3)
                }
            }
            .task(id: frames.count) {
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(2))
                    guard !Task.isCancelled, !frames.isEmpty else { continue }
                    index = (index + 1) % frames.count
                }
            }
            .onChange(of: frames.count) { _, n in if index >= n { index = 0 } }
    }
}
