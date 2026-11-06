import SwiftUI
import EmberKit

struct ClockPage<Content: View>: View {
    @Environment(DeviceSettingsModel.self) private var device
    @ViewBuilder var content: Content

    var body: some View {
        Form {
            LoadStateSection(isLoaded: device.isLoaded, error: device.loadError,
                             offMessage: "This server can't reach a clock. Check the clock's address below or discover one.",
                             retry: { await device.load(force: true) })
            Group { content }
                .disabled(!device.isLoaded)
        }
        .formStyle(.grouped)
        .autosaves(device.settings)
        .autosaves(device.display)
        .autosaves(device.sensors)
        .reloads { await device.load() }
    }
}

struct ClockStatusPane: View {
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        Form {
            LoadStateSection(isLoaded: device.isLoaded, error: device.loadError,
                             offMessage: "This server can't reach a clock. Check the clock's address below or discover one.",
                             retry: { await device.load(force: true) })
            ClockStatusSection()
        }
        .formStyle(.grouped)
        .reloads { await device.load() }
    }
}

extension ConfigModel where T == DeviceSettings {
    func binding<V>(_ key: WritableKeyPath<DeviceSettings, V?>, _ fallback: V) -> Binding<V> {
        Binding(get: { self.draft[keyPath: key] ?? fallback },
                set: { self.draft[keyPath: key] = $0 })
    }

    func binding<O, V>(_ object: WritableKeyPath<DeviceSettings, O?>, _ key: WritableKeyPath<O, V?>,
                       _ fallback: V, empty: O) -> Binding<V> {
        Binding(get: { self.draft[keyPath: object]?[keyPath: key] ?? fallback },
                set: { value in
                    var o = self.draft[keyPath: object] ?? empty
                    o[keyPath: key] = value
                    self.draft[keyPath: object] = o
                })
    }

    func option<E: RawRepresentable & Sendable>(_ key: WritableKeyPath<DeviceSettings, String?>, _ fallback: E) -> Binding<E>
    where E.RawValue == String {
        Binding(get: { self.draft[keyPath: key].flatMap { E(rawValue: $0) } ?? fallback },
                set: { self.draft[keyPath: key] = $0.rawValue })
    }
}
