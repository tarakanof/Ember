import SwiftUI
import EmberKit

struct PanelPreview: View {
    let title: LocalizedStringKey
    let caption: LocalizedStringKey
    let enabled: Bool
    let frame: CardFrame?

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
            .opacity(enabled ? 1 : 0.35)
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
    func ledBezel(padding: CGFloat = 8, cornerRadius: CGFloat = 8) -> some View {
        self.padding(padding)
            .background(.black, in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .environment(\.colorScheme, .dark)
    }

    func settingsPreviewRow() -> some View {
        padding(.vertical, 4)
    }

    func previews<D: Equatable>(_ draft: D, into model: PreviewModel,
                                fetch: @escaping @MainActor (D) async throws -> PreviewResponse) -> some View {
        modifier(PreviewFollower(draft: draft, model: model, fetch: fetch))
    }

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
