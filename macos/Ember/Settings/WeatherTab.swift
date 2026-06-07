import SwiftUI
import AppKit
import CoreLocation
import EmberKit

struct WeatherTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var config = WeatherConfig()
    @State private var save: SaveState = .idle
    @State private var loaded = false
    @State private var writer = DebouncedWriter(delay: .milliseconds(600))
    @State private var lastApplied: WeatherConfig?
    @State private var locating = false
    @State private var locateError: String?
    @State private var locateNeedsSettings = false

    var body: some View {
        Form {
            Section {
                Toggle("Enable weather", isOn: $config.enabled)
            } footer: {
                Text("Conditions are fetched server-side from a free, key-less provider and shown as a rotating tile plus optional popups.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Location") {
                locationAccessRow(status: env.location.authStatus)
                Picker("Provider", selection: $config.provider) {
                    Text("Open-Meteo").tag("open-meteo")
                    Text("MET Norway").tag("met-no")
                }
                TextField("Name (optional)", text: $config.locationName, prompt: Text("Amsterdam"))
                TextField("Latitude", value: $config.latitude, format: .number.precision(.fractionLength(0...4)))
                TextField("Longitude", value: $config.longitude, format: .number.precision(.fractionLength(0...4)))
                Button {
                    Task { await detectLocation(force: true) }
                } label: {
                    Label(locating ? "Locating…" : "Use current location", systemImage: "location")
                }
                .disabled(locating)
                if let locateError {
                    Text(locateError).font(.caption).foregroundStyle(.red)
                    if locateNeedsSettings {
                        Button("Open Location Settings…") { openLocationSettings() }
                            .font(.caption)
                    }
                }
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
                Toggle("Show forecast tile", isOn: $config.forecastTile)
                Stepper("Forecast hours: \(config.forecastHours)",
                        value: $config.forecastHours, in: 6...24, step: 1)
                Toggle("Sunrise / sunset alerts", isOn: $config.sunPopups)
                Toggle("Moon phase on clear nights", isOn: $config.moonPhase)
            } header: {
                Text("Forecast & sky")
            } footer: {
                Text("The forecast tile shows hourly temperature bars (height + colour = temperature). The weather tile also carries a one-pixel-per-hour strip. Sunrise/sunset and moon use your location, computed on-device.")
                    .font(.caption).foregroundStyle(.secondary)
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
        // Refresh the access row on appear so a toggle flipped in System Settings
        // (or another launch's grant) shows without relaunching.
        .task { env.location.refreshAuthorization() }
        .task {
            if !loaded {
                await load()
                loaded = true
                await detectLocation(force: false)
            }
        }
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

    private func detectLocation(force: Bool) async {
        if !force && (config.latitude != 0 || config.longitude != 0) { return }
        locating = true; locateError = nil; locateNeedsSettings = false
        do {
            let fix = try await env.location.current()
            // The auto path (force:false) must not clobber coordinates the user
            // started typing while detection was in flight.
            if !force && (config.latitude != 0 || config.longitude != 0) { locating = false; return }
            config.latitude = (fix.latitude * 10000).rounded() / 10000
            config.longitude = (fix.longitude * 10000).rounded() / 10000
            if let name = fix.name, (force || config.locationName.isEmpty) { config.locationName = name }
        } catch LocationService.LocationError.denied {
            // Toggle is off for Ember; only the auto path stays quiet.
            locateNeedsSettings = force
            if force { locateError = "Location is off for Ember. Turn it on in System Settings ▸ Privacy & Security ▸ Location Services, then try again." }
        } catch LocationService.LocationError.authorizationUnavailable {
            // macOS never showed the permission prompt — usual for menu-bar apps.
            locateNeedsSettings = force
            if force { locateError = "macOS didn't show a location prompt (common for menu-bar apps). Enable Location for Ember in System Settings ▸ Privacy & Security ▸ Location Services, then try again." }
        } catch {
            if force { locateError = "Couldn't get your location — enter coordinates manually." }
        }
        locating = false
    }

    /// Persistent access-state row, mirroring the Reminders tab. Unlike EventKit,
    /// CoreLocation's prompt is unreliable for a menu-bar (accessory) app, so the
    /// "not enabled" state offers a best-effort in-app request AND the reliable
    /// System Settings deep-link.
    @ViewBuilder private func locationAccessRow(status: CLAuthorizationStatus) -> some View {
        switch status {
        case .authorizedWhenInUse, .authorizedAlways:
            Label("Location access granted", systemImage: "checkmark.circle.fill")
                .font(.caption).foregroundStyle(.green)
        case .denied, .restricted:
            VStack(alignment: .leading, spacing: 4) {
                Label("Location access denied", systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.red)
                Text("Allow Location for Ember in System Settings ▸ Privacy & Security ▸ Location Services, then reopen this tab.")
                    .font(.caption2).foregroundStyle(.secondary)
                Button("Open Location Settings…") { openLocationSettings() }
            }
        default:   // .notDetermined and any future case
            VStack(alignment: .leading, spacing: 4) {
                Label("Location access not enabled", systemImage: "circle.dashed")
                    .font(.caption).foregroundStyle(.secondary)
                Text("Menu-bar apps don't get a location prompt, so enable Ember manually in System Settings ▸ Privacy & Security ▸ Location Services.")
                    .font(.caption2).foregroundStyle(.secondary)
                Button("Open Location Settings…") { openLocationSettings() }
            }
        }
    }

    /// Opens System Settings straight to the Location Services pane so the user
    /// can flip Ember on. Each ad-hoc reinstall resets that toggle, so this is the
    /// path back from a denied/never-prompted state.
    private func openLocationSettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_LocationServices") {
            NSWorkspace.shared.open(url)
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
