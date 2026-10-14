import SwiftUI
import EmberKit

struct KnobDemoPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var device = DeviceKind.knob

    private var knob: KnobModel { env.knob }
    private var demo: KnobDemoModel { env.knob.demo }
    private var availability: KnobDemoAvailability { KnobDemoAvailability(knob: knob.knob) }

    var body: some View {
        Form {
            Section {
                Picker("Device", selection: $device) {
                    Text(verbatim: knob.knob?.name ?? String(localized: DeviceKind.knob.title)).tag(DeviceKind.knob)
                }
            } footer: {
                Text("Plays scenes on the real device. The clock can't play demos yet.")
            }
            LoadStateSection(isLoaded: knob.isLoaded, error: knob.loadError,
                             offMessage: "This server has no knob registry. Update the Ember server.",
                             retry: { await knob.load() })
            if knob.isLoaded {
                switch availability {
                case .noKnob:
                    Section { Text("Set up a knob to play demos on it.") }
                case .needsFirmware:
                    Section {
                        Label("Needs knob firmware with demo support. Update the knob in its Firmware page.",
                              systemImage: "arrow.down.circle")
                    }
                case .ready:
                    controls
                }
            }
        }
        .formStyle(.grouped)
        .task(id: TaskKey(device: knob.knob?.id, ready: availability == .ready, follow: demo.followID)) {
            guard availability == .ready else { return }
            await demo.enter()
        }
    }

    private struct TaskKey: Equatable {
        var device: String?
        var ready: Bool
        var follow: Int
    }

    @ViewBuilder private var controls: some View {
        @Bindable var demo = demo
        Section {
            HStack {
                statusLine
                Spacer()
                Button("Stop") { Task { await demo.stop() } }
                    .disabled(!demo.status.canStop || demo.busy)
            }
            Toggle("Hold", isOn: $demo.hold)
        } header: {
            Text("Playback")
        } footer: {
            Text("Hold keeps a single scene on the knob until Stop or 60 seconds. Play all ignores it.")
        }
        LoadStateSection(isLoaded: demo.scenesLoaded, error: demo.catalogError,
                         offMessage: "This server has no demo scenes. Update the Ember server.",
                         retry: { await demo.loadScenes() })
        if let error = demo.statusError {
            Section {
                Label { Text(error.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .foregroundStyle(.red)
            }
        }
        ForEach(demo.groups) { group in
            Section {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), alignment: .leading)], alignment: .leading, spacing: 8) {
                    ForEach(group.scenes) { scene in
                        Button { Task { await demo.play(scene) } } label: {
                            Text(verbatim: scene.title).frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .disabled(demo.busy)
                    }
                }
            } header: {
                HStack {
                    Text(verbatim: group.name)
                    Spacer()
                    Button { Task { await demo.playAll(group) } } label: {
                        Label("Play all", systemImage: "play.fill")
                    }
                    .disabled(demo.busy)
                }
            }
        }
    }

    @ViewBuilder private var statusLine: some View {
        switch demo.line {
        case .idle:
            Text("Idle").foregroundStyle(.secondary)
        case .waiting(let title):
            Label {
                Text("Waiting for the knob: \(title)",
                     comment: "Demo page status: a scene was sent and the knob hasn't started it yet; the argument is the scene's title (\"Hop\").")
            } icon: { ProgressView().controlSize(.small) }
        case .playing(let title, let left) where left > 0:
            Label {
                Text("Playing \(title), \(left) more",
                     comment: "Demo page status: the knob is playing a scene of a queue; the arguments are the scene's title (\"Hop\") and how many scenes follow it.")
            } icon: { Image(systemName: "play.fill") }
        case .playing(let title, _):
            Label {
                Text("Playing \(title)",
                     comment: "Demo page status: the knob is playing a scene; the argument is the scene's title (\"Hop\").")
            } icon: { Image(systemName: "play.fill") }
        case .unanswered(let title):
            Label {
                Text("Knob didn't respond to \(title)",
                     comment: "Demo page status: the knob never started the scene within 10 seconds; the argument is the scene's title (\"Hop\").")
            } icon: { Image(systemName: "exclamationmark.triangle.fill") }
            .foregroundStyle(.orange)
        case .restarted(let title):
            Label {
                Text("Knob restarted during \(title)",
                     comment: "Demo page status: the knob rebooted while a scene was sent or playing, so the server dropped the demo and its queue; the argument is the scene's title (\"Hop\").")
            } icon: { Image(systemName: "exclamationmark.triangle.fill") }
            .foregroundStyle(.orange)
        }
    }
}
