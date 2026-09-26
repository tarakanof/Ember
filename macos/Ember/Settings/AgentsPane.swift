import SwiftUI
import EmberKit

/// What the clock shows about Claude and Codex sessions. Card toggles are
/// this Mac's producer.env (`agentsEnv`); usage cards and behaviour are
/// server-wide.
struct AgentsPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var preview: PreviewResponse?
    @State private var previewFailed = false
    @State private var producers: ProducerInstallModel?

    private var cards: EnvConfigModel<DisplaySettings> { env.settings.agentsEnv }
    private var usage: ServerConfigModel<UsageConfig> { env.settings.usage }
    private var behavior: ServerConfigModel<DisplayConfig> { env.settings.display }

    /// Title and caption per preview card key (the server names frames after
    /// render's card constants).
    private func meta(_ card: String) -> (title: LocalizedStringKey, caption: LocalizedStringKey) {
        switch card {
        case "source": ("Source card", "Tool icon and machine name in the source color.")
        case "usage-5h": ("5-hour usage", "Green under 70%, amber, red from 90%.")
        case "usage-reset": ("Reset clock", "Time until the 5-hour window resets.")
        case "usage-7d": ("7-day usage", "Weekly window usage.")
        case "usage-model-a": ("First model", "Per-model 5-hour usage, e.g. Opus.")
        case "usage-model-b": ("Second model", "Per-model 5-hour usage, e.g. Sonnet.")
        default: (LocalizedStringKey(card), "")
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
        @Bindable var behavior = behavior
        Form {
            Section {
                VStack(alignment: .leading, spacing: 14) {
                    if let preview {
                        ForEach(preview.frames, id: \.card) { frame in
                            let m = meta(frame.card)
                            PanelPreview(title: m.title, caption: m.caption,
                                         enabled: cardEnabled(frame.card), frame: frame)
                        }
                    } else {
                        PanelPreview(title: "Preview", caption: previewFailed
                                     ? "Preview unavailable: the server didn't answer."
                                     : "Loading…",
                                     enabled: true, frame: nil)
                    }
                }
                .settingsPreviewBackdrop()
            } footer: {
                Text("The clock rotates these cards for each active session. The icon uses this Mac's source color; its eyes show the session state.")
            }

            if let producers {
                ProducersToggleSection(model: producers)
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
                StepperRow(title: "Show usage from", value: $usage.draft.usageThresholdPct,
                           range: 0...100, step: 5) { pct in
                    pct == 0 ? Text("Always") : Text(Percent.text(Double(pct)))
                }
                .disabled(!usage.draft.usageWidget)
                Toggle("Per-model usage", isOn: $usage.draft.usagePerModel)
                    .disabled(!usage.draft.usageWidget)
            } header: {
                Text("Usage")
            } footer: {
                SectionFooter(text: "Usage cards join the rotation once the 5-hour window reaches this level. Server-wide.",
                              error: usage.saveError ?? (usage.isLoaded ? nil : usage.loadError))
            }
            .disabled(!usage.isLoaded)

            Section {
                Toggle("Context glass", isOn: $cards.draft.contextPct)
                Picker("Bottom bar", selection: $cards.draft.bottomBarMode) {
                    Text("Session pixels").tag(BottomBarMode.session)
                    Text("Rate bar").tag(BottomBarMode.rate)
                    Text("Off").tag(BottomBarMode.off)
                }
                Toggle("Activity trail", isOn: $cards.draft.activityTrail)
            } header: {
                Text("On Every Card")
            } footer: {
                Text("Context glass fills with the session's context use; turning it off also stops reporting it. The trail shows other sessions along the bottom.")
            }
            .disabled(!cards.isLoaded)

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
        }
        .formStyle(.grouped)
        .autosaves(cards)
        .autosaves(usage)
        .autosaves(behavior)
        .onAppear { if producers == nil { producers = ProducerInstallModel(service: env.producers) } }
        .onChange(of: cards.draft) { _, _ in Task { await refreshPreview() } }
        .reloads {
            let s = env.settings
            async let a: Void = s.agentsEnv.load()
            async let b: Void = s.usage.load()
            async let c: Void = s.display.load()
            _ = await (a, b, c)
            await refreshPreview()
        }
    }

    private func refreshPreview() async {
        // Always ask for the source and usage cards: the toggles dim the
        // panels here instead of removing them, so each option stays visible.
        var draft = cards.draft.draftDisplay(sourceColor: env.settings.connectionEnv.draft.sourceColor)
        draft.sourceCard = true
        draft.usageCard = true
        do {
            preview = try await env.preview.fetchPreview(draft)
            previewFailed = false
        } catch {
            previewFailed = preview == nil
        }
    }
}
