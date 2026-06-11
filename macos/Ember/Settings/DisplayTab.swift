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

    var body: some View {
        Form {
            Section {
                if let preview, !preview.frames.isEmpty {
                    PreviewCanvas(frames: preview.frames)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                        .frame(maxWidth: .infinity)
                        .background(.black)
                        .listRowInsets(EdgeInsets())
                } else {
                    MatrixScreenView(pixels: Array(repeating: 0, count: 256))
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                        .frame(maxWidth: .infinity)
                        .background(.black)
                        .listRowInsets(EdgeInsets())
                        .overlay(Text(status ?? "No preview")
                            .font(.caption).foregroundStyle(.secondary))
                }
            } footer: {
                if let preview, !preview.activity.isEmpty {
                    Text("How your enabled elements render, cycling the cards — updates live as you toggle. Activity card: \(preview.activity)")
                        .font(.caption).foregroundStyle(.secondary)
                } else {
                    Text("How your enabled elements render, cycling the cards — updates live as you toggle, before the clock rotates to show them.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }

            Section {
                PictogramToggle(rows: DisplayPictogram.sourceCard, color: DisplayPictogram.neutral,
                                label: "Source name card", isOn: $display.sourceCard)
                PictogramToggle(rows: DisplayPictogram.ratePct, color: DisplayPictogram.amber,
                                label: "Rate-limit %", isOn: $display.ratePct)
                PictogramToggle(rows: DisplayPictogram.contextNumber, color: DisplayPictogram.green,
                                label: "Context number", isOn: $display.contextNumber)
                PictogramToggle(rows: DisplayPictogram.resetClock, color: DisplayPictogram.amber,
                                label: "Rate reset countdown", isOn: $display.rateReset)
                PictogramToggle(rows: DisplayPictogram.textLines, color: DisplayPictogram.blue,
                                label: "Activity detail", isOn: $display.activityDetail)
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
            } header: {
                Text("Agent app — this Mac")
            } footer: {
                Text("Cards and bars for sessions started from this Mac (producer.env — applies on the producers' next poll). Icon body uses the Source color from Connection; eyes/cursor show state. Context glass off also stops context reporting (hides the glass and the context-number card).")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
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
            } header: {
                Text("Agent app — behavior")
            } footer: {
                Text("Server-side, applies within seconds, all machines. Idle: dims, then leaves the rotation; returns on activity (0 = hide immediately).")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                Toggle("Show usage apps", isOn: $usage.usageWidget)
                Toggle("Per-model (Opus / Sonnet)", isOn: $usage.usagePerModel)
                    .disabled(!usage.usageWidget)
                Toggle("Limit reset alarm", isOn: $usage.limitAlarm)
            } header: {
                Text("Standalone apps")
            } footer: {
                Text("Server-side. Usage apps: Claude/Codex 5h + 7d tiles. Limit reset alarm: popup + chime when a maxed 5h window resets.")
                    .font(.caption).foregroundStyle(.secondary)
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
        do {
            preview = try await env.preview.fetchPreview(display.draftDisplay(sourceColor: sourceColor))
            status = nil
        } catch {
            preview = nil
            status = "Preview unavailable (server offline?)"
        }
    }
}
