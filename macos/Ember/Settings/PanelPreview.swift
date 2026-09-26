import SwiftUI
import EmberKit

/// One named preview of a clock panel for the Agents, Focus, Weather and
/// Calendar panes: title (with "Off" when that panel is disabled), the 32×8
/// frame (dimmed when off) and a caption that says what the pixels mean.
/// Sits on the black preview backdrop, so its text colours are fixed.
struct PanelPreview: View {
    let title: LocalizedStringKey
    let caption: LocalizedStringKey
    let enabled: Bool
    /// The frame to render; nil shows a blank matrix (not loaded yet).
    let frame: CardFrame?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text(title).font(.caption.weight(.semibold)).foregroundStyle(Color.white.opacity(0.85))
                if !enabled {
                    Text("Off").font(.caption).foregroundStyle(Color.white.opacity(0.6))
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
            .frame(maxWidth: 420, alignment: .leading)
            .accessibilityHidden(true)
            Text(caption).font(.caption).foregroundStyle(Color.white.opacity(0.65))
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(title))
        .accessibilityValue(enabled ? Text(caption) : Text("Off. \(Text(caption))"))
        .accessibilityAddTraits(.isImage)
    }
}

extension View {
    /// The black LED backdrop a preview row sits on, edge to edge in its section.
    func settingsPreviewBackdrop() -> some View {
        padding(.horizontal, 14)
            .padding(.vertical, 12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.black)
            .environment(\.colorScheme, .dark)
            .listRowInsets(EdgeInsets())
    }
}
