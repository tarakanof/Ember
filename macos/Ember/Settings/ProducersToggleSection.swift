import SwiftUI
import AppKit
import EmberKit

struct ProducersToggleSection: View {
    let model: ProducerInstallModel

    var body: some View {
        Section {
            Toggle("Report this Mac's agent activity", isOn: Binding(
                get: { model.isOn },
                set: { on in Task { await model.setEnabled(on) } }))
                .disabled(model.isWorking || model.snapshot == nil)

            if let snapshot = model.snapshot {
                if snapshot.noToolDetected {
                    Text("No Claude Code, Codex or T3 Code found on this Mac.").foregroundStyle(.secondary)
                }
                ForEach(snapshot.agents, id: \.agent) { row in
                    agentRow(row.agent, state: row.state, snapshot: snapshot)
                }
                if snapshot.claudeHooksNotice != .fine {
                    claudeHooksWarning(snapshot.claudeHooksNotice)
                }
                if !snapshot.localNetworkBlocked.isEmpty {
                    localNetworkHint(snapshot.localNetworkBlocked)
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

    private func agentRow(_ agent: ProducerAgent, state: AgentState, snapshot: ProducerSnapshot) -> some View {
        LabeledContent {
            HStack(spacing: 8) {
                stateView(state)
                Toggle(agentName(agent), isOn: Binding(
                    get: { state != .off },
                    set: { on in Task { await model.setEnabled(agent, on) } }))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.mini)
                    .disabled(model.isWorking || isError(state))
            }
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                Text(agentName(agent))
                if snapshot.undetected.contains(agent) {
                    Text("Not found on this Mac. You can turn it on now; it reports once the app runs.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                if agent == .claude, let hooks = snapshot.claudeHooks {
                    claudeHooksCaption(hooks, reportingOn: state != .off)
                }
            }
        }
    }

    private func agentName(_ agent: ProducerAgent) -> LocalizedStringKey {
        switch agent {
        case .claude: "Claude Code"
        case .codex: "Codex"
        case .t3: "T3 Code"
        }
    }

    private func isError(_ state: AgentState) -> Bool {
        if case .error = state { true } else { false }
    }

    @ViewBuilder
    private func claudeHooksCaption(_ hooks: ClaudeHookRegistration, reportingOn: Bool) -> some View {
        Group {
            switch hooks.source {
            case .plugin: Text("Hooks: the ember@ember plugin")
            case .settings: Text("Hooks: ~/.claude/settings.json")
            case .both: Text("Hooks: the ember@ember plugin and ~/.claude/settings.json")
            case .none: Text("Hooks: none registered")
            }
            if hooks.killSwitch {
                Text(reportingOn
                     ? "Paused by claude-hooks.disabled"
                     : "Paused while reporting is off (claude-hooks.disabled)")
            }
        }
        .font(.caption).foregroundStyle(.secondary)
    }

    private func claudeHooksWarning(_ notice: ClaudeHooksNotice) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Group {
                switch notice {
                case .registeredTwice:
                    Label("Claude hooks are registered twice, by the ember@ember plugin and in settings.json, so every event is sent twice.",
                          systemImage: "exclamationmark.triangle.fill")
                case .missing:
                    Label("No Claude hooks are registered, so Claude Code sessions don't report.",
                          systemImage: "exclamationmark.triangle.fill")
                case .paused, .fine:
                    Label("Claude hooks are paused (~/.config/ember/claude-hooks.disabled), so Claude Code sessions don't report.",
                          systemImage: "exclamationmark.triangle.fill")
                }
            }
            .foregroundStyle(.orange)
            Button("Fix Hooks") { Task { await model.configureClaudeHooks() } }
                .disabled(model.isWorking)
                .help("Runs ember-claude-producer configure: removes the pause and keeps one set of hooks, the plugin's when it's enabled.")
        }
    }

    private func localNetworkHint(_ agents: [ProducerAgent]) -> some View {
        let helpers = ListFormatter.localizedString(byJoining: agents.map(\.binaryName))
        return VStack(alignment: .leading, spacing: 6) {
            Label("macOS is blocking \(helpers) from the local network, so its reports don't reach the server. Allow it under Local Network.",
                  systemImage: "wifi.exclamationmark")
                .foregroundStyle(.orange)
            Button("Review Permissions…") { showSettingsPane(.permissions) }
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
