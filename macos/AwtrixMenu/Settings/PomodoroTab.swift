import SwiftUI
import AwtrixMenuKit

struct PomodoroTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var config = PomoConfig(
        focusMinutes: 25, shortBreakMinutes: 5, longBreakMinutes: 15,
        roundsBeforeLongBreak: 4, autoStartNext: false, sound: true,
        soundMelody: "", focusColor: "#3aa0ff", breakColor: "#2ee85e")
    @State private var status: String?
    @State private var loadError: String?
    @State private var loaded = false

    var body: some View {
        Form {
            if let loadError {
                Text(loadError).foregroundStyle(.secondary).font(.caption)
            }

            Section("Durations") {
                Stepper("Focus: \(config.focusMinutes) min",
                        value: $config.focusMinutes, in: 1...180)
                Stepper("Short break: \(config.shortBreakMinutes) min",
                        value: $config.shortBreakMinutes, in: 1...60)
                Stepper("Long break: \(config.longBreakMinutes) min",
                        value: $config.longBreakMinutes, in: 1...120)
                Stepper("Rounds before long break: \(config.roundsBeforeLongBreak)",
                        value: $config.roundsBeforeLongBreak, in: 1...12)
            }

            Section("Behaviour") {
                Toggle("Auto-start next phase", isOn: $config.autoStartNext)
                Toggle("Sound", isOn: $config.sound)
                TextField("Melody (RTTTL/name)", text: $config.soundMelody)
                    .disabled(!config.sound)
            }

            Section("Colours") {
                colorRow("Focus colour", $config.focusColor)
                colorRow("Break colour", $config.breakColor)
            }

            if let status { Text(status).font(.caption) }

            HStack {
                Button("Reload") { Task { await load() } }
                Spacer()
                Button("Save") { Task { await save() } }.keyboardShortcut(.defaultAction)
            }
        }
        .padding()
        .task { if !loaded { await load(); loaded = true } }
    }

    @ViewBuilder
    private func colorRow(_ label: String, _ binding: Binding<String>) -> some View {
        HStack {
            TextField(label + " (#RRGGBB)", text: binding)
            if let c = RGB(hex: binding.wrappedValue).map({ Color($0) }) {
                RoundedRectangle(cornerRadius: 3).fill(c).frame(width: 18, height: 18)
            }
        }
    }

    private func load() async {
        loadError = nil; status = nil
        do {
            config = try await env.pomodoro.getConfig()
        } catch {
            loadError = "Couldn't load Pomodoro config (server offline or pomodoro disabled)."
        }
    }

    private func save() async {
        status = "Saving…"
        guard RGB(hex: config.focusColor) != nil, RGB(hex: config.breakColor) != nil else {
            status = "Colours must be #RRGGBB."
            return
        }
        do {
            try await env.pomodoro.putConfig(config)
            status = "Saved."
        } catch let e as APIError where e.isUnauthorized {
            status = "Unauthorized — check the token in the Connection tab."
        } catch {
            status = "Save failed: \(error.localizedDescription)"
        }
    }
}
