import SwiftUI
import EmberKit

struct WeatherTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var config = WeatherConfig()
    @State private var save: SaveState = .idle
    @State private var loaded = false
    @State private var writer = DebouncedWriter(delay: .milliseconds(600))
    @State private var lastApplied: WeatherConfig?

    var body: some View {
        Form {
            Section {
                Toggle("Enable weather", isOn: $config.enabled)
            } footer: {
                Text("Conditions are fetched server-side from a free, key-less provider and shown as a rotating tile plus optional popups.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Location") {
                Picker("Provider", selection: $config.provider) {
                    Text("Open-Meteo").tag("open-meteo")
                    Text("MET Norway").tag("met-no")
                }
                TextField("Name (optional)", text: $config.locationName, prompt: Text("Amsterdam"))
                TextField("Latitude", value: $config.latitude, format: .number.precision(.fractionLength(0...4)))
                TextField("Longitude", value: $config.longitude, format: .number.precision(.fractionLength(0...4)))
                Picker("Units", selection: $config.units) {
                    Text("Metric (°C)").tag("metric")
                    Text("Imperial (°F)").tag("imperial")
                }
            }

            Section("Tile") {
                Toggle("Show rotating tile", isOn: $config.rotateInApps)
                Stepper("Refresh every \(config.refreshMinutes) min",
                        value: $config.refreshMinutes, in: 5...120, step: 5)
            }

            Section {
                Toggle("Popup on condition change", isOn: $config.popupOnChange)
                Stepper(config.popupIntervalMinutes == 0
                        ? "Interval popup: off"
                        : "Interval popup every \(config.popupIntervalMinutes) min",
                        value: $config.popupIntervalMinutes, in: 0...360, step: 30)
                Stepper("Popup duration \(config.popupDurationSeconds) s",
                        value: $config.popupDurationSeconds, in: 5...120, step: 5)
                Toggle("Severe-weather alert (with sound)", isOn: $config.severeAlert)
                TextField("Severe sound", text: $config.severeSound, prompt: Text("RTTTL or name"))
                    .disabled(!config.severeAlert)
                Toggle("Use native animated icons in popups", isOn: $config.useNativeIcons)
            } header: {
                Text("Popups")
            } footer: {
                statusCaption
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Weather")
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
        guard loaded else { return }
        guard config != lastApplied else { return }
        save = .saving
        let cfg = config
        writer.schedule {
            do {
                try await env.weather.putConfig(cfg)
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
            config = try await env.weather.getConfig()
            lastApplied = config
        } catch {
            save = .error("Couldn't load weather config (server offline?).")
        }
    }
}
