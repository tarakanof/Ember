import SwiftUI
import EmberKit

struct DisplayTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var display = DisplaySettings(reading: EnvFile(parsing: ""))
    @State private var sourceColor = ""
    @State private var preview: PreviewResponse?
    @State private var status: String?
    @State private var loaded = false

    var body: some View {
        Form {
            Section("Preview") {
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
            }

            Section("Context") {
                Toggle("Context %", isOn: $display.contextPct)
                Toggle("Context number", isOn: $display.contextNumber)
            }
            Section("Rate limit") {
                Toggle("Rate-limit %", isOn: $display.ratePct)
                Toggle("Rate bottom bar", isOn: $display.rateBottomBar)
                Toggle("Rate reset countdown", isOn: $display.rateReset)
            }
            Section("Activity") {
                Toggle("Activity detail", isOn: $display.activityDetail)
                Toggle("Activity trail (multi-session bar)", isOn: $display.activityTrail)
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Display")
        .task {
            if !loaded {
                let envFile = env.currentEnv()
                display = DisplaySettings(reading: envFile)
                sourceColor = ConnectionSettings(reading: envFile).sourceColor
                loaded = true
                await refreshPreview()
            }
        }
        .onChange(of: display) { _, _ in
            writeDisplay()                       // immediate auto-apply (no reload)
            Task { await refreshPreview() }
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
