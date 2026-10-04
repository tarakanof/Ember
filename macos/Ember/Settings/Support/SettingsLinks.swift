import SwiftUI
import EmberKit

/// The top of a device app: the source it shows, linked to its settings.
struct SourceLinkSection: View {
    let source: SourceID
    /// The source is turned off, so the device shows nothing for this app.
    var isOff = false

    var body: some View {
        Section {
            LabeledContent {
                Button("Source Settings…") { showSettings(.source(source)) }
            } label: {
                Label { Text(source.title) } icon: { Image(systemName: source.systemImage) }
            }
        } header: {
            Text("Source")
        } footer: {
            if isOff {
                Label("This source is off, so this app shows nothing. Turn it on in the source's settings.",
                      systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
            } else {
                Text("What's shown comes from the source, set once for every device. This page sets only how this device shows it.")
            }
        }
    }
}

/// The bottom of a source pane: every device app showing this source.
struct ShownOnSection: View {
    @Environment(\.settingsTree) private var tree
    let source: SourceID

    var body: some View {
        let apps = tree.apps(showing: source)
        if !apps.isEmpty {
            Section {
                ForEach(apps, id: \.route) { app in
                    LabeledContent {
                        Button("Edit…") { showSettings(app.route) }
                            .accessibilityLabel(Text("Edit \(String(localized: tree.title(for: app.route))) on \(app.device.name)",
                                                     comment: "VoiceOver label of a Shown On link: the app (\"Weather\"), then the device's name (\"Clock\")."))
                    } label: {
                        Label {
                            Text(verbatim: app.device.name)
                            Text(tree.title(for: app.route))
                        } icon: {
                            Image(systemName: app.device.kind.systemImage)
                        }
                    }
                }
            } header: {
                Text("Shown On")
            } footer: {
                Text("Each device has its own look for this source.")
            }
        }
    }
}
