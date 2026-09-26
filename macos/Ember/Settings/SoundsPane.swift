import SwiftUI
import EmberKit

/// Every sound the clock makes, in one place: the clock's own mute and
/// volume, quiet hours, and each feature's chime (each row saves through the
/// model of the feature it belongs to).
struct SoundsPane: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        let s = env.settings
        @Bindable var quiet = s.quiet
        @Bindable var pomodoro = s.pomodoro
        @Bindable var display = s.display
        @Bindable var usage = s.usage
        @Bindable var meetings = s.meetings
        @Bindable var weather = s.weather
        @Bindable var watcher = env.reminderWatcher
        let names = device.melodies.map(\.name)
        Form {
            clockSection

            Section {
                Toggle("Mute sounds at night", isOn: $quiet.draft.enabled)
                Group {
                    DatePicker("From", selection: hourMinuteBinding($quiet.draft.start), displayedComponents: .hourAndMinute)
                    DatePicker("Until", selection: hourMinuteBinding($quiet.draft.end), displayedComponents: .hourAndMinute)
                }
                .disabled(!quiet.draft.enabled)
            } header: {
                Text("Quiet Hours")
            } footer: {
                SectionFooter(text: "Mutes every chime and alarm from the server during these hours, in the server's time zone. Popups still show.",
                              error: quiet.saveError ?? (quiet.isLoaded ? nil : quiet.loadError))
            }
            .disabled(!quiet.isLoaded)

            Section {
                Group {
                    Toggle("Focus phase ends", isOn: $pomodoro.draft.sound)
                    MelodyRow(title: "Focus melody", value: $pomodoro.draft.soundMelody, names: names,
                              allowsCustom: true, canPreview: device.audio == .available,
                              preview: { await device.playTestChime(melody: $0) })
                        .disabled(!pomodoro.draft.sound)
                }
                .disabled(!pomodoro.isLoaded)
                Toggle("Agent needs attention", isOn: $display.draft.attentionChime)
                    .disabled(!display.isLoaded)
                Toggle("5-hour limit resets", isOn: $usage.draft.limitAlarm)
                    .disabled(!usage.isLoaded)
                Toggle("Meeting popup", isOn: $meetings.draft.chime)
                    .disabled(!meetings.isLoaded || meetings.draft.popupLeadMinutes == 0)
                MelodyRow(title: "Severe weather", value: $weather.draft.severeSound, names: names,
                          allowsCustom: true, canPreview: device.audio == .available,
                          preview: { await device.playTestChime(melody: $0) })
                    .disabled(!weather.isLoaded || !weather.draft.severeAlert)
            } header: {
                Text("Chimes")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("A meeting chimes with its popup, so it needs a popup time in Calendar. The severe weather sound plays with its alert, set in Weather.")
                    SaveErrorFooter(error: pomodoro.saveError ?? display.saveError ?? usage.saveError
                                    ?? meetings.saveError ?? weather.saveError)
                }
            }

            Section {
                Toggle("Reminder due", isOn: $watcher.prefs.sound)
                Toggle("Repeat until dismissed", isOn: $watcher.prefs.repeatSound)
                    .disabled(!watcher.prefs.sound || !watcher.prefs.hold)
            } header: {
                Text("This Mac")
            } footer: {
                Text("Repeating rings every few seconds until you press the clock's middle button. It needs “Keep on screen until dismissed” in Calendar, and stops after 15 minutes or when quiet hours start.")
            }
            .disabled(!watcher.prefs.enabled)
        }
        .formStyle(.grouped)
        .autosaves(quiet)
        .autosaves(pomodoro)
        .autosaves(display)
        .autosaves(usage)
        .autosaves(meetings)
        .autosaves(weather)
        .autosaves(device.settings)
        .reloads {
            let s = env.settings
            async let a: Void = s.quiet.load()
            async let b: Void = s.pomodoro.load()
            async let c: Void = s.display.load()
            async let d: Void = s.usage.load()
            async let e: Void = s.meetings.load()
            async let f: Void = s.weather.load()
            _ = await (a, b, c, d, e, f)
            await device.load()
        }
    }

    @ViewBuilder private var clockSection: some View {
        let settings = device.settings
        Section {
            if device.supportsNG11 {
                Toggle("Sound", isOn: settings.binding(\.soundEnabled, true))
                if device.hasBuzzer {
                    PercentSliderRow(title: "Buzzer volume", percent: settings.binding(\.buzzerVolume, 80), step: 5)
                        .disabled(!(settings.draft.soundEnabled ?? true))
                }
            }
            if device.audio == .available {
                LabeledContent {
                    HStack {
                        Button("Play Test Chime") { Task { await device.playTestChime() } }
                            .disabled(device.running.contains(.testChime))
                        Button("Stop") { Task { await device.stopAudio() } }
                    }
                } label: {
                    Text("Test")
                }
            }
        } header: {
            Text("Clock")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                if !device.isLoaded {
                    if let e = device.loadError {
                        Label { Text(e.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                            .foregroundStyle(.orange)
                    } else {
                        Text("Loading…")
                    }
                } else if !device.supportsNG11 {
                    Text("Update the Ember server to mute the clock or set its volume here.")
                } else if device.audio == .noOutput {
                    Text("This clock has no speaker.")
                } else {
                    Text("Turning Sound off silences the clock completely, chimes included.")
                }
                if let e = device.actionErrors[.testChime] ?? device.actionErrors[.stopAudio] {
                    Label { Text(e.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .foregroundStyle(.red)
                }
                SaveErrorFooter(error: settings.saveError)
            }
        }
        .disabled(!device.isLoaded)
    }
}

/// A melody setting: the feature's built-in chime, a melody stored on the
/// clock, or (where the server accepts it) an RTTTL string typed in.
private struct MelodyRow: View {
    let title: LocalizedStringKey
    @Binding var value: String
    let names: [String]
    let allowsCustom: Bool
    let canPreview: Bool
    let preview: (String?) async -> Void
    @State private var customPicked = false

    var body: some View {
        let choice = customPicked ? .custom : MelodyChoice(value: value, available: names)
        LabeledContent {
            HStack(spacing: 6) {
                Picker(selection: Binding(
                    get: { choice },
                    set: { new in
                        customPicked = new == .custom
                        value = new.value(replacing: value, available: names)
                    })) {
                    Text("Built-in").tag(MelodyChoice.builtIn)
                    if !names.isEmpty { Divider() }
                    ForEach(names, id: \.self) { Text(verbatim: $0).tag(MelodyChoice.stored($0)) }
                    if allowsCustom || choice == .custom {
                        Divider()
                        Text("Custom").tag(MelodyChoice.custom)
                    }
                } label: {
                    Text(title)
                }
                .labelsHidden()
                .fixedSize()
                if canPreview {
                    Button {
                        Task {
                            if case .stored(let name) = choice { await preview(name) } else { await preview(nil) }
                        }
                    } label: {
                        Label("Play", systemImage: "play.circle")
                    }
                    .labelStyle(.iconOnly)
                    .buttonStyle(.borderless)
                    .disabled(choice == .custom)
                    .help("Play on the clock")
                }
            }
        } label: {
            Text(title)
        }
        if choice == .custom {
            TextField("Melody", text: $value, prompt: Text("Melody name or RTTTL"))
        }
    }
}
