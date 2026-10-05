import SwiftUI
import EmberKit

/// Sources › Agents: which agents report, and the server-wide timing that
/// every device's agent app follows.
struct AgentsSourcePane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var producers: ProducerInstallModel?

    private var usage: ServerConfigModel<UsageConfig> { env.settings.usage }
    private var behavior: ServerConfigModel<DisplayConfig> { env.settings.display }

    var body: some View {
        @Bindable var usage = usage
        @Bindable var behavior = behavior
        Form {
            if let producers {
                ProducersToggleSection(model: producers)
            }

            Section {
                StepperRow(title: "Show usage from", value: $usage.draft.usageThresholdPct,
                           range: 0...100, step: 5) { pct in
                    pct == 0 ? Text("Always") : Text(Percent.text(Double(pct)))
                }
            } header: {
                Text("Usage")
            } footer: {
                SectionFooter(text: "Usage cards join the rotation once the 5-hour window reaches this level. Server-wide.",
                              error: usage.saveError ?? (usage.isLoaded ? nil : usage.loadError))
            }
            .disabled(!usage.isLoaded)

            Section {
                StepperRow(title: "Hide when idle", value: $behavior.draft.idleHideMinutes, range: 0...60) { m in
                    m == 0 ? Text("At once") : Text("After \(m) min")
                }
                StepperRow(title: "Attention hold", value: $behavior.draft.attentionHoldSeconds,
                           range: 5...300, step: 5) { Text("\($0) s") }
            } header: {
                Text("Behavior")
            } footer: {
                SectionFooter(text: "An idle session dims, then leaves the rotation until it's active again. A session that needs you holds the clock this long. Server-wide.",
                              error: behavior.saveError ?? (behavior.isLoaded ? nil : behavior.loadError))
            }
            .disabled(!behavior.isLoaded)

            ShownOnSection(source: .agents)
        }
        .formStyle(.grouped)
        .autosaves(usage)
        .autosaves(behavior)
        .onAppear { if producers == nil { producers = ProducerInstallModel(service: env.producers) } }
        .reloads {
            let s = env.settings
            async let b: Void = s.usage.load()
            async let c: Void = s.display.load()
            _ = await (b, c)
        }
    }
}

/// Clock › Apps › Agents: the agent cards on the TC001.
struct ClockAgentsAppPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var preview = PreviewModel()

    private var cards: EnvConfigModel<DisplaySettings> { env.settings.agentsEnv }
    private var usage: ServerConfigModel<UsageConfig> { env.settings.usage }

    private func meta(_ card: String) -> (title: LocalizedStringKey, caption: LocalizedStringKey) {
        switch card {
        case "source": ("Source card", "Tool icon and machine name in the source color.")
        case "usage-5h": ("5-hour usage", "Green under 70%, amber, red from 90%.")
        case "usage-reset": ("Reset clock", "Time until the 5-hour window resets.")
        case "usage-7d": ("7-day usage", "Weekly window usage.")
        case "usage-model-a": ("First model", "Per-model 5-hour usage, e.g. Opus.")
        case "usage-model-b": ("Second model", "Per-model 5-hour usage, e.g. Sonnet.")
        default: (LocalizedStringKey(card), LocalizedStringKey(String()))
        }
    }

    private func cardEnabled(_ card: String) -> Bool {
        switch card {
        case "source": cards.draft.sourceCard
        case "usage-model-a", "usage-model-b": usage.draft.usageWidget && usage.draft.usagePerModel
        default: card.hasPrefix("usage-") ? usage.draft.usageWidget : true
        }
    }

    var body: some View {
        @Bindable var cards = cards
        @Bindable var usage = usage
        Form {
            SourceLinkSection(source: .agents)

            Section {
                VStack(alignment: .leading, spacing: 14) {
                    if let response = preview.response {
                        ForEach(response.frames, id: \.card) { frame in
                            let m = meta(frame.card)
                            PanelPreview(title: m.title, caption: m.caption,
                                         enabled: cardEnabled(frame.card), frame: frame)
                        }
                    } else {
                        PanelPreview(title: "Preview", caption: preview.isUnavailable
                                     ? "Preview unavailable: the server didn't answer."
                                     : "Loading…",
                                     enabled: true, frame: nil)
                    }
                }
                .settingsPreviewRow()
            } footer: {
                Text("The clock rotates these cards for each active session. The icon uses this Mac's source color; its eyes show the session state.")
            }

            Section {
                Toggle("Source card", isOn: $cards.draft.sourceCard)
                Toggle("Activity card", isOn: $cards.draft.activityDetail)
            } header: {
                Text("Cards")
            } footer: {
                SectionFooter(text: "The activity card scrolls the current tool call next to the icon. These apply to this Mac's sessions.",
                              error: cards.saveError)
            }
            .disabled(!cards.isLoaded)

            Section {
                Toggle("Usage cards", isOn: $usage.draft.usageWidget)
                Toggle("Per-model usage", isOn: $usage.draft.usagePerModel)
                    .disabled(!usage.draft.usageWidget)
            } header: {
                Text("Usage")
            } footer: {
                SectionFooter(text: "When usage cards join the rotation is set in Sources › Agents. Server-wide.",
                              error: usage.saveError ?? (usage.isLoaded ? nil : usage.loadError))
            }
            .disabled(!usage.isLoaded)

            Section {
                Toggle("Context glass", isOn: $cards.draft.contextPct)
                InfoRow("Bottom bar", info: .bottomBar) { label in
                    Picker(selection: $cards.draft.bottomBarMode) {
                        Text("Session pixels").tag(BottomBarMode.session)
                        Text("Rate bar").tag(BottomBarMode.rate)
                        Text("Off").tag(BottomBarMode.off)
                    } label: { label }
                }
                InfoToggle("Activity trail", isOn: $cards.draft.activityTrail, info: .activityTrail)
            } header: {
                Text("On Every Card")
            } footer: {
                Text("Context glass fills with the session's context use; turning it off also stops reporting it.")
            }
            .disabled(!cards.isLoaded)

            ShowOnClockSection()
        }
        .formStyle(.grouped)
        .autosaves(cards)
        .autosaves(usage)
        .previews(previewDraft, into: preview) { try await env.preview.fetchPreview($0) }
        .reloads {
            let s = env.settings
            async let a: Void = s.agentsEnv.load()
            async let b: Void = s.usage.load()
            _ = await (a, b)
        }
    }

    private var previewDraft: DraftDisplay {
        var draft = cards.draft.draftDisplay(sourceColor: env.settings.connectionEnv.draft.sourceColor)
        draft.sourceCard = true
        draft.usageCard = true
        return draft
    }
}

/// Which tools' sessions the clock shows (the menu's Show on Clock).
private struct ShowOnClockSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let apps = MenuRows.showOnClock(env.live.apps)
        if !apps.isEmpty {
            Section {
                ForEach(apps) { app in
                    Toggle(isOn: Binding(
                        get: { app.enabled },
                        set: { on in Task { await env.actions.run(.setApp(app.name, enabled: on)) } })) {
                        Text(app.title)
                    }
                }
            } header: {
                Text("Show on Clock")
            } footer: {
                Text("A hidden tool's sessions still report; the clock skips them. Changes apply at once.")
            }
        }
    }
}
