import SwiftUI
import EmberKit

struct DayClockPane: View {
    @Environment(AppEnvironment.self) private var env

    private var model: ServerConfigModel<DayConfig> { env.settings.day }

    var body: some View {
        @Bindable var model = model
        let c = model.draft
        Form {
            LoadStateSection(isLoaded: model.isLoaded, error: model.loadError,
                             offMessage: "The Day Clock needs a newer Ember server. Update the server.",
                             retry: { await model.load() })

            Group {
                Section {
                    Toggle("Enable Day Clock", isOn: $model.draft.enabled)
                } footer: {
                    Text("Clock in and out on the knob. While you're off the clock Ember stays quiet, and the Dashboard shows when your days end against your stop time.")
                }

                Section {
                    DatePicker("Default stop time", selection: hourMinuteBinding($model.draft.defaultStop),
                               displayedComponents: .hourAndMinute)
                    StepperRow(title: "Break hint", value: $model.draft.breakHintMinutes,
                               range: (0...240).including(c.breakHintMinutes), step: 15) { n in
                        n == 0 ? Text("Off") : Text("after \(n) min", comment: "Break hint setting value: minutes on the clock before the hint.")
                    }
                    StepperRow(title: "Auto clock-out", value: $model.draft.autoStopMinutes,
                               range: (5...180).including(c.autoStopMinutes), step: 5) { Text("after \($0) min") }
                    StepperRow(title: "Grace", value: $model.draft.graceMinutes,
                               range: (0...120).including(c.graceMinutes), step: 5) { Text("\($0) min") }
                } header: {
                    Text("Stop time")
                } footer: {
                    Text("The knob opens on the default stop time when you clock in. The break hint appears after that long on the clock without a break. Past your stop time with no reply for the auto clock-out time, the day ends by itself. A day counts as in range when you stop within the grace time of your stop time.")
                }

                SaveErrorFooter(error: model.saveError)
            }
            .disabled(!model.isLoaded)
        }
        .formStyle(.grouped)
        .autosaves(model)
        .reloads { await env.settings.day.load() }
    }
}
