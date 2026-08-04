import SwiftUI
import AppKit
import EmberKit

/// AWTRIX clock settings, proxied through the Ember server (/v1/device/*). Mirrors
/// the awtrix-ng app's surface: General, Overlay, Native Apps, Time & Date, Sensors,
/// Buttons. Auto-applies on change (debounced), matching the other settings tabs.
struct DeviceTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var settings = DeviceSettings()
    @State private var stats: DeviceStats?
    @State private var save: SaveState = .idle
    @State private var loaded = false
    @State private var loadError: String?
    @State private var writer = DebouncedWriter(delay: .milliseconds(600))
    @State private var lastApplied: DeviceSettings?
    @State private var confirmReboot = false
    @State private var config: DeviceConfig?
    /// The server answered, but it can't talk to the clock — the case app-side
    /// discovery exists to fix.
    @State private var clockUnreachable = false
    @State private var buttons: ButtonStatus?
    @State private var clockExpanded = false

    @State private var quiet = QuietConfig()
    @State private var lastQuiet: QuietConfig?
    @State private var quietWriter = DebouncedWriter(delay: .milliseconds(600))

    @State private var sensorsOnDevice: SensorCalibration?
    @State private var sensorsEdit = SensorCalibration()

    /// The Ulanzi build's compiled-in offsets, applied when the system object has
    /// no explicit override (-9 °C compensates the device's self-heating).
    /// Display-only.
    private let firmwareTempOffset = -9.0
    private let firmwareHumOffset = 0.0

    /// The clock's live effect/transition/overlay catalogue (GET
    /// /v1/device/capabilities). nil while loading or unreachable — pickers fall
    /// back to DeviceKnownValues.fallbackTransitions until it loads.
    @State private var capabilities: DeviceCapabilities?

    @State private var apps: [AppInfo] = []

    @State private var deviceDisplay = DeviceDisplay()
    @State private var lastDeviceDisplay: DeviceDisplay?
    @State private var displayWriter = DebouncedWriter(delay: .milliseconds(600))

    var body: some View {
        Form {
            screenSection

            if let loadError {
                Section {
                    Label(loadError, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption).foregroundStyle(.red)
                }
            }

            clockSection

            generalSection
            overlaySection
            quietSection
            nativeAppsSection
            sensorsSection
            timeDateSection
            buttonsSection
        }
        .formStyle(.grouped)
        .disabled(loadError != nil && !loaded)
        .toolbar {
            ToolbarItemGroup(placement: .navigation) {
                Button { Task { await perform { try await env.device.previousApp() } } } label: {
                    Image(systemName: "chevron.backward")
                }
                .help("Previous app")
                Button { Task { await perform { try await env.device.nextApp() } } } label: {
                    Image(systemName: "chevron.forward")
                }
                .help("Next app")
                Button { Task { await perform { try await env.device.dismiss() } } } label: {
                    Image(systemName: "bell.slash.fill")
                }
                .help("Dismiss notification")
                Button(role: .destructive) { confirmReboot = true } label: {
                    Image(systemName: "power")
                }
                .help("Reboot clock")
            }
            ToolbarItem { statusCaption }
            ToolbarItem { Button("Reload from clock") { Task { await load() } } }
        }
        .task {
            if !loaded {
                await load()
                loaded = true
            }
        }
        .task { await pollStats() }
        .onChange(of: settings) { _, _ in scheduleSave() }
        .onChange(of: deviceDisplay) { _, _ in scheduleDisplaySave() }
        .confirmationDialog("Reboot the clock?", isPresented: $confirmReboot, titleVisibility: .visible) {
            Button("Reboot", role: .destructive) { Task { await perform { try await env.device.reboot() } } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The clock will restart and be briefly unavailable.")
        }
    }

    // MARK: Sections

    /// Live mirror of the clock's display, like the AWTRIX mobile app's header.
    /// While the clock is unreachable the empty grid keeps the panel visible.
    @ViewBuilder private var screenSection: some View {
        Section {
            LiveMatrixMirror()
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity)
                .background(.black)
                .listRowInsets(EdgeInsets())
        }
    }

    @ViewBuilder private var clockSection: some View {
        Section(isExpanded: $clockExpanded) {
            LabeledContent { Text(config?.baseURL ?? "—").foregroundStyle(.secondary) } label: {
                RowLabel("Address", symbol: "globe", tint: .blue)
            }
            LabeledContent { Text(config?.source ?? "—").foregroundStyle(.secondary) } label: {
                RowLabel("Source", symbol: "point.3.connected.trianglepath.dotted", tint: .indigo)
            }
            LabeledContent { Text(stats?.batteryPercent.map { "\(Int($0))%" } ?? "—") } label: {
                RowLabel("Battery", symbol: "battery.75percent", tint: .green)
            }
            LabeledContent { Text(stats?.version ?? "—") } label: {
                RowLabel("Firmware", symbol: "cpu", tint: .gray)
            }
            LabeledContent { Text(stats?.temperature.map { String(format: "%.1f °C", $0) } ?? "—") } label: {
                RowLabel("Temperature", symbol: "thermometer.medium", tint: .orange)
            }
            LabeledContent { Text(stats?.humidity.map { String(format: "%.0f %%", $0) } ?? "—") } label: {
                RowLabel("Humidity", symbol: "humidity.fill", tint: .teal)
            }
            if clockUnreachable {
                Label("The server can't reach the clock. Find it from this Mac.",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
            }
            FindClockView(currentBaseURL: config?.baseURL) { await load() }
        } header: {
            Text("Clock")
        }
    }

    @ViewBuilder private var generalSection: some View {
        Section {
            Toggle(isOn: b(\.uppercase)) {
                RowLabel("Uppercase letters", symbol: "textformat", tint: .blue)
            }
            Toggle(isOn: b(\.blockNavigation)) {
                RowLabel("Block buttons", symbol: "hand.raised.fill", tint: .orange)
            }
            Toggle(isOn: b(\.autoBrightness)) {
                RowLabel("Auto brightness", symbol: "sun.max.fill", tint: .yellow)
            }
            sliderRow("Brightness", symbol: "sun.min.fill", tint: .yellow,
                      value: i(\.brightness, 80), range: 0...255) { "\($0)" }
                .disabled(b(\.autoBrightness).wrappedValue)
            sliderRow("Volume", symbol: "speaker.wave.2.fill", tint: .pink,
                      value: i(\.volume, 25), range: 0...30) { "\($0)" }
            sliderRow("App time", symbol: "timer", tint: .orange,
                      value: appDurationSecondsBinding, range: 1...60) { "\($0)s" }
            ColorHexPicker(title: "Text color", symbol: "paintpalette.fill", tint: .teal, hex: s(\.textColor, "#FFFFFF"))
            Toggle(isOn: b(\.autoTransition)) {
                RowLabel("Auto transition", symbol: "arrow.left.arrow.right", tint: .green)
            }
            transitionEffectPicker
            sliderRow("Transition speed", symbol: "gauge.with.needle", tint: .cyan,
                      value: i(\.transitionDurationMs, 400), range: 0...2000, step: 50) { "\($0)ms" }
            sliderRow("Scroll speed", symbol: "forward.fill", tint: .mint,
                      value: scroll(\.speed, 100), range: 10...500, step: 10) { "\($0)%" }
        } header: {
            Text("General")
        } footer: {
            Text("“Block buttons” may be briefly overridden while a Pomodoro is running.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// The device-reported transition effects (GET /v1/device/capabilities),
    /// falling back to DeviceKnownValues.fallbackTransitions while capabilities
    /// hasn't loaded or the endpoint is unreachable.
    private var transitionOptions: [String] {
        let live = capabilities?.transitions ?? []
        return live.isEmpty ? DeviceKnownValues.fallbackTransitions : live
    }

    @ViewBuilder private var transitionEffectPicker: some View {
        let current = s(\.transitionEffect, transitionOptions.first ?? "random").wrappedValue
        let all = transitionOptions.contains(current) ? transitionOptions : transitionOptions + [current]
        Picker(selection: s(\.transitionEffect, transitionOptions.first ?? "random")) {
            ForEach(all, id: \.self) { name in
                Text(DeviceKnownValues.displayName(name)).tag(name)
            }
        } label: {
            RowLabel("Transition effect", symbol: "sparkles", tint: .purple)
        }
    }

    @ViewBuilder private var overlaySection: some View {
        Section {
            Picker(selection: overlayBinding) {
                Text("None").tag(Optional<String>.none)
                ForEach(OverlayEffect.allCases) { o in
                    Text(o.displayName).tag(Optional(o.rawValue))
                }
            } label: {
                RowLabel("Overlay", symbol: "cloud.snow.fill", tint: .blue)
            }
        } header: {
            Text("Overlay")
        } footer: {
            Text("An ambient weather effect drawn over whatever app is currently showing.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private var overlayBinding: Binding<String?> {
        Binding(get: { deviceDisplay.overlay }, set: { deviceDisplay.overlay = $0 })
    }

    @ViewBuilder private var quietSection: some View {
        Section {
            Toggle(isOn: $quiet.enabled) {
                RowLabel("Mute sounds at night", symbol: "moon.zzz.fill", tint: .indigo)
            }
            DatePicker(selection: timeBinding($quiet.start), displayedComponents: .hourAndMinute) {
                RowLabel("From", symbol: "moon.fill", tint: .purple)
            }
            .disabled(!quiet.enabled)
            DatePicker(selection: timeBinding($quiet.end), displayedComponents: .hourAndMinute) {
                RowLabel("Until", symbol: "sunrise.fill", tint: .orange)
            }
            .disabled(!quiet.enabled)
        } header: {
            Text("Quiet hours")
        } footer: {
            Text("Server-side: all clock sounds (pomodoro, alarms, reminders, weather) are muted during the window. Visual notifications still show. Times are the server's local time.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .task {
            if lastQuiet == nil, let q = try? await env.quiet.getConfig() {
                quiet = q
                lastQuiet = q
            }
        }
        .onChange(of: quiet) { _, _ in scheduleQuietSave() }
    }

    private func scheduleQuietSave() {
        guard quiet != lastQuiet else { return } // initial load / no-op
        save = .saving
        let q = quiet
        quietWriter.schedule {
            do {
                try await env.quiet.putConfig(q)
                await MainActor.run { lastQuiet = q; save = .saved }
            } catch let e as APIError where e.isUnauthorized {
                await MainActor.run { save = .error("Unauthorized — check the token in Connection.") }
            } catch {
                await MainActor.run { save = .error("Save failed: \(error.localizedDescription)") }
            }
        }
    }

    /// Bridges an "HH:MM" string to the Date a .hourAndMinute DatePicker wants.
    private func timeBinding(_ hhmm: Binding<String>) -> Binding<Date> {
        Binding(
            get: {
                let parts = hhmm.wrappedValue.split(separator: ":").compactMap { Int($0) }
                var c = DateComponents()
                c.hour = parts.count > 0 ? parts[0] : 0
                c.minute = parts.count > 1 ? parts[1] : 0
                return Calendar.current.date(from: c) ?? .distantPast
            },
            set: { date in
                let c = Calendar.current.dateComponents([.hour, .minute], from: date)
                hhmm.wrappedValue = String(format: "%02d:%02d", c.hour ?? 0, c.minute ?? 0)
            }
        )
    }

    /// SF Symbol per known native app name; unrecognised (pushed/scripted) apps
    /// get a generic glyph.
    private func symbol(forApp name: String) -> String {
        switch name {
        case "Time": return "clock.fill"
        case "Date": return "calendar"
        case "Temperature": return "thermometer.medium"
        case "Humidity": return "humidity.fill"
        case "Battery": return "battery.50percent"
        default: return "square.grid.2x2"
        }
    }

    @ViewBuilder private var nativeAppsSection: some View {
        Section {
            if apps.isEmpty {
                Text("No native apps reported yet").font(.caption).foregroundStyle(.secondary)
            }
            ForEach(apps) { app in
                Toggle(isOn: Binding(get: { app.enabled }, set: { setAppEnabled(app.name, $0) })) {
                    RowLabel(app.name, symbol: symbol(forApp: app.name), tint: .blue)
                }
            }
        } header: {
            Text("Native Apps")
        } footer: {
            Text("Built-in clock apps shown in the rotation. Changes apply immediately — no reboot.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// Toggles one native app's enabled state and PUTs the full order/disabled
    /// set — NG has no per-app toggle endpoint, so every change re-sends the
    /// whole list (see AppsUpdate).
    private func setAppEnabled(_ name: String, _ enabled: Bool) {
        guard let idx = apps.firstIndex(where: { $0.name == name }) else { return }
        apps[idx].enabled = enabled
        let order = apps.map(\.name)
        let disabled = apps.filter { !$0.enabled }.map(\.name)
        save = .saving
        Task {
            do {
                try await env.device.updateApps(AppsUpdate(order: order, disabled: disabled))
                await MainActor.run { save = .saved }
            } catch let e as APIError where e.isUnauthorized {
                await MainActor.run { save = .error("Unauthorized — check the token in Connection.") }
            } catch {
                await MainActor.run { save = .error("Save failed: \(error.localizedDescription)") }
            }
        }
    }

    /// Calibration of the clock's internal temp/hum sensor. Applies live via
    /// PUT /v1/device/sensors — no reboot follows.
    @ViewBuilder private var sensorsSection: some View {
        Section {
            offsetRow("Temperature offset", symbol: "thermometer.and.liquid.waves", tint: .orange,
                      value: offsetBinding(\.tempOffset, firmwareTempOffset), step: 0.5,
                      measured: stats?.temperature, applied: sensorsOnDevice?.tempOffset ?? firmwareTempOffset,
                      unit: "°C")
            offsetRow("Humidity offset", symbol: "humidity", tint: .teal,
                      value: offsetBinding(\.humOffset, firmwareHumOffset), step: 1,
                      measured: stats?.humidity, applied: sensorsOnDevice?.humOffset ?? firmwareHumOffset,
                      unit: "%")
            HStack {
                Button("Reset to firmware defaults") {
                    sensorsEdit = SensorCalibration()
                }
                .disabled(sensorsEdit == SensorCalibration() && sensorsOnDevice == SensorCalibration())
                Spacer()
                Button("Apply calibration") { Task { await applySensors() } }
                    .disabled(sensorsOnDevice == nil || sensorsEdit == sensorsOnDevice)
            }
        } header: {
            Text("Sensor Calibration")
        } footer: {
            Text("The firmware default (\(Int(firmwareTempOffset)) °C) compensates the clock's self-heating. Compare the measured value with a trusted thermometer and adjust the offset by the difference. Applies immediately — no reboot.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// Stepper row with the measured reading and, when the offset differs from
    /// what the clock currently applies, a predicted post-apply value.
    private func offsetRow(_ title: String, symbol: String, tint: Color,
                           value: Binding<Double>, step: Double,
                           measured: Double?, applied: Double, unit: String) -> some View {
        LabeledContent {
            VStack(alignment: .trailing, spacing: 2) {
                Stepper(value: value, in: -50...50, step: step) {
                    Text(String(format: "%+.1f %@", value.wrappedValue, unit))
                        .monospacedDigit()
                        .frame(minWidth: 72, alignment: .trailing)
                }
                if let measured {
                    if value.wrappedValue != applied {
                        Text(String(format: "now %.1f → %.1f %@ after apply",
                                    measured, measured - applied + value.wrappedValue, unit))
                            .font(.caption).foregroundStyle(.secondary).monospacedDigit()
                    } else {
                        Text(String(format: "measured %.1f %@", measured, unit))
                            .font(.caption).foregroundStyle(.secondary).monospacedDigit()
                    }
                }
            }
        } label: {
            RowLabel(title, symbol: symbol, tint: tint)
        }
    }

    private func offsetBinding(_ kp: WritableKeyPath<SensorCalibration, Double?>,
                               _ def: Double) -> Binding<Double> {
        Binding(get: { sensorsEdit[keyPath: kp] ?? def }, set: { sensorsEdit[keyPath: kp] = $0 })
    }

    private func applySensors() async {
        save = .saving
        do {
            try await env.device.updateSensors(sensorsEdit)
            sensorsOnDevice = sensorsEdit
            save = .saved
        } catch let e as APIError where e.isUnauthorized {
            save = .error("Unauthorized — check the token in Connection.")
        } catch {
            save = .error("Calibration failed: \(error.localizedDescription)")
        }
    }

    @ViewBuilder private var timeDateSection: some View {
        Section {
            Toggle(isOn: b(\.time24h)) {
                RowLabel("24-hour clock", symbol: "clock.badge", tint: .blue)
            }
            Toggle(isOn: b(\.timeLeadingZero)) {
                RowLabel("Leading zero", symbol: "0.square", tint: .blue)
            }
            Toggle(isOn: b(\.timeShowSeconds)) {
                RowLabel("Show seconds", symbol: "timer", tint: .blue)
            }
            Toggle(isOn: b(\.timeShowAmPm)) {
                RowLabel("Show AM/PM", symbol: "a.square", tint: .blue)
            }
            .disabled(b(\.time24h).wrappedValue)
            Picker(selection: enumBinding(\.timeSeparatorMode, TimeSeparatorMode.steady)) {
                ForEach(TimeSeparatorMode.allCases) { m in Text(m.displayName).tag(m) }
            } label: {
                RowLabel("Separator style", symbol: "circle.grid.2x2", tint: .indigo)
            }
            LabeledContent { Text(timePreviewText).monospacedDigit().foregroundStyle(.secondary) } label: {
                RowLabel("Preview", symbol: "eye", tint: .gray)
            }
            Picker(selection: i(\.timeMode, 1)) {
                ForEach(0...6, id: \.self) { m in
                    Text("Style \(m)").tag(m)
                }
                if !(0...6).contains(i(\.timeMode, 1).wrappedValue) {
                    Text("Style \(i(\.timeMode, 1).wrappedValue)").tag(i(\.timeMode, 1).wrappedValue)
                }
            } label: {
                RowLabel("Time style", symbol: "squares.below.rectangle", tint: .indigo)
            }

            Picker(selection: enumBinding(\.dateOrder, DateOrder.dayMonthYear)) {
                ForEach(DateOrder.allCases) { o in Text(o.displayName).tag(o) }
            } label: {
                RowLabel("Date order", symbol: "calendar.badge.clock", tint: .red)
            }
            Picker(selection: enumBinding(\.dateSeparator, DateSeparator.dot)) {
                ForEach(DateSeparator.allCases) { sep in Text(sep.displayName).tag(sep) }
            } label: {
                RowLabel("Date separator", symbol: "textformat", tint: .red)
            }
            Picker(selection: enumBinding(\.dateYearMode, DateYearMode.twoDigit)) {
                ForEach(DateYearMode.allCases) { y in Text(y.displayName).tag(y) }
            } label: {
                RowLabel("Year", symbol: "calendar", tint: .red)
            }
            Toggle(isOn: b(\.dateShowWeekday)) {
                RowLabel("Show weekday", symbol: "calendar.day.timeline.left", tint: .purple)
            }
            Toggle(isOn: b(\.dateMonthNames)) {
                RowLabel("Show month names", symbol: "textformat.abc", tint: .purple)
            }
            LabeledContent { Text(datePreviewText).monospacedDigit().foregroundStyle(.secondary) } label: {
                RowLabel("Preview", symbol: "eye", tint: .gray)
            }

            ColorHexPicker(title: "Calendar header", symbol: "calendar.circle.fill", tint: .red, hex: s(\.calendarHeaderColor, "#FF0000"))
            ColorHexPicker(title: "Calendar body", symbol: "square.fill", tint: .gray, hex: s(\.calendarBodyColor, "#FFFFFF"))
            ColorHexPicker(title: "Calendar text", symbol: "textformat.123", tint: .brown, hex: s(\.calendarTextColor, "#000000"))
            Toggle(isOn: weekdayBar(\.startOnMonday, false)) {
                RowLabel("Start week on Monday", symbol: "calendar.day.timeline.left", tint: .purple)
            }
            Toggle(isOn: weekdayBar(\.show, true)) {
                RowLabel("Show weekday bar", symbol: "w.square.fill", tint: .cyan)
            }
            ColorHexPicker(title: "Active weekday", symbol: "circle.fill", tint: .green, hex: weekdayBar(\.activeColor, "#FFFFFF"))
            ColorHexPicker(title: "Inactive weekday", symbol: "circle", tint: .gray, hex: weekdayBar(\.inactiveColor, "#666666"))
        } header: {
            Text("Time & Date")
        }
    }

    private var timePreviewText: String {
        DeviceKnownValues.timePreview(
            hour24: b(\.time24h).wrappedValue,
            leadingZero: b(\.timeLeadingZero).wrappedValue,
            showSeconds: b(\.timeShowSeconds).wrappedValue,
            showAmPm: b(\.timeShowAmPm).wrappedValue
        )
    }

    private var datePreviewText: String {
        DeviceKnownValues.datePreview(
            order: enumBinding(\.dateOrder, DateOrder.dayMonthYear).wrappedValue,
            separator: enumBinding(\.dateSeparator, DateSeparator.dot).wrappedValue,
            yearMode: enumBinding(\.dateYearMode, DateYearMode.twoDigit).wrappedValue
        )
    }

    @ViewBuilder private var buttonsSection: some View {
        Section {
            if let secs = buttons?.secondsSince {
                LabeledContent {
                    Text(agoText(secs)).foregroundStyle(secs < 3600 ? .green : .secondary)
                } label: {
                    RowLabel("Last button press", symbol: "button.horizontal.top.press", tint: .green)
                }
            } else {
                Text("No button presses seen yet").font(.caption).foregroundStyle(.secondary)
            }
            LabeledContent {
                Text(buttons?.configured == true ? "Configured" : "Not configured")
                    .foregroundStyle(buttons?.configured == true ? .green : .secondary)
            } label: {
                RowLabel("Clock callback", symbol: "link", tint: .blue)
            }
            Button {
                Task { await setButtonsEnabled(!(buttons?.configured ?? false)) }
            } label: {
                RowLabel(buttons?.configured == true ? "Disable clock buttons" : "Enable clock buttons",
                         symbol: "button.programmable", tint: buttons?.configured == true ? .red : .green)
            }
        } header: {
            Text("Buttons")
        } footer: {
            Text("Drives Pomodoro from the clock's physical buttons. Enabling points the clock at this server directly — no manual configuration needed.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func setButtonsEnabled(_ enabled: Bool) async {
        save = .saving
        do {
            buttons = try await env.device.updateButtons(enabled: enabled)
            save = .saved
        } catch let e as APIError where e.isUnauthorized {
            save = .error("Unauthorized — check the token in Connection.")
        } catch {
            save = .error("Save failed: \(error.localizedDescription)")
        }
    }

    private func agoText(_ s: Int) -> String {
        if s < 60 { return "\(s)s ago" }
        if s < 3600 { return "\(s / 60)m ago" }
        if s < 86400 { return "\(s / 3600)h ago" }
        return "\(s / 86400)d ago"
    }

    @ViewBuilder private var statusCaption: some View {
        switch save {
        case .idle:   EmptyView()
        case .saving: Text("Saving…").font(.caption).foregroundStyle(.secondary)
        case .saved:  Label("Saved", systemImage: "checkmark.circle").font(.caption).foregroundStyle(.secondary)
        case .error(let m): Label(m, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.red)
        }
    }

    // MARK: Row builders

    /// Tahoe-style slider row: badge + title leading, slider with a trailing
    /// value readout in the content column.
    private func sliderRow(_ title: String, symbol: String, tint: Color,
                           value: Binding<Int>, range: ClosedRange<Double>, step: Double = 1,
                           display: @escaping (Int) -> String) -> some View {
        LabeledContent {
            HStack(spacing: 8) {
                Slider(
                    value: Binding(get: { Double(value.wrappedValue) }, set: { value.wrappedValue = Int($0) }),
                    in: range, step: step
                )
                .frame(maxWidth: 240)
                Text(display(value.wrappedValue))
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .frame(minWidth: 56, alignment: .trailing)
            }
        } label: {
            RowLabel(title, symbol: symbol, tint: tint)
        }
    }

    // MARK: Binding helpers (optional settings field <-> non-optional control)

    private func b(_ kp: WritableKeyPath<DeviceSettings, Bool?>, _ def: Bool = false) -> Binding<Bool> {
        Binding(get: { settings[keyPath: kp] ?? def }, set: { settings[keyPath: kp] = $0 })
    }
    private func i(_ kp: WritableKeyPath<DeviceSettings, Int?>, _ def: Int) -> Binding<Int> {
        Binding(get: { settings[keyPath: kp] ?? def }, set: { settings[keyPath: kp] = $0 })
    }
    private func s(_ kp: WritableKeyPath<DeviceSettings, String?>, _ def: String) -> Binding<String> {
        Binding(get: { settings[keyPath: kp] ?? def }, set: { settings[keyPath: kp] = $0 })
    }

    /// Binds a raw-String device setting field to a typed enum, defaulting when
    /// unset or unrecognised.
    private func enumBinding<E: RawRepresentable & Sendable>(
        _ kp: WritableKeyPath<DeviceSettings, String?>, _ def: E
    ) -> Binding<E> where E.RawValue == String {
        Binding(
            get: { E(rawValue: settings[keyPath: kp] ?? def.rawValue) ?? def },
            set: { settings[keyPath: kp] = $0.rawValue }
        )
    }

    /// Reads/writes one Int field of the nested `scroll` object, creating it on
    /// first write.
    private func scroll(_ kp: WritableKeyPath<ScrollSettings, Int?>, _ def: Int) -> Binding<Int> {
        Binding(
            get: { settings.scroll?[keyPath: kp] ?? def },
            set: { var scr = settings.scroll ?? ScrollSettings(); scr[keyPath: kp] = $0; settings.scroll = scr }
        )
    }

    /// Reads/writes one Bool field of the nested `weekdayBar` object, creating
    /// it on first write.
    private func weekdayBar(_ kp: WritableKeyPath<WeekdayBar, Bool?>, _ def: Bool) -> Binding<Bool> {
        Binding(
            get: { settings.weekdayBar?[keyPath: kp] ?? def },
            set: { var w = settings.weekdayBar ?? WeekdayBar(); w[keyPath: kp] = $0; settings.weekdayBar = w }
        )
    }
    /// Reads/writes one String field of the nested `weekdayBar` object,
    /// creating it on first write.
    private func weekdayBar(_ kp: WritableKeyPath<WeekdayBar, String?>, _ def: String) -> Binding<String> {
        Binding(
            get: { settings.weekdayBar?[keyPath: kp] ?? def },
            set: { var w = settings.weekdayBar ?? WeekdayBar(); w[keyPath: kp] = $0; settings.weekdayBar = w }
        )
    }

    /// appDurationMs is milliseconds on the wire; the UI shows seconds, matching
    /// the AWTRIX3-era control (was ATIME, already in seconds).
    private var appDurationSecondsBinding: Binding<Int> {
        Binding(
            get: { (settings.appDurationMs ?? 7000) / 1000 },
            set: { settings.appDurationMs = $0 * 1000 }
        )
    }

    // MARK: Load / save

    /// Keeps the battery/firmware rows live while the tab is visible (the .task
    /// modifier cancels this loop on disappear). The matrix mirror polls itself
    /// inside LiveMatrixMirror.
    private func pollStats() async {
        while !Task.isCancelled {
            if let st = try? await env.device.stats() { stats = st }
            if Task.isCancelled { return }
            try? await Task.sleep(for: .seconds(5))
        }
    }

    private func load() async {
        save = .idle
        config = try? await env.device.config()
        buttons = try? await env.device.buttons()
        sensorsOnDevice = try? await env.device.sensors()
        sensorsEdit = sensorsOnDevice ?? SensorCalibration()
        capabilities = try? await env.device.capabilities()
        apps = (try? await env.device.apps()) ?? []
        do {
            async let s = env.device.settings()
            async let st = try? env.device.stats()
            async let d = try? env.device.display()
            settings = try await s
            stats = await st
            deviceDisplay = await d ?? DeviceDisplay()
            lastDeviceDisplay = deviceDisplay
            lastApplied = settings
            loadError = nil
            clockUnreachable = false
        } catch {
            // Only a 502 (server up, its proxy to the clock down) is fixable by
            // finding the clock. A server we can't reach at all would fail the
            // config PUT too, so it must not offer discovery as the remedy.
            let failure = DeviceFailure.classify(error)
            clockUnreachable = failure == .clockUnreachable
            switch failure {
            case .unauthorized:
                loadError = "Unauthorized — check the token in Connection."
            case .serverUnreachable:
                loadError = "Server unreachable — check the server URL in Connection."
            case .clockUnreachable:
                loadError = "Clock unreachable — use “Find clock” to locate it on your network."
                clockExpanded = true   // put the one-click fix on screen
            case .timedOut:
                // Nobody answered inside our budget, and that budget already
                // out-waits the server's own 8s clock budget. Blame neither end;
                // open the section so "Find clock" is at hand either way.
                loadError = "No response in time — the server may be busy, or the clock is hanging."
                clockExpanded = true
            case .other:
                loadError = "Couldn't load from the clock: \(error.localizedDescription)"
            }
        }
    }

    private func scheduleSave() {
        guard loaded, loadError == nil, settings != lastApplied else { return }
        save = .saving
        let snapshot = settings
        writer.schedule {
            do {
                try await env.device.update(snapshot)
                await MainActor.run { lastApplied = snapshot; save = .saved }
            } catch let e as APIError where e.isUnauthorized {
                await MainActor.run { save = .error("Unauthorized — check the token in Connection.") }
            } catch {
                await MainActor.run { save = .error("Save failed: \(error.localizedDescription)") }
            }
        }
    }

    private func scheduleDisplaySave() {
        guard loaded, loadError == nil, deviceDisplay != lastDeviceDisplay else { return }
        save = .saving
        let snapshot = deviceDisplay
        displayWriter.schedule {
            do {
                try await env.device.updateDisplay(snapshot)
                await MainActor.run { lastDeviceDisplay = snapshot; save = .saved }
            } catch let e as APIError where e.isUnauthorized {
                await MainActor.run { save = .error("Unauthorized — check the token in Connection.") }
            } catch {
                await MainActor.run { save = .error("Save failed: \(error.localizedDescription)") }
            }
        }
    }

    /// Runs a one-off device action and reflects the outcome in the status caption.
    private func perform(_ action: @escaping () async throws -> Void) async {
        do {
            try await action()
            save = .saved
        } catch {
            save = .error("Action failed: \(error.localizedDescription)")
        }
    }
}
