import SwiftUI
import EmberKit

struct SourceLinkSection: View {
    let source: SourceID
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

struct DayClockLinkSection: View {
    var body: some View {
        Section {
            LabeledContent {
                Button("Day Clock Settings…") { showSettings(.app(.dayClock)) }
            } label: {
                Label { Text(AppPane.dayClock.title) } icon: { Image(systemName: AppPane.dayClock.systemImage) }
            }
        } footer: {
            Text("The stop time, break hint and quiet when off are set once in Day Clock. This page sets only whether this knob shows the day clock.")
        }
    }
}

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
