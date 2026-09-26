import SwiftUI
import EmberKit

/// The clock's built-in apps: which run, in what order, in which colour.
/// Ember's own tiles (pushed apps) are left out so they can't be switched
/// off here by accident.
struct NativeAppsSection: View {
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        let s = device.settings
        let apps = device.nativeApps
        Section {
            if apps.isEmpty {
                Text("The clock hasn't reported its apps yet.").foregroundStyle(.secondary)
            }
            ForEach(Array(apps.enumerated()), id: \.element.id) { index, app in
                Toggle(isOn: Binding(
                    get: { app.enabled },
                    set: { on in Task { await device.setApp(app.name, enabled: on) } })) {
                    Text(title(app.name))
                }
                .contextMenu {
                    Button("Move Up") { Task { await device.moveApps(fromOffsets: [index], toOffset: index - 1) } }
                        .disabled(index == 0)
                    Button("Move Down") { Task { await device.moveApps(fromOffsets: [index], toOffset: index + 2) } }
                        .disabled(index == apps.count - 1)
                }
                .accessibilityActions {
                    if index > 0 {
                        Button("Move Up") { Task { await device.moveApps(fromOffsets: [index], toOffset: index - 1) } }
                    }
                    if index < apps.count - 1 {
                        Button("Move Down") { Task { await device.moveApps(fromOffsets: [index], toOffset: index + 2) } }
                    }
                }
            }
            .onMove { from, to in Task { await device.moveApps(fromOffsets: from, toOffset: to) } }
            Picker("Temperature unit", selection: s.binding(\.useCelsius, true)) {
                Text("Celsius").tag(true)
                Text("Fahrenheit").tag(false)
            }
        } header: {
            Text("Built-in Apps")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Drag to reorder, or Control-click an app. Changes apply at once.")
                if let e = device.actionErrors[.apps] {
                    Label { Text(e.saveMessage) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .foregroundStyle(.red)
                }
            }
        }

        Section {
            let text = s.draft.textColor ?? "#FFFFFF"
            let inherit = device.supportsNG11
            InheritableColorRow(title: "Time", hex: s.binding(\.timeColor), inherited: text, supportsInherit: inherit)
            InheritableColorRow(title: "Date", hex: s.binding(\.dateColor), inherited: text, supportsInherit: inherit)
            InheritableColorRow(title: "Temperature", hex: s.binding(\.temperatureColor), inherited: text,
                                supportsInherit: inherit)
            InheritableColorRow(title: "Humidity", hex: s.binding(\.humidityColor), inherited: text,
                                supportsInherit: inherit)
            InheritableColorRow(title: "Battery", hex: s.binding(\.batteryColor), inherited: text,
                                supportsInherit: inherit)
        } header: {
            Text("App Colors")
        } footer: {
            if device.firmwareTooOld {
                Text("Update the clock's firmware to NG 1.1 or later to let an app follow the text color again.")
            } else if !device.supportsNG11 {
                Text("Update the Ember server to let an app follow the text color again.")
            }
        }
    }

    private func title(_ name: String) -> LocalizedStringKey {
        switch name {
        case "Time": "Time"
        case "Date": "Date"
        case "Temperature": "Temperature"
        case "Humidity": "Humidity"
        case "Battery": "Battery"
        default: LocalizedStringKey(name)
        }
    }
}

extension ConfigModel where T == DeviceSettings {
    /// An optional field as is (nil = inherit).
    func binding(_ key: WritableKeyPath<DeviceSettings, String?>) -> Binding<String?> {
        Binding(get: { self.draft[keyPath: key] }, set: { self.draft[keyPath: key] = $0 })
    }
}
