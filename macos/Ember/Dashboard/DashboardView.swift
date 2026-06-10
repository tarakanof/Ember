import SwiftUI
import EmberKit

struct DashboardView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL

    @State private var preview: PreviewResponse?
    @State private var refresh = Timer.publish(every: 5, on: .main, in: .common).autoconnect()

    private func mmss(_ sec: Int) -> String {
        let s = max(0, sec); return String(format: "%d:%02d", s / 60, s % 60)
    }
    private func act(_ a: PomodoroAction) {
        Task { try? await env.pomodoro.action(a); await env.model.refresh() }
    }

    /// Opens the server-rendered stats dashboard in the default browser. All
    /// stats are computed server-side; this view just links to the page.
    private func openStatsDashboard() {
        var base = ConnectionSettings(reading: env.currentEnv()).serverURL
            .trimmingCharacters(in: .whitespaces)
        guard !base.isEmpty else { return }
        if base.hasSuffix("/") { base.removeLast() }
        if let url = URL(string: base + "/v1/pomodoro/dashboard") { openURL(url) }
    }

    var body: some View {
        let model = env.model
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Device").font(.headline)
                if let preview, !preview.frames.isEmpty {
                    PreviewCanvas(frames: preview.frames)
                        .frame(height: 80)
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                } else {
                    RoundedRectangle(cornerRadius: 6).fill(.black).frame(height: 80)
                        .overlay(Text(model.connected ? "…" : "Offline")
                            .font(.caption).foregroundStyle(.secondary))
                }
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Sessions").font(.headline)
                if model.sessions.isEmpty {
                    Text(model.connected ? "No active sessions" : "Offline")
                        .font(.caption).foregroundStyle(.secondary)
                } else {
                    ForEach(model.sessions, id: \.session) { s in
                        HStack(spacing: 8) {
                            Circle().fill(Color(stateColorRGB(s.state))).frame(width: 8, height: 8)
                            Text(s.source).bold()
                            Text(s.tool).foregroundStyle(.secondary)
                            Spacer()
                            Text(s.state).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }

            VStack(alignment: .leading, spacing: 6) {
                Text("Pomodoro").font(.headline)
                if let p = model.pomoState {
                    HStack {
                        Text(p.phase.capitalized).bold()
                        Spacer()
                        Text(mmss(p.remainingSec)).monospacedDigit().font(.title3)
                        Text("· round \(p.round)").foregroundStyle(.secondary)
                    }
                    HStack(spacing: 8) {
                        if p.running && !p.paused {
                            Button("Pause") { act(.pause) }
                            Button("Skip") { act(.skip) }
                            Button("Stop") { act(.stop) }
                        } else if p.paused {
                            Button("Resume") { act(.resume) }
                            Button("Stop") { act(.stop) }
                        } else {
                            Button("Start Focus") { act(.start) }
                        }
                    }
                }
                if let st = model.stats {
                    Text("Today: \(st.today.completedFocus) focus · \(st.today.focusMin) min · streak \(st.streak)")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Button { openStatsDashboard() } label: {
                    Label("Stats Dashboard", systemImage: "chart.bar.xaxis")
                }
                .buttonStyle(.link)
                .help("Open the full Pomodoro stats dashboard in your browser")
            }
        }
        .padding(20)
        .frame(width: 380, alignment: .leading)
        .task { await refreshPreview() }
        .onReceive(refresh) { _ in Task { await refreshPreview() } }
    }

    private func refreshPreview() async {
        let envFile = env.currentEnv()
        let draft = DisplaySettings(reading: envFile)
            .draftDisplay(sourceColor: ConnectionSettings(reading: envFile).sourceColor)
        preview = try? await env.preview.fetchPreview(draft)
    }
}
