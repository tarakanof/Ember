import SwiftUI
import AwtrixMenuKit

struct MenuBarContentView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openWindow) private var openWindow
    @Environment(\.openSettings) private var openSettings

    private func mmss(_ sec: Int) -> String {
        let s = max(0, sec); return String(format: "%d:%02d", s / 60, s % 60)
    }

    private func act(_ a: PomodoroAction) {
        Task { try? await env.pomodoro.action(a); await env.model.refresh() }
    }

    var body: some View {
        let model = env.model
        VStack(alignment: .leading, spacing: 8) {
            if let s = model.winningSession {
                Text("\(s.source) · \(s.tool) · \(s.state)").font(.headline)
            } else {
                Text(model.connected ? "Idle" : "Offline").font(.headline)
            }

            Divider()

            if let p = model.pomoState {
                HStack {
                    Text(p.phase.capitalized).bold()
                    Spacer()
                    Text(mmss(p.remainingSec)).monospacedDigit()
                    Text("· round \(p.round)").foregroundStyle(.secondary)
                }
                HStack(spacing: 6) {
                    if p.running && !p.paused {
                        Button("Pause") { act(.pause) }
                        Button("Skip") { act(.skip) }
                        Button("Stop") { act(.stop) }
                    } else if p.paused {
                        Button("Resume") { act(.resume) }
                        Button("Stop") { act(.stop) }
                    } else {
                        Button("Start") { act(.start) }
                    }
                }
            } else {
                Button("Start Focus") { act(.start) }
            }

            if let st = model.stats {
                Text("Today: \(st.today.completedFocus) focus · \(st.today.focusMin) min · streak \(st.streak)")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Divider()
            Button("Dashboard…") {
                NSApplication.shared.activate(ignoringOtherApps: true)
                openWindow(id: "dashboard")
            }
            // NB: a plain SettingsLink does NOT activate the app, so in this
            // LSUIElement (accessory) app the Settings window opens behind and
            // unfocused — it looks like nothing happened until some other window
            // has activated the app first. Activate explicitly, then open.
            Button("Settings…") {
                NSApplication.shared.activate(ignoringOtherApps: true)
                openSettings()
            }
            Button("Quit AWTRIX Menu") { NSApplication.shared.terminate(nil) }
        }
        .padding(12)
        .frame(width: 260)
    }
}
