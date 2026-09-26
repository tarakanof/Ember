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
                } else if model.snapshot?.toggle == .partial {
                    Label("Only some agents are reporting.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                }
            }
        }
        .task { await model.refresh() }
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
        case .error(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
        }
    }
}
