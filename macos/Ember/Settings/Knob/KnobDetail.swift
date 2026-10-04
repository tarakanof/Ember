import SwiftUI
import EmberKit

/// Settings › Knob: the one knob registered on the server (cinder, the
/// ESP32-S3 round display), set up over USB.
struct KnobPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var setup: KnobSetupModel.Mode?

    private var knob: KnobModel { env.knob }

    var body: some View {
        Form {
            LoadStateSection(isLoaded: knob.isLoaded, error: knob.loadError,
                             offMessage: "This server has no knob registry. Update the Ember server.",
                             retry: { await knob.load() })
            if knob.isLoaded {
                if knob.knob == nil {
                    KnobEmptySection(setUp: { setup = .setup })
                } else {
                    KnobStatusSection(setUp: { setup = .setup })
                    Group {
                        KnobDisplaySection()
                        KnobPagesSection()
                        KnobBehaviorSection()
                    }
                    .disabled(!knob.settings.isLoaded)
                    KnobAdvancedSection(changeWiFi: { setup = .changeWiFi })
                }
            }
        }
        .formStyle(.grouped)
        .autosaves(knob.settings)
        .reloads { await knob.load() }
        .task {
            // Last check-in, uptime and RSSI move on their own.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(15))
                await knob.load()
            }
        }
        // The only automatic port opens: while this pane is on screen, once
        // on appear (retrying boards that weren't cinder) and for new boards.
        .onAppear { Task { await knob.probePorts(retryFailed: true) } }
        .onChange(of: knob.ports.ports) { _, _ in Task { await knob.probePorts() } }
        .sheet(item: $setup) { mode in
            KnobSetupSheet(mode: mode)
        }
    }
}

extension KnobSetupModel.Mode: @retroactive Identifiable {
    public var id: Self { self }
}

// MARK: Empty state

struct KnobEmptySection: View {
    @Environment(AppEnvironment.self) private var env
    let setUp: () -> Void

    var body: some View {
        Section {
            VStack(alignment: .leading, spacing: 6) {
                Label("No knob set up", systemImage: "dial.medium")
                    .font(.headline)
                Text("Plug the knob into this Mac with a USB-C cable, then set it up. Ember gives it your Wi-Fi, this server's address and its own token.")
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 4)
            KnobUSBRow()
            HStack {
                Spacer()
                Button("Set Up Knob…", action: setUp)
                    .keyboardShortcut(.defaultAction)
            }
        } header: {
            Text("Status")
        } footer: {
            Text("The knob has to run cinder. Settings change here once it's set up; Wi-Fi changes need the cable again.")
        }
    }
}

/// What's on USB right now.
struct KnobUSBRow: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let knob = env.knob
        LabeledContent("USB") {
            if let port = knob.connectedPort {
                switch knob.portStatus[port.path] {
                case .cinder(let id)?:
                    Label {
                        if id.isProvisioned {
                            Text("\(id.info.name) connected",
                                 comment: "Settings › Knob USB row: the knob's name (\"Knob 61FC8C\").")
                        } else {
                            Text("\(id.info.name) connected, not set up",
                                 comment: "Settings › Knob USB row: the knob's name (\"Knob 61FC8C\"); it has no Wi-Fi or Ember settings yet.")
                        }
                    } icon: {
                        Image(systemName: "cable.connector").foregroundStyle(.green)
                    }
                case .notCinder?:
                    Label("An ESP32 board is connected but isn't running cinder", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                case .unavailable?:
                    Label("A board is connected, but Ember couldn't open its port", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .help("Quit other apps using the port (such as idf.py monitor), then unplug and plug the knob back in.")
                case .probing?, nil:
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.small)
                        Text("Checking the board…").foregroundStyle(.secondary)
                    }
                }
            } else {
                Text("No knob connected").foregroundStyle(.secondary)
            }
        }
    }
}
