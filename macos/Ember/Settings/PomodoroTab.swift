import SwiftUI
import EmberKit

struct PomodoroTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var config = PomoConfig(
        focusMinutes: 25, shortBreakMinutes: 5, longBreakMinutes: 15,
        roundsBeforeLongBreak: 4, autoStartNext: false, sound: true,
        soundMelody: "", focusColor: "#3aa0ff", breakColor: "#2ee85e",
        maxSessionMinutes: 480)
    @State private var save: SaveState = .idle
    @State private var loaded = false
    @State private var writer = DebouncedWriter(delay: .milliseconds(600))
    @State private var lastApplied: PomoConfig?   // last value loaded-from / saved-to server; suppresses no-op saves

    var body: some View {
        Form {
            Section {
                Toggle("Enable Pomodoro", isOn: $config.enabled)
            } footer: {
                Text("Runs the Pomodoro timer on the clock and lets the device buttons control it. Takes effect immediately — no restart.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Durations") {
                Stepper("Focus: \(config.focusMinutes) min",
                        value: $config.focusMinutes, in: 1...480)
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
                TextField("Melody", text: $config.soundMelody, prompt: Text("RTTTL or name"))
                    .disabled(!config.sound)
                Stepper(config.maxSessionMinutes == 0
                        ? "Auto-stop: off"
                        : "Auto-stop after: \(config.maxSessionMinutes / 60) h",
                        value: $config.maxSessionMinutes, in: 0...1440, step: 60)
            }

            Section {
                ColorHexPicker(title: "Focus colour", hex: $config.focusColor)
                ColorHexPicker(title: "Break colour", hex: $config.breakColor)
            } header: {
                Text("Colours")
            } footer: {
                statusCaption
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Pomodoro")
        .toolbar {
            ToolbarItem { Button("Reload from server") { Task { await load() } } }
        }
        .task { if !loaded { await load(); loaded = true } }
        .onChange(of: config) { _, _ in scheduleSave() }
    }

    @ViewBuilder private var statusCaption: some View {
        switch save {
        case .idle:   EmptyView()
        case .saving: Text("Saving…").font(.caption).foregroundStyle(.secondary)
        case .saved:  Label("Saved", systemImage: "checkmark.circle")
                        .font(.caption).foregroundStyle(.secondary)
        case .error(let m): Label(m, systemImage: "exclamationmark.triangle")
                        .font(.caption).foregroundStyle(.red)
        }
    }

    private func scheduleSave() {
        guard loaded else { return }                 // ignore the initial load mutation
        guard config != lastApplied else { return }   // initial load / no-op change: nothing to save
        guard RGB(hex: config.focusColor) != nil, RGB(hex: config.breakColor) != nil else {
            save = .error("Colours must be #RRGGBB."); return
        }
        save = .saving
        let cfg = config
        writer.schedule {
            do {
                try await env.pomodoro.putConfig(cfg)
                await MainActor.run { lastApplied = cfg; save = .saved }
            } catch let e as APIError where e.isUnauthorized {
                await MainActor.run { save = .error("Unauthorized — check the token in Connection.") }
            } catch {
                await MainActor.run { save = .error("Save failed: \(error.localizedDescription)") }
            }
        }
    }

    private func load() async {
        save = .idle
        do {
            config = try await env.pomodoro.getConfig()
            lastApplied = config
        } catch {
            save = .error("Couldn't load Pomodoro config — \(error.localizedDescription)")
        }
    }
}
