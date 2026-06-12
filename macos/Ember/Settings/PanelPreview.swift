import SwiftUI
import EmberKit

/// One named, static panel preview row for the stacked Display sections
/// (Weather tab panels, Display tab agent cards): title row (with an "— off"
/// suffix when the panel is disabled), the 32×8 frame dimmed when off, and a
/// caption decoding what the pixels mean. Lives on the section's black
/// backdrop, so text colours are explicit (not theme-dependent).
struct PanelPreview: View {
    let title: String
    let caption: String
    let enabled: Bool
    /// The frame to render; nil shows a blank matrix (preview not loaded yet).
    let frame: CardFrame?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text(title).font(.caption2.weight(.semibold)).foregroundStyle(Color.white.opacity(0.75))
                if !enabled {
                    Text("— off").font(.caption2).foregroundStyle(Color.white.opacity(0.4))
                }
            }
            Group {
                if let frame {
                    PreviewCanvas(frames: [frame])
                } else {
                    MatrixScreenView(pixels: Array(repeating: 0, count: 256))
                }
            }
            .opacity(enabled ? 1 : 0.35)
            Text(caption).font(.caption2).foregroundStyle(Color.white.opacity(0.45))
        }
    }
}
