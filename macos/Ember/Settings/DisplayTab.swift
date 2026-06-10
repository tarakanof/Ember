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

    var body: some View {
        Form {
            Section {
                LiveMatrixMirror()
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .frame(maxWidth: .infinity)
                    .background(.black)
                    .listRowInsets(EdgeInsets())
            } header: {
                Text("Live display")
            } footer: {
                Text("What's on the clock right now.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                if let preview, !preview.frames.isEmpty {
                    PreviewCanvas(frames: preview.frames)
                        .frame(height: 56)
                        .frame(maxWidth: .infinity, alignment: .center)
                    if !preview.activity.isEmpty {
                        Text("Activity card: \(preview.activity)")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                } else {
                    RoundedRectangle(cornerRadius: 4).fill(.black).frame(height: 56)
                        .overlay(Text(status ?? "No preview")
                            .font(.caption).foregroundStyle(.secondary))
                }
            } header: {
                Text("Preview")
            } footer: {
                Text("How your enabled elements render — updates live as you toggle, before the clock rotates to show them.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Context") {
                PictogramToggle(rows: DisplayPictogram.percent, color: DisplayPictogram.green,
                                label: "Context %", isOn: $display.contextPct)
                PictogramToggle(rows: DisplayPictogram.glass, color: DisplayPictogram.green,
                                label: "Context number", isOn: $display.contextNumber)
            }
            Section("Rate limit") {
                PictogramToggle(rows: DisplayPictogram.percent, color: DisplayPictogram.amber,
                                label: "Rate-limit %", isOn: $display.ratePct)
                PictogramToggle(rows: DisplayPictogram.bottomBar, color: DisplayPictogram.amber,
                                label: "Rate bottom bar", isOn: $display.rateBottomBar)
                PictogramToggle(rows: DisplayPictogram.hourglass, color: DisplayPictogram.amber,
                                label: "Rate reset countdown", isOn: $display.rateReset)
            }
            Section("Activity") {
                PictogramToggle(rows: DisplayPictogram.textLines, color: DisplayPictogram.blue,
                                label: "Activity detail", isOn: $display.activityDetail)
                PictogramToggle(rows: DisplayPictogram.trail, color: DisplayPictogram.blue,
                                label: "Activity trail (multi-session bar)", isOn: $display.activityTrail)
            }

            Section {
                Toggle("Show usage apps", isOn: $usage.usageWidget)
                Toggle("Per-model (Opus / Sonnet)", isOn: $usage.usagePerModel)
                    .disabled(!usage.usageWidget)
            } header: {
                Text("Usage widget")
            } footer: {
                Text("Standalone AI-usage clock apps (Claude/Codex 5h + 7d, optional Opus/Sonnet). Server-side — applies within a few seconds.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Code agent")
        .task {
            if !loaded {
                let envFile = env.currentEnv()
                display = DisplaySettings(reading: envFile)
                sourceColor = ConnectionSettings(reading: envFile).sourceColor
                loaded = true
                if let u = try? await env.usage.getConfig() { usage = u; lastUsage = u }
                await refreshPreview()
            }
        }
        .onChange(of: display) { _, _ in
            writeDisplay()                       // immediate auto-apply (no reload)
            Task { await refreshPreview() }
        }
        .onChange(of: usage) { _, _ in scheduleUsageSave() }
    }

    private func scheduleUsageSave() {
        guard usage != lastUsage else { return }   // initial load / no-op
        let u = usage
        usageWriter.schedule {
            try? await env.usage.putConfig(u)
            await MainActor.run { lastUsage = u }
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
