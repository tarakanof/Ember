import SwiftUI
import AppKit
import EmberKit

/// "Report this Mac's agent activity": installs or removes the Claude/Codex
/// producer LaunchAgents, with a status row per agent CLI found on this Mac.
struct ProducersToggleSection: View {
    let model: ProducerInstallModel

    var body: some View {
        Section {
            Toggle("Report this Mac's agent activity", isOn: Binding(
                get: { model.isOn },
                set: { on in Task { await model.setEnabled(on) } }))
                .disabled(model.isWorking || model.snapshot == nil)

            if let snapshot = model.snapshot {
                if snapshot.agents.isEmpty {
                    Text("No Claude Code or Codex found on this Mac.").foregroundStyle(.secondary)
                } else {
                    ForEach(snapshot.agents, id: \.agent) { row in
                        LabeledContent(row.agent == .claude ? "Claude Code" : "Codex") {
                            stateView(row.state)
                        }
                    }
                    if !snapshot.localNetworkBlocked.isEmpty {
                        localNetworkHint(snapshot.localNetworkBlocked)
                    }
                }
            } else {
                LabeledContent {
                    ProgressView().controlSize(.small)
                } label: {
                    Text("Checking agents…").foregroundStyle(.secondary)
                }
            }
        } header: {
            Text("Reporting")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Reporting keeps running after you quit Ember. Turn it off before deleting Ember to remove it completely.")
                if model.isWorking {
                    Text("Applying…")
                } else if let failure = model.failure {
                    Label(failure, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                } else if model.snapshot?.needsRepair == true {
                    Label("Reporting is on, but macOS isn't running its background helper. Repair registers it again.",
                          systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                } else if model.snapshot?.toggle == .partial {
                    Label("Only some agents are reporting.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                }
            }
        }
        .task { await model.refresh() }
    }

    /// The helper runs but macOS denies it the LAN ("no route to host"), so
    /// nothing reaches the server while the row still says On. A rebuilt or
    /// re-signed helper needs Local Network access again.
    private func localNetworkHint(_ agents: [ProducerAgent]) -> some View {
        let helpers = ListFormatter.localizedString(byJoining: agents.map(\.binaryName))
        return VStack(alignment: .leading, spacing: 6) {
            Label("macOS is blocking \(helpers) from the local network, so its reports don't reach the server. Allow it under Local Network.",
                  systemImage: "wifi.exclamationmark")
                .foregroundStyle(.orange)
            Button("Open Local Network Settings…") {
                openSystemSettings("x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork")
            }
        }
    }

    @ViewBuilder
    private func stateView(_ state: AgentState) -> some View {
        switch state {
        case .off:
            Text("Off").foregroundStyle(.secondary)
        case .on:
            Label("On", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
        case .needsApproval:
            Button("Approve in Login Items…") {
                openSystemSettings("x-apple.systempreferences:com.apple.LoginItems-Settings.extension")
            }
        case .notRunning:
            HStack(spacing: 8) {
                Label("Not running", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Button("Repair") { Task { await model.repair() } }
                    .disabled(model.isWorking)
                    .help("Registers the background helper with macOS again so it starts reporting.")
            }
        case .error(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
        }
    }
}
