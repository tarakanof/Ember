import SwiftUI
import EmberKit

/// Renders a feed in whichever state it's in, so every Dashboard card handles them alike.
struct FeedStateView<T: Sendable & Equatable, Content: View>: View {
    let feed: Loadable<T>
    var placeholder: T? = nil
    var isEmpty: (T) -> Bool = { _ in false }
    var emptyTitle: LocalizedStringKey = "Nothing yet"
    var emptySymbol: String = "tray"
    var offTitle: LocalizedStringKey = "Needs a newer server"
    var offDescription: LocalizedStringKey? = nil
    var offSymbol: String = "power"
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
        case .failed(.featureOff, _, _):
            ContentUnavailableView {
                Label(offTitle, systemImage: offSymbol)
            } description: {
                if let offDescription { Text(offDescription) }
            } actions: {
                if let pane = offSettingsPane {
                    Button("Open Settings") { openSettings(pane: pane, using: openWindow) }
                }
            }
        case .failed(let error, let last?, let lastAt):
            loaded(last)
                .overlay(alignment: .topTrailing) {
                    if showsStaleChip, error != .rateLimited, let lastAt { StaleChip(since: lastAt) }
                }
        case .failed(.rateLimited, nil, _):
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        case .failed(.offline, nil, _):
            ContentUnavailableView("Server unreachable", systemImage: "network.slash")
        case .failed(.timedOut, nil, _):
            ContentUnavailableView {
                Label { Text("Server not responding", comment: "Title shown in place of content: the server didn't answer in time.") }
                    icon: { Image(systemName: "clock.badge.exclamationmark") }
            }
        case .failed(.localNetworkDenied, nil, _):
            ContentUnavailableView("Local Network access is off", systemImage: "wifi.exclamationmark")
        case .failed(let error, nil, _):
            ContentUnavailableView {
                Label("Server error", systemImage: "exclamationmark.triangle")
            } description: {
                Text(error.message)
            }
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
