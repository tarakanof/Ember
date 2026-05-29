import SwiftUI
import AwtrixMenuKit

struct MenuBarContentView: View {
    @Bindable var model: AppModel
    let pomodoro: PomodoroService

    private func mmss(_ sec: Int) -> String {
        let s = max(0, sec); return String(format: "%d:%02d", s / 60, s % 60)
    }

    private func act(_ a: PomodoroAction) {
        Task { try? await pomodoro.action(a); await model.refresh() }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let s = model.winningSession {
                Text("\(s.source) · \(s.tool) · \(s.state)")
                    .font(.headline)
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
            Button("Quit AWTRIX Menu") { NSApplication.shared.terminate(nil) }
        }
        .padding(12)
        .frame(width: 260)
    }
}
