import SwiftUI
import EmberKit

/// The Pomodoro timer: lengths, goals, behaviour and colours. Server-wide.
/// The phase-end chime is under Sounds & Alerts.
struct FocusPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var preview: PreviewResponse?
    /// "Custom" picked while the length is still one of the presets.
    @State private var customFocus = false

    private var model: ServerConfigModel<PomoConfig> { env.settings.pomodoro }

    var body: some View {
        @Bindable var model = model
        let c = model.draft
        Form {
            Section {
                VStack(alignment: .leading, spacing: 14) {
                    PanelPreview(title: "Focus", caption: "Tomato and time left in the focus color; the bottom bar shows the phase's progress.",
                                 enabled: c.enabled, frame: frame("focus"))
                    PanelPreview(title: "Short break", caption: "Coffee mug and time left in the break color.",
                                 enabled: c.enabled, frame: frame("short_break"))
                    PanelPreview(title: "Long break", caption: "Crescent moon, after ^[\(c.roundsBeforeLongBreak) round](inflect: true).",
                                 enabled: c.enabled, frame: frame("long_break"))
                }
                .settingsPreviewRow()
            } footer: {
                Text("The clock shows animated icons; the preview shows the drawn ones.")
            }

            LoadStateSection(isLoaded: model.isLoaded, error: model.loadError,
                             offMessage: "Pomodoro is off on the server, or the server is too old. Turn it on in the server's config.",
                             retry: { await model.load() })

            Group {
                Section {
                    Toggle("Enable Pomodoro", isOn: $model.draft.enabled)
                } footer: {
                    Text("Runs the timer on the clock and lets the clock's buttons control it.")
                }

                Section("Durations") {
                    let custom = customFocus || !FocusPreset.minutes.contains(c.focusMinutes)
                    Picker("Focus", selection: Binding(
                        get: { custom ? 0 : c.focusMinutes },
                        set: { minutes in
                            customFocus = minutes == 0
                            if minutes != 0 { model.draft.focusMinutes = minutes }
                        })) {
                        ForEach(FocusPreset.minutes, id: \.self) { Text("\($0) min").tag($0) }
                        Divider()
                        Text("Custom").tag(0)
                    }
                    if custom {
                        StepperRow(title: "Custom focus", value: $model.draft.focusMinutes,
                                   range: FocusPreset.customRange.including(c.focusMinutes), step: 5) { Text("\($0) min") }
                    }
                    StepperRow(title: "Short break", value: $model.draft.shortBreakMinutes,
                               range: (1...30).including(c.shortBreakMinutes)) { Text("\($0) min") }
                    StepperRow(title: "Long break", value: $model.draft.longBreakMinutes,
                               range: (5...60).including(c.longBreakMinutes), step: 5) { Text("\($0) min") }
                    StepperRow(title: "Rounds before long break", value: $model.draft.roundsBeforeLongBreak,
                               range: (1...8).including(c.roundsBeforeLongBreak)) { Text("\($0)") }
                }

                Section {
                    StepperRow(title: "Daily goal", value: $model.draft.dailyGoalSessions,
                               range: (0...16).including(c.dailyGoalSessions)) { n in
                        n == 0 ? Text("Off") : Text("^[\(n) session](inflect: true)")
                    }
                    StepperRow(title: "Weekly goal", value: $model.draft.weeklyGoalDays, range: 0...7) { n in
                        n == 0 ? Text("Off") : Text("^[\(n) active day](inflect: true)")
                    }
                } header: {
                    Text("Goals")
                } footer: {
                    Text("The Dashboard tracks these.")
                }

                Section("Behavior") {
                    Toggle("Start the next phase automatically", isOn: $model.draft.autoStartNext)
                    StepperRow(title: "Stop after", value: $model.draft.maxSessionMinutes,
                               range: (0...480).including(c.maxSessionMinutes), step: 30) { m in
                        m == 0 ? Text("Never") : Text(verbatim: DurationText.minutes(m))
                    }
                }

                Section {
                    HexColorRow(title: "Focus", hex: $model.draft.focusColor, fallback: "#3AA0FF")
                    HexColorRow(title: "Break", hex: $model.draft.breakColor, fallback: "#2EE85E")
                } header: {
                    Text("Colors")
                } footer: {
                    SaveErrorFooter(error: model.saveError)
                }
            }
            .disabled(!model.isLoaded)
        }
        .formStyle(.grouped)
        .autosaves(model)
        .task(id: model.draft) {
            // Follow edits, lightly debounced; the preview route is open.
            guard (try? await Task.sleep(for: .milliseconds(300))) != nil else { return }
            if let p = try? await env.preview.fetchPomodoroPreview(model.draft) { preview = p }
        }
        .reloads { await model.load() }
    }

    private func frame(_ card: String) -> CardFrame? {
        preview?.frames.first { $0.card == card }
    }
}

extension ClosedRange where Bound == Int {
    /// The range, widened to include `value` so a setting made elsewhere
    /// isn't clamped the moment the stepper is touched.
    func including(_ value: Int) -> ClosedRange<Int> {
        Swift.min(lowerBound, value)...Swift.max(upperBound, value)
    }
}
