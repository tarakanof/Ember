import SwiftUI
import EmberKit

/// Renders a feed in whichever state it's in, so every Dashboard card handles
/// loading, empty, off, offline, unauthorized and stale the same way:
///
/// | State | Shows |
/// |---|---|
/// | never loaded | `placeholder` redacted, else a spinner |
/// | loaded | `content`, or the empty message when `isEmpty` |
/// | 404, no value | the off message and its Settings button |
/// | offline/server error/429 with a value | `content` plus a stale chip |
/// | offline/server error, no value | "Server unreachable" |
/// | 429, no value | a spinner (a retry is already scheduled) |
/// | 401 | "Needs token" with a button to Connection |
struct FeedStateView<T: Sendable & Equatable, Content: View>: View {
    let feed: Loadable<T>
    /// Rendered redacted while loading, so the card keeps its shape.
    var placeholder: T? = nil
    var isEmpty: (T) -> Bool = { _ in false }
    var emptyTitle: LocalizedStringKey = "Nothing yet"
    var emptySymbol: String = "tray"
    var offTitle: LocalizedStringKey = "Needs a newer server"
    var offDescription: LocalizedStringKey? = nil
    /// Settings pane ("focus") the off state's button opens; no button if nil.
    var offSettingsPane: String? = nil
    var showsStaleChip = true
    @ViewBuilder let content: (T) -> Content

    @Environment(\.openWindow) private var openWindow

    var body: some View {
        switch feed {
        case .loading:
            if let placeholder {
                content(placeholder)
                    .redacted(reason: .placeholder)
                    .accessibilityLabel("Loading")
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        case .loaded(let value, _):
            loaded(value)
        case .failed(.unauthorized, _, _):
            ContentUnavailableView {
                Label("Needs token", systemImage: "key")
            } description: {
                Text("Add the server's token in Connection settings.")
            } actions: {
                Button("Open Connection Settings") { openSettings(pane: "connection", using: openWindow) }
            }
        case .failed(let error, let last?, let lastAt):
            loaded(last)
                .overlay(alignment: .topTrailing) {
                    if showsStaleChip, error != .rateLimited, let lastAt { StaleChip(since: lastAt) }
                }
        case .failed(.featureOff, nil, _):
            ContentUnavailableView {
                Label(offTitle, systemImage: "power")
            } description: {
                if let offDescription { Text(offDescription) }
            } actions: {
                if let pane = offSettingsPane {
                    Button("Open Settings") { openSettings(pane: pane, using: openWindow) }
                }
            }
        case .failed(.rateLimited, nil, _):
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        case .failed(_, nil, _):
            ContentUnavailableView("Server unreachable", systemImage: "network.slash")
        }
    }

    @ViewBuilder
    private func loaded(_ value: T) -> some View {
        if isEmpty(value) {
            ContentUnavailableView(emptyTitle, systemImage: emptySymbol)
        } else {
            content(value)
        }
    }
}
