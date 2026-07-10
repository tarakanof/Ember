import SwiftUI
import AppKit
import EmberKit

/// "Report this Mac's agent activity" section: a single toggle that installs
/// (or uninstalls) the Claude/Codex producer LaunchAgents via
/// `ProducerInstallService`, plus a live per-agent status row for whichever
/// agent CLIs are detected on this Mac. Lives in the Agent settings tab.
struct ProducersToggleSection: View {
    @Environment(AppEnvironment.self) private var env

    @State private var isWorking = false
    @State private var save: SaveState = .idle
    // Bumped after every install/uninstall so the derived `toggleState` /
    // `agentState` reads (which have no Observation of their own) recompute.
    @State private var refreshToken = 0

    var body: some View {
        Section {
            Toggle("Report this Mac's agent activity", isOn: toggleBinding)
                .disabled(isWorking)

            let agents = env.producers.detectedAgents()
            if agents.isEmpty {
                Text("No supported agent CLI detected on this Mac (looked for ~/.claude and ~/.codex).")
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                ForEach(agents, id: \.self) { agent in
                    agentRow(agent)
                }
            }
        } header: {
            Text("Agent reporting")
        } footer: {
            footer
        }
    }

    private var toggleBinding: Binding<Bool> {
        Binding(
            get: { _ = refreshToken; return env.producers.toggleState() == .on },
            set: { newValue in Task { await apply(newValue) } }
        )
    }

    @ViewBuilder
    private func agentRow(_ agent: ProducerAgent) -> some View {
        switch env.producers.agentState(agent) {
        case .off:
            LabeledContent(agent.displayName) {
                Text("Off").foregroundStyle(.secondary)
            }
        case .on:
            LabeledContent(agent.displayName) {
                Text("On").foregroundStyle(.secondary)
            }
        case .needsApproval:
            Button {
                openLoginItemsSettings()
            } label: {
                LabeledContent(agent.displayName) {
                    Text("Needs approval in System Settings ›").foregroundStyle(.orange)
                }
            }
            .buttonStyle(.plain)
        case .error(let message):
            LabeledContent(agent.displayName) {
                Text(message).foregroundStyle(.red)
            }
        }
    }

    @ViewBuilder private var footer: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Installs the Claude/Codex producers so this Mac's agent status shows on the clock.")
            statusCaption
            Text("Quitting Ember leaves reporting running in the background — turn this off before deleting Ember for a full clean removal.")
        }
        .font(.caption).foregroundStyle(.secondary)
    }

    @ViewBuilder private var statusCaption: some View {
        switch save {
        case .idle:
            switch env.producers.toggleState() {
            case .partial:
                Label("Partially installed — some agents are on, some off.", systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.orange)
            case .error:
                Label("One or more agents reported an error — see the rows above.", systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.red)
            default:
                EmptyView()
            }
        case .saving:
            Text("Applying…")
        case .saved:
            Label("Saved", systemImage: "checkmark.circle")
        case .error(let m):
            Label(m, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
        }
    }

    /// Runs `installAll`/`uninstallAll` (never throws; per-agent failures are
    /// reported via `AgentOutcome.error`) and surfaces the first failure, if any.
    private func apply(_ on: Bool) async {
        isWorking = true
        save = .saving
        let outcomes = on ? env.producers.installAll() : env.producers.uninstallAll()
        isWorking = false
        refreshToken += 1
        if let failed = outcomes.first(where: { $0.error != nil }) {
            save = .error("\(failed.agent.displayName) failed: \(failed.error!.localizedDescription)")
        } else {
            save = .saved
        }
    }

    /// Opens System Settings > General > Login Items & Extensions, where a
    /// `.needsApproval` LaunchAgent must be approved by the user.
    private func openLoginItemsSettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.LoginItems-Settings.extension") {
            NSWorkspace.shared.open(url)
        }
    }
}

private extension ProducerAgent {
    var displayName: String { rawValue.capitalized }
}
