import SwiftUI
import CoreLocation
import EmberKit

/// Weather tiles and popups. Server-wide; the location can come from this
/// Mac. The severe-weather sound is under Sounds & Alerts.
struct WeatherPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var preview = PreviewModel()
    @State private var locating = false
    @State private var locateError: LocalizedStringKey?
    @AppStorage("weatherFold.icons") private var iconsExpanded = false

    private var model: ServerConfigModel<WeatherConfig> { env.settings.weather }

    var body: some View {
        @Bindable var model = model
        let c = model.draft
        Form {
            Section {
                VStack(alignment: .leading, spacing: 14) {
                    PanelPreview(title: "Current conditions",
                                 caption: "Condition icon, temperature and a strip for the next ^[\(c.forecastHours) hour](inflect: true), blue for cold to red for warm.",
                                 enabled: c.enabled && c.rotateInApps, frame: frame("weather"))
                    PanelPreview(title: "Hourly forecast",
                                 caption: "Bars whose height and color follow the temperature.",
                                 enabled: c.enabled && c.forecastTile, frame: frame("forecast"))
                    PanelPreview(title: "Air quality",
                                 caption: "European AQI in its scale color, with the next 24 hours below.",
                                 enabled: c.enabled && c.airTile, frame: frame("air"))
                }
                .settingsPreviewRow()
            } footer: {
                Text("Animated icons and rain or snow overlays appear on the clock only.")
            }

            LoadStateSection(isLoaded: model.isLoaded, error: model.loadError,
                             offMessage: "Weather is off on the server, or the server is too old.",
                             retry: { await model.load() })

            Group {
                Section {
                    Toggle("Enable weather", isOn: $model.draft.enabled)
                } footer: {
                    Text("The server fetches conditions from a free provider that needs no key.")
                }

                Group {
                    locationSection(model)
                    tilesSection(model)
                    popupsSection(model)
                    if c.useNativeIcons || c.tileNativeIcons {
                        Section(isExpanded: $iconsExpanded) {
                            ForEach(Self.iconConditions, id: \.key) { row in
                                TextField(row.label, text: iconBinding(row.key), prompt: Text(verbatim: row.placeholder))
                            }
                        } header: {
                            Text("Native Icon IDs")
                        }
                    }
                }
                .disabled(!c.enabled)
            }
            .disabled(!model.isLoaded)
        }
        .formStyle(.grouped)
        .autosaves(model)
        .previews(previewDraft, into: preview) { try await env.preview.fetchWeatherPreview($0) }
        .reloads {
            env.location.refreshAuthorization()
            await model.load()
            // First run: fill in this Mac's location when none is set.
            if model.isLoaded, model.draft.latitude == 0, model.draft.longitude == 0 {
                await locate(quietly: true)
            }
        }
    }

    // MARK: Sections

    private func locationSection(_ model: ServerConfigModel<WeatherConfig>) -> some View {
        @Bindable var model = model
        return Section {
            Picker("Provider", selection: $model.draft.provider) {
                Text(verbatim: "Open-Meteo").tag("open-meteo")
                Text(verbatim: "MET Norway").tag("met-no")
            }
            TextField("Name", text: $model.draft.locationName, prompt: Text("Shown in popups"))
            LabeledContent("Coordinates") {
                HStack(spacing: 4) {
                    TextField("Latitude", value: $model.draft.latitude,
                              format: .number.precision(.fractionLength(0...4)), prompt: Text(verbatim: "52.37"))
                        .labelsHidden()
                        .multilineTextAlignment(.trailing)
                        .frame(maxWidth: 90)
                    Text(verbatim: "°").accessibilityHidden(true)
                    TextField("Longitude", value: $model.draft.longitude,
                              format: .number.precision(.fractionLength(0...4)), prompt: Text(verbatim: "4.90"))
                        .labelsHidden()
                        .multilineTextAlignment(.trailing)
                        .frame(maxWidth: 90)
                    Text(verbatim: "°").accessibilityHidden(true)
                }
            }
            LabeledContent {
                Button("Use Current Location") { Task { await locate() } }
                    .disabled(locating)
            } label: {
                locationAccess
            }
            Picker("Units", selection: $model.draft.units) {
                Text("Metric (°C)").tag("metric")
                Text("Imperial (°F)").tag("imperial")
            }
        } header: {
            Text("Location")
        } footer: {
            let badCoordinates = abs(model.draft.latitude) > 90 || abs(model.draft.longitude) > 180
            if badCoordinates || locateError != nil || model.saveError != nil {
                locationFooter(model)
            }
        }
    }

    private func locationFooter(_ model: ServerConfigModel<WeatherConfig>) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if abs(model.draft.latitude) > 90 || abs(model.draft.longitude) > 180 {
                Label("Latitude runs from −90 to 90 and longitude from −180 to 180.",
                      systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
            }
            if let locateError {
                Label(locateError, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                Button("Open Location Settings…") { openLocationSettings() }
            }
            SaveErrorFooter(error: model.saveError)
        }
    }

    @ViewBuilder private var locationAccess: some View {
        switch env.location.authStatus {
        case .authorizedAlways, .authorizedWhenInUse:
            Label("Location access on", systemImage: "location.fill").foregroundStyle(.secondary)
        case .denied, .restricted:
            Label("Location access off", systemImage: "location.slash").foregroundStyle(.secondary)
        default:
            Label("Location access not set", systemImage: "location").foregroundStyle(.secondary)
        }
    }

    private func tilesSection(_ model: ServerConfigModel<WeatherConfig>) -> some View {
        @Bindable var model = model
        let c = model.draft
        return Section {
            Toggle("Current conditions", isOn: $model.draft.rotateInApps)
            Group {
                Toggle("Native animated icon", isOn: $model.draft.tileNativeIcons)
                Toggle("Moon phase at night", isOn: $model.draft.moonPhase)
                Toggle("Rain and snow overlay", isOn: $model.draft.overlay)
            }
            .disabled(!c.rotateInApps)
            Toggle("Hourly forecast", isOn: $model.draft.forecastTile)
            StepperRow(title: "Hours ahead", value: $model.draft.forecastHours,
                       range: (6...24).including(c.forecastHours), step: 6) { Text("\($0) h") }
                .disabled(!c.rotateInApps && !c.forecastTile)
            Toggle("Air quality", isOn: $model.draft.airTile)
            StepperRow(title: "Air quality alert from", value: $model.draft.airPopupThreshold,
                       range: 0...200, step: 10) { n in n == 0 ? Text("Off") : Text("AQI \(n)") }
            StepperRow(title: "Refresh every", value: $model.draft.refreshMinutes,
                       range: (5...60).including(c.refreshMinutes), step: 5) { Text("\($0) min") }
        } header: {
            Text("Tiles")
        } footer: {
            Text("European AQI: up to 20 good, 40 fair, 60 moderate, 80 poor, 100 very poor, above that extreme. The alert pops up once as the AQI crosses the level.")
        }
    }

    private func popupsSection(_ model: ServerConfigModel<WeatherConfig>) -> some View {
        @Bindable var model = model
        return Section {
            Toggle("When conditions change", isOn: $model.draft.popupOnChange)
            Toggle("Sunrise and sunset", isOn: $model.draft.sunPopups)
            StepperRow(title: "Every", value: $model.draft.popupIntervalMinutes,
                       range: (0...360).including(model.draft.popupIntervalMinutes), step: 30) { m in
                m == 0 ? Text("Off") : Text(verbatim: DurationText.minutes(m))
            }
            StepperRow(title: "Show for", value: $model.draft.popupDurationSeconds,
                       range: (5...120).including(model.draft.popupDurationSeconds), step: 5) { Text("\($0) s") }
            Toggle("Severe weather alert", isOn: $model.draft.severeAlert)
            Toggle("Native icons in popups", isOn: $model.draft.useNativeIcons)
        } header: {
            Text("Popups")
        } footer: {
            Text("The severe weather alert plays a sound; choose it in Sounds & Alerts.")
        }
    }

    // MARK: Helpers

    static let iconConditions: [(key: String, label: LocalizedStringKey, placeholder: String)] = [
        ("clear", "Clear", "1338"),
        ("clouds", "Clouds", "2286"),
        ("fog", "Fog", "17056"),
        ("rain", "Rain", "72"),
        ("snow", "Snow", "2289"),
        ("storm", "Storm", "11428"),
    ]

    /// An empty ID removes the override, so the server uses its default.
    private func iconBinding(_ key: String) -> Binding<String> {
        Binding(
            get: { model.draft.iconIds[key] ?? "" },
            set: { v in
                let t = v.trimmingCharacters(in: .whitespaces)
                model.draft.iconIds[key] = t.isEmpty ? nil : t
            })
    }

    private func frame(_ card: String) -> CardFrame? { preview.frame(card) }

    /// Always asks for every tile: the toggles dim the panels here instead of
    /// removing them, so each option stays visible.
    private var previewDraft: WeatherConfig {
        var draft = model.draft
        draft.rotateInApps = true
        draft.forecastTile = true
        draft.airTile = true
        return draft
    }

    private func locate(quietly: Bool = false) async {
        locating = true
        locateError = nil
        defer { locating = false }
        do {
            let fix = try await env.location.current()
            // The quiet first-run path must not overwrite coordinates typed meanwhile.
            if quietly, model.draft.latitude != 0 || model.draft.longitude != 0 { return }
            model.draft.latitude = (fix.latitude * 10000).rounded() / 10000
            model.draft.longitude = (fix.longitude * 10000).rounded() / 10000
            if let name = fix.name, !quietly || model.draft.locationName.isEmpty { model.draft.locationName = name }
        } catch where quietly {
            return
        } catch LocationService.LocationError.denied {
            locateError = "Location is off for Ember. Turn it on in System Settings › Privacy & Security › Location Services."
        } catch LocationService.LocationError.authorizationUnavailable {
            locateError = "macOS didn't ask for location access. Turn it on for Ember in System Settings › Privacy & Security › Location Services."
        } catch {
            locateError = "Couldn't find your location. Enter the coordinates instead."
        }
    }

    private func openLocationSettings() {
        openSystemSettings("x-apple.systempreferences:com.apple.preference.security?Privacy_LocationServices")
    }
}
