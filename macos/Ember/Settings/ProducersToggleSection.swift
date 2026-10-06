import SwiftUI
import AppKit
import EmberKit

struct ProducersToggleSection: View {
    let model: ProducerInstallModel
    let tuning: EnvConfigModel<ProducerTuning>
    let overrides: ProducerTuning.Overrides

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
                    if snapshot.showsSettings(for: row.agent) {
                        AgentTuningRows(agent: row.agent, reporting: row.state != .off,
                                        tuning: tuning, overrides: overrides)
                    }
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
                SaveErrorFooter(error: tuning.saveError)
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
                stateView(state, agent: agent)
                if state != .cliInstalled {
                    Toggle(agentName(agent), isOn: Binding(
                        get: { state != .off },
                        set: { on in Task { await model.setEnabled(agent, on) } }))
                        .labelsHidden()
                        .toggleStyle(.switch)
                        .controlSize(.mini)
                        .disabled(model.isWorking || isError(state))
                }
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
                    Label("No Claude hooks are registered in ~/.claude/settings.json (a plugin enabled only for a project isn't checked), so Claude Code sessions may not report.",
                          systemImage: "exclamationmark.triangle.fill")
                case .settingsUnreadable:
                    Label("~/.claude/settings.json isn't valid JSON, so Ember can't read or fix the hooks. Fix the file by hand.",
                          systemImage: "exclamationmark.triangle.fill")
                case .paused, .fine:
                    Label("Claude hooks are paused (~/.config/ember/claude-hooks.disabled), so Claude Code sessions don't report.",
                          systemImage: "exclamationmark.triangle.fill")
                }
            }
            .foregroundStyle(.orange)
            if notice.offersConfigure {
                Button("Fix Hooks") { Task { await model.configureClaudeHooks() } }
                    .disabled(model.isWorking)
                    .help("Runs ember-claude-producer configure: removes the pause and keeps one set of hooks, the plugin's when it's enabled.")
            }
        }
    }

    private func localNetworkHint(_ agents: [ProducerAgent]) -> some View {
        let helpers = ListFormatter.localizedString(byJoining: agents.map(\.binaryName))
        return VStack(alignment: .leading, spacing: 6) {
            Label("macOS is blocking \(helpers) from the local network, so its reports don't reach the server. Allow it under Local Network.",
                  systemImage: "wifi.exclamationmark")
                .foregroundStyle(.orange)
            Button("Review Permissions…") { showSettings(.app(.permissions)) }
        }
    }

    @ViewBuilder
    private func stateView(_ state: AgentState, agent: ProducerAgent) -> some View {
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
        case .cliInstalled:
            HStack(spacing: 8) {
                Text("Installed from the CLI").foregroundStyle(.secondary)
                Button("Move to Ember") { Task { await model.moveToEmber(agent) } }
                    .disabled(model.isWorking
                              || (agent == .claude && model.snapshot?.claudeHooks?.settingsUnreadable == true))
                    .help("Removes the command-line LaunchAgent with the same name, then lets Ember run the bundled helper.")
            }
        case .error(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
        }
    }
}

/// An agent's producer.env settings, listed under its row in Reporting.
private struct AgentTuningRows: View {
    let agent: ProducerAgent
    let reporting: Bool
    let tuning: EnvConfigModel<ProducerTuning>
    let overrides: ProducerTuning.Overrides

    var body: some View {
        @Bindable var tuning = tuning
        switch agent {
        case .claude:
            Group {
                InfoToggle("Cross-check with claude agents", isOn: Binding(
                    get: { overrides.claudeAgentsPoll ?? tuning.draft.claudeAgentsPoll },
                    set: { tuning.draft.claudeAgentsPoll = $0 }),
                    info: .claudeAgentsPoll, requirement: requirement(overridden: overrides.claudeAgentsPoll != nil))
                    .disabled(!tuning.isLoaded || overrides.claudeAgentsPoll != nil)
                if overrides.claudeAgentsPoll != nil {
                    envCaption(Text("Set by the EMBER_CLAUDE_AGENTS_POLL environment variable."))
                }
                StepperRow(title: "Keep reporting finished sessions", value: $tuning.draft.doneTTLSeconds,
                           range: 5...300, step: 5, info: .doneTTL, requirement: .loading) { Text("\($0) s") }
                    .disabled(!tuning.isLoaded)
                StepperRow(title: "Status line timeout", value: Binding(
                    get: { overrides.statuslineTimeoutMs ?? tuning.draft.statuslineTimeoutMs },
                    set: { tuning.draft.statuslineTimeoutMs = $0 }),
                           range: 1000...60000, step: 1000, info: .statuslineTimeout,
                           requirement: requirement(overridden: overrides.statuslineTimeoutMs != nil)) { ms in
                    ms % 1000 == 0 ? Text("\(ms / 1000) s") : Text("\(ms) ms")
                }
                .disabled(!tuning.isLoaded || overrides.statuslineTimeoutMs != nil)
                if overrides.statuslineTimeoutMs != nil {
                    envCaption(Text("Set by the EMBER_STATUSLINE_TIMEOUT_MS environment variable."))
                }
            }
            .padding(.leading, 12)
        case .codex:
            Group {
                InfoToggle("Include Claude Code's Codex runs", isOn: $tuning.draft.codexIncludeClaude,
                           info: .codexIncludeClaude, requirement: .loading)
                InfoRow("Session kinds", info: .codexSources, requirement: .loading) { label in
                    LabeledContent {
                        HStack(spacing: 10) {
                            ForEach(ProducerTuning.codexSourceKinds, id: \.self) { kind in
                                Toggle(isOn: Binding(
                                    get: { tuning.draft.codexSources.contains(kind) },
                                    set: { tuning.draft.setCodexSource(kind, on: $0) })) {
                                    Text(sourceName(kind))
                                }
                                .toggleStyle(.checkbox)
                                .disabled(tuning.draft.codexSources.contains(kind)
                                          && !tuning.draft.canUncheckCodexSource(kind))
                            }
                        }
                    } label: { label }
                }
                InfoToggle("Follow the app-server daemon", isOn: $tuning.draft.codexAppServer,
                           info: .codexAppServer, requirement: .loading)
                if reporting {
                    Text("Codex reporting restarts to apply a change.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .disabled(!tuning.isLoaded)
            .padding(.leading, 12)
        case .t3:
            EmptyView()
        }
    }

    private func requirement(overridden: Bool) -> SettingsInfoRequirement {
        overridden ? .setByEnvironment : .loading
    }

    private func envCaption(_ text: Text) -> some View {
        text.font(.caption).foregroundStyle(.secondary)
    }

    private func sourceName(_ kind: String) -> LocalizedStringKey {
        switch kind {
        case "cli": "CLI"
        case "vscode": "VS Code"
        case "exec": "Exec"
        case "mcp": "MCP"
        default: LocalizedStringKey(kind)
        }
    }
}
