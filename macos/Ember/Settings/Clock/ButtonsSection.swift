import SwiftUI
import EmberKit

/// Whether the clock's buttons drive Pomodoro through this server.
struct ButtonsSection: View {
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        Section {
            Toggle("Clock buttons control Pomodoro", isOn: Binding(
                get: { device.buttons?.configured ?? false },
                set: { on in Task { await device.setButtonsEnabled(on) } }))
                .disabled(device.buttons == nil)
            LabeledContent("Last press") {
                if let unix = device.buttons?.lastPressUnix, unix > 0 {
                    Text(Date(timeIntervalSince1970: TimeInterval(unix)), format: .relative(presentation: .named))
                } else {
                    Text("None yet").foregroundStyle(.secondary)
                }
            }
        } header: {
            Text("Buttons")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Middle starts, pauses or resumes; left stops; right skips. Turning this on points the clock at this server.")
                if let e = device.actionErrors[.buttons] {
                    Label { Text(e.saveMessage) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .foregroundStyle(.red)
                }
            }
        }
    }
}
