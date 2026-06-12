import SwiftUI
import EmberKit

struct DisplayTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var display = DisplaySettings(reading: EnvFile(parsing: ""))
    @State private var sourceColor = ""
    @State private var preview: PreviewResponse?
    @State private var status: String?
    @State private var loaded = false

    // Server-backed AI-usage-widget toggles (GET/PUT /v1/usage/config), debounced.
    @State private var usage = UsageConfig()
    @State private var lastUsage: UsageConfig?
    @State private var usageWriter = DebouncedWriter(delay: .milliseconds(600))

    // Server-backed display behavior config (GET/PUT /v1/display/config), debounced.
    @State private var displayCfg = DisplayConfig()
    @State private var lastDisplayCfg: DisplayConfig?
    @State private var displayWriter = DebouncedWriter(delay: .milliseconds(600))

    // Per-section fold state, persisted so the user's arrangement sticks.
    @AppStorage("displayFold.source") private var sourceExpanded = false
    @AppStorage("displayFold.activity") private var activityExpanded = false
    @AppStorage("displayFold.usage") private var usageExpanded = false
    @AppStorage("displayFold.everyCard") private var everyCardExpanded = false
    @AppStorage("displayFold.behavior") private var behaviorExpanded = false

    /// Friendly name + pixel-decoding caption per preview card key (the server
    /// names frames after render's card constants).
    private static let cardMeta: [String: (title: String, caption: String)] = [
        "source": ("SOURCE CARD", "Tool icon + machine name in its source colour."),
        "usage-5h": ("USAGE — 5H WINDOW", "5h rate-limit usage, threshold-coloured (green < 70 / amber / red ≥ 90); shows the reset clock instead when the rate bottom bar carries the percent."),
        "usage-reset": ("USAGE — RESET CLOCK", "Time until the 5h window resets."),
        "usage-7d": ("USAGE — 7 DAYS", "Weekly window usage percent."),
        "usage-model-a": ("USAGE — MODEL A", "Per-model 5h usage (first model, e.g. Opus)."),
        "usage-model-b": ("USAGE — MODEL B", "Per-model 5h usage (second model, e.g. Sonnet)."),
    ]

    private func cardEnabled(_ card: String) -> Bool {
        switch card {
        case "source": return display.sourceCard
        case "usage-model-a", "usage-model-b": return usage.usageWidget && usage.usagePerModel
        default: return card.hasPrefix("usage-") ? usage.usageWidget : true
        }
    }

    var body: some View {
        Form {
            Section {
                VStack(alignment: .leading, spacing: 14) {
                    if let preview {
                        ForEach(preview.frames, id: \.card) { frame in
                            let meta = Self.cardMeta[frame.card] ?? (frame.card.uppercased(), "")
                            PanelPreview(title: meta.title, caption: meta.caption,
                                         enabled: cardEnabled(frame.card), frame: frame)
                        }
                        activityPanel
                    } else {
                        MatrixScreenView(pixels: Array(repeating: 0, count: 256))
                            .overlay(Text(status ?? "No preview")
                                .font(.caption).foregroundStyle(.secondary))
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.black)
                .listRowInsets(EdgeInsets())
            } footer: {
                Text("The Agent app rotates these cards for each active session — updates live as you toggle. Icon body uses the Source color from Connection; eyes/cursor show state.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section(isExpanded: $sourceExpanded) {
                PictogramToggle(rows: DisplayPictogram.sourceCard, color: DisplayPictogram.neutral,
                                label: "Show source card", isOn: $display.sourceCard)
            } header: {
                Text("Source card")
            }

            Section(isExpanded: $activityExpanded) {
                PictogramToggle(rows: DisplayPictogram.textLines, color: DisplayPictogram.blue,
                                label: "Show activity detail", isOn: $display.activityDetail)
            } header: {
                Text("Activity card")
            }

            Section(isExpanded: $usageExpanded) {
                Toggle("Usage cards", isOn: $usage.usageWidget)
                Stepper(usage.usageThresholdPct == 0
                        ? "Show always (5h ≥ 0 %)"
                        : "Show when 5h ≥ \(usage.usageThresholdPct) %",
                        value: $usage.usageThresholdPct, in: 0...100, step: 5)
                    .disabled(!usage.usageWidget)
                Toggle("Per-model (Opus / Sonnet)", isOn: $usage.usagePerModel)
                    .disabled(!usage.usageWidget)
                Toggle("Limit reset alarm", isOn: $usage.limitAlarm)
                Text("Server-side. Usage cards join the rotation when the 5h window reaches the threshold (0 = always) and keep a dimmed frame on screen while idle. Limit reset alarm: popup + chime when a maxed 5h window resets.")
                    .font(.caption).foregroundStyle(.secondary)
            } header: {
                Text("Usage cards")
            }

            Section(isExpanded: $everyCardExpanded) {
                PictogramToggle(rows: DisplayPictogram.contextGlass, color: DisplayPictogram.green,
                                label: "Context glass", isOn: $display.contextPct)
                Picker(selection: $display.bottomBarMode) {
                    ForEach(BottomBarMode.allCases) { Text($0.rawValue).tag($0) }
                } label: {
                    HStack(spacing: 10) {
                        PixelGlyph(rows: display.bottomBarMode == .rate
                                   ? DisplayPictogram.barRate : DisplayPictogram.barSession,
                                   color: DisplayPictogram.amber)
                        Text("Bottom bar")
                    }
                }
                PictogramToggle(rows: DisplayPictogram.trail, color: DisplayPictogram.blue,
                                label: "Activity trail (multi-session bar)", isOn: $display.activityTrail)
                Text("Drawn on every card. This-Mac options (source card, activity, these elements) write producer.env and apply on the producers' next poll. Context glass off also stops context reporting.")
                    .font(.caption).foregroundStyle(.secondary)
            } header: {
                Text("On every card")
            }

            Section(isExpanded: $behaviorExpanded) {
                Stepper("Hide when idle: \(displayCfg.idleHideMinutes) min",
                        value: $displayCfg.idleHideMinutes, in: 0...60)
                Stepper("Attention hold: \(displayCfg.attentionHoldSeconds) s",
                        value: $displayCfg.attentionHoldSeconds, in: 5...300, step: 5)
                Toggle(isOn: $displayCfg.attentionChime) {
                    HStack(spacing: 10) {
                        PixelGlyph(rows: DisplayPictogram.bell, color: DisplayPictogram.amber)
                        Text("Attention chime")
                    }
                }
                Text("Server-side, applies within seconds, all machines. Idle: dims, then leaves the rotation; returns on activity (0 = hide immediately).")
                    .font(.caption).foregroundStyle(.secondary)
            } header: {
                Text("Behavior")
            }
        }
        .formStyle(.grouped)
        .task {
            if !loaded {
                let envFile = env.currentEnv()
                display = DisplaySettings(reading: envFile)
                sourceColor = ConnectionSettings(reading: envFile).sourceColor
                loaded = true
                if let u = try? await env.usage.getConfig() { usage = u; lastUsage = u }
                if let d = try? await env.displayConfig.getConfig() { displayCfg = d; lastDisplayCfg = d }
                await refreshPreview()
            }
        }
        .onChange(of: display) { _, _ in
            writeDisplay()                       // immediate auto-apply (no reload)
            Task { await refreshPreview() }
        }
        .onChange(of: usage) { _, _ in scheduleUsageSave() }
        .onChange(of: displayCfg) { _, _ in scheduleDisplayCfgSave() }
    }

    /// The scrolling tool-call card has no static pixel form, so it previews as
    /// styled text instead of a frame — same title/caption/dim treatment as the
    /// pixel panels.
    @ViewBuilder private var activityPanel: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text("ACTIVITY CARD").font(.caption2.weight(.semibold)).foregroundStyle(Color.white.opacity(0.75))
                if !display.activityDetail {
                    Text("— off").font(.caption2).foregroundStyle(Color.white.opacity(0.4))
                }
            }
            Text((preview?.activity.isEmpty == false ? preview!.activity : "Bash: npm test") + "  ▸▸")
                .font(.system(size: 13, design: .monospaced))
                .foregroundStyle(Color(red: 0.18, green: 0.91, blue: 0.37)) // running green
                .lineLimit(1)
                .opacity(display.activityDetail ? 1 : 0.35)
            Text("Scrolls the current tool call next to the icon — no static pixel form.")
                .font(.caption2).foregroundStyle(Color.white.opacity(0.45))
        }
    }

    private func scheduleUsageSave() {
        guard usage != lastUsage else { return }   // initial load / no-op
        let u = usage
        usageWriter.schedule {
            try? await env.usage.putConfig(u)
            await MainActor.run { lastUsage = u }
        }
    }

    private func scheduleDisplayCfgSave() {
        guard displayCfg != lastDisplayCfg else { return }   // initial load / no-op
        let d = displayCfg
        displayWriter.schedule {
            try? await env.displayConfig.putConfig(d)
            await MainActor.run { lastDisplayCfg = d }
        }
    }

    private func writeDisplay() {
        var envFile = env.currentEnv()
        display.apply(to: &envFile)
        try? envFile.write(to: env.producerEnvPath)   // best-effort, mirrors old Save
    }

    private func refreshPreview() async {
        // Always request the source + usage cards: the enable toggles dim the
        // stacked panels locally instead of removing them, so the option↔card
        // mapping stays visible (same pattern as the Weather tab).
        var draft = display.draftDisplay(sourceColor: sourceColor)
        draft.sourceCard = true
        draft.usageCard = true
        do {
            preview = try await env.preview.fetchPreview(draft)
            status = nil
        } catch {
            preview = nil
            status = "Preview unavailable (server offline?)"
        }
    }
}
