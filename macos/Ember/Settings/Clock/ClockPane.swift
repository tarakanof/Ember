import SwiftUI
import EmberKit

/// The AWTRIX clock's own settings, proxied through the server
/// (`/v1/device/*`). Everything auto-applies through `DeviceSettingsModel`;
/// the clock's live display and quick actions live in the menu and the
/// Dashboard, not here.
struct ClockPane: View {
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        Form {
            LoadStateSection(isLoaded: device.isLoaded, error: device.loadError,
                             offMessage: "This server can't reach a clock. Check the clock's address below or discover one.",
                             retry: { await device.load(force: true) })
            ClockStatusSection()
            Group {
                DisplaySection()
                RotationSection()
                NativeAppsSection()
                TimeDateSection()
                SensorsSection()
                ButtonsSection()
            }
            .disabled(!device.isLoaded)
        }
        .formStyle(.grouped)
        .autosaves(device.settings)
        .autosaves(device.display)
        .autosaves(device.sensors)
        .reloads { await device.load() }
    }
}

extension ConfigModel where T == DeviceSettings {
    /// A control binding over an optional settings field, showing `fallback`
    /// until the clock reports a value.
    func binding<V>(_ key: WritableKeyPath<DeviceSettings, V?>, _ fallback: V) -> Binding<V> {
        Binding(get: { self.draft[keyPath: key] ?? fallback },
                set: { self.draft[keyPath: key] = $0 })
    }

    /// A field of a nested settings object, creating the object on first write.
    func binding<O, V>(_ object: WritableKeyPath<DeviceSettings, O?>, _ key: WritableKeyPath<O, V?>,
                       _ fallback: V, empty: O) -> Binding<V> {
        Binding(get: { self.draft[keyPath: object]?[keyPath: key] ?? fallback },
                set: { value in
                    var o = self.draft[keyPath: object] ?? empty
                    o[keyPath: key] = value
                    self.draft[keyPath: object] = o
                })
    }

    /// A raw string field as a typed option.
    func option<E: RawRepresentable & Sendable>(_ key: WritableKeyPath<DeviceSettings, String?>, _ fallback: E) -> Binding<E>
    where E.RawValue == String {
        Binding(get: { self.draft[keyPath: key].flatMap { E(rawValue: $0) } ?? fallback },
                set: { self.draft[keyPath: key] = $0.rawValue })
    }
}
