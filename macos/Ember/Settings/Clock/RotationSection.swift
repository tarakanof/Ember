import SwiftUI
import EmberKit

/// How the clock steps through its apps, and the ambient overlay on top.
struct RotationSection: View {
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        let s = device.settings
        @Bindable var display = device.display
        Section {
            StepperRow(title: "Time per app", value: Binding(
                get: { (s.draft.appDurationMs ?? 7000) / 1000 },
                set: { s.draft.appDurationMs = $0 * 1000 }),
                range: 2...60) { Text("\($0) s") }
            Toggle("Switch apps automatically", isOn: s.binding(\.autoTransition, true))
            Picker("Transition", selection: s.binding(\.transitionEffect, device.transitions.first ?? "Fade")) {
                let current = s.draft.transitionEffect
                ForEach(device.transitions, id: \.self) { Text(verbatim: DeviceKnownValues.displayName($0)).tag($0) }
                if let current, !device.transitions.contains(current) {
                    Text(verbatim: DeviceKnownValues.displayName(current)).tag(current)
                }
            }
            StepperRow(title: "Transition length", value: s.binding(\.transitionDurationMs, 400),
                       range: 100...2000, step: 100) { Text("\($0) ms") }
            Picker("Overlay", selection: $display.draft.overlay) {
                Text("None").tag(String?.none)
                ForEach(device.overlays, id: \.self) { o in
                    Text(verbatim: DeviceKnownValues.displayName(o)).tag(Optional(o))
                }
            }
            .disabled(!display.isLoaded)
        } header: {
            Text("App Rotation")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("The overlay draws rain, snow or storms over whatever is showing.")
                SaveErrorFooter(error: display.saveError)
            }
        }
    }
}
