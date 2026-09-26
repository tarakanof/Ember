import SwiftUI
import EmberKit

/// One named preview of a clock panel for the Agents, Focus, Weather and
/// Calendar panes: title (with "Off" when that panel is disabled), the 32×8
/// frame on its own black bezel (dimmed when off) and a caption that says what
/// the pixels mean. The bezel hugs the matrix and sits centred in the row, so
/// the preview keeps the panel's shape whatever the row's width.
struct PanelPreview: View {
    let title: LocalizedStringKey
    let caption: LocalizedStringKey
    let enabled: Bool
    /// The frame to render; nil shows a blank matrix (not loaded yet).
    let frame: CardFrame?

    /// Previews stay modest next to the controls they illustrate.
    static let maxPitch: CGFloat = 12

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Text(title).font(.callout.weight(.semibold))
                if !enabled {
                    Text("Off").font(.callout).foregroundStyle(.secondary)
                }
            }
            Group {
                if let frame {
                    PreviewCanvas(frames: [frame], maxPitch: Self.maxPitch)
                } else {
                    MatrixScreenView(pixels: Array(repeating: 0, count: 256), maxPitch: Self.maxPitch)
                }
            }
            .opacity(enabled ? 1 : 0.35) // dims the LEDs, not the black bezel
            .ledBezel()
            .frame(maxWidth: .infinity)
            .accessibilityHidden(true)
            Text(caption).font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(title))
        .accessibilityValue(enabled ? Text(caption) : Text("Off. \(Text(caption))"))
        .accessibilityAddTraits(.isImage)
    }
}

extension View {
    /// The black panel an LED matrix sits on: a thin margin, hugging the
    /// matrix so it never letterboxes across a wide row or card.
    func ledBezel(padding: CGFloat = 8, cornerRadius: CGFloat = 8) -> some View {
        self.padding(padding)
            .background(.black, in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .environment(\.colorScheme, .dark)
    }

    /// Spacing for the stack of previews at the top of a settings pane.
    func settingsPreviewRow() -> some View {
        padding(.vertical, 4)
    }

    /// Keeps `model` on the server's preview of `draft`: fetched when the pane
    /// appears, when `draft` changes and when the window becomes active again,
    /// always through the model's debounce, and cancelled when the pane goes
    /// away. `fetch` reads `env.preview` at call time, so a Connection change
    /// is picked up.
    func previews<D: Equatable>(_ draft: D, into model: PreviewModel,
                                fetch: @escaping @MainActor (D) async throws -> PreviewResponse) -> some View {
        modifier(PreviewFollower(draft: draft, model: model, fetch: fetch))
    }

    /// A preview that doesn't depend on a draft (Calendar's meeting and
    /// reminder tiles).
    func previews(into model: PreviewModel,
                  fetch: @escaping @MainActor () async throws -> PreviewResponse) -> some View {
        previews(true, into: model) { _ in try await fetch() }
    }
}

private struct PreviewFollower<D: Equatable>: ViewModifier {
    let draft: D
    let model: PreviewModel
    let fetch: @MainActor (D) async throws -> PreviewResponse
    @Environment(\.appearsActive) private var appearsActive

    func body(content: Content) -> some View {
        content
            .onAppear { request(draft) }
            .onChange(of: draft) { _, new in request(new) }
            .onChange(of: appearsActive) { _, active in
                if active { request(draft) }
            }
            .onDisappear { model.cancel() }
    }

    private func request(_ draft: D) {
        let fetch = fetch
        model.request { try await fetch(draft) }
    }
}
