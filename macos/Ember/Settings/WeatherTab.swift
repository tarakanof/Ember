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

            if config.useNativeIcons {
                Section {
                    ForEach(Self.iconConditions, id: \.key) { row in
                        TextField(row.label, text: iconBinding(row.key),
                                  prompt: Text(row.placeholder))
                    }
                } header: {
                    Text("Native icon IDs")
                } footer: {
                    Text("LaMetric icon IDs (developer.lametric.com/icons). Leave blank to use the built-in default.")
                        .font(.caption).foregroundStyle(.secondary)
                }
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

    /// The six condition buckets + their built-in default IDs (shown as the
    /// field placeholder). Keys match the server's render condition constants.
    static let iconConditions: [(key: String, label: String, placeholder: String)] = [
        ("clear", "Clear", "1338"),
        ("clouds", "Clouds", "2286"),
        ("fog", "Fog", "17056"),
        ("rain", "Rain", "72"),
        ("snow", "Snow", "2289"),
        ("storm", "Storm", "11428"),
    ]

    /// Binds a condition's override ID; an empty value removes the key so the
    /// server falls back to its default.
    private func iconBinding(_ key: String) -> Binding<String> {
        Binding(
            get: { config.iconIds[key] ?? "" },
            set: { newValue in
                let trimmed = newValue.trimmingCharacters(in: .whitespaces)
                if trimmed.isEmpty { config.iconIds[key] = nil } else { config.iconIds[key] = trimmed }
            }
        )
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
