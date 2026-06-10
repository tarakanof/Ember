import SwiftUI
import EmberKit

/// AWTRIX clock settings, proxied through the Ember server (/v1/device/*). Mirrors
/// the official AWTRIX3 app's surface: General, Native Apps, Time & Date, Actions.
/// Auto-applies on change (debounced), matching the other settings tabs.
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

    private let overlays = ["clear", "snow", "rain", "drizzle", "storm", "thunder", "frost"]

    var body: some View {
        Form {
            if let loadError {
                Section {
                    Label(loadError, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption).foregroundStyle(.red)
                }
            }

            Section {
                LabeledContent("Battery") { Text(stats?.bat.map { "\($0)%" } ?? "—") }
                LabeledContent("Firmware") { Text(stats?.version ?? "—") }
            } header: {
                Text("Clock")
            }

            generalSection
            nativeAppsSection
            timeDateSection
            actionsSection
        }
        .formStyle(.grouped)
        .navigationTitle("Device")
        .disabled(loadError != nil && !loaded)
        .toolbar {
            ToolbarItem { Button("Reload from clock") { Task { await load() } } }
        }
        .task {
            if !loaded {
                await load()
                loaded = true
            }
        }
        .onChange(of: settings) { _, _ in scheduleSave() }
        .confirmationDialog("Reboot the clock?", isPresented: $confirmReboot, titleVisibility: .visible) {
            Button("Reboot", role: .destructive) { Task { await perform { try await env.device.reboot() } } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The clock will restart and be briefly unavailable.")
        }
    }

    // MARK: Sections

    @ViewBuilder private var generalSection: some View {
        Section {
            Toggle("Uppercase letters", isOn: b(\.uppercase))
            Toggle("Block buttons", isOn: b(\.blockn))
            Toggle("Auto brightness", isOn: b(\.abri))
            VStack(alignment: .leading) {
                LabeledContent("Brightness", value: "\(i(\.bri, 80).wrappedValue)")
                Slider(value: doubleBinding(\.bri, 80), in: 0...255, step: 1)
                    .disabled(b(\.abri).wrappedValue)
            }
            Stepper("Volume: \(i(\.vol, 25).wrappedValue)", value: i(\.vol, 25), in: 0...30)
            Stepper("App time: \(i(\.atime, 7).wrappedValue)s", value: i(\.atime, 7), in: 1...60)
            ColorHexPicker(title: "Text color", hex: s(\.tcol, "#FFFFFF"))
            Toggle("Auto transition", isOn: b(\.atrans))
            Stepper("Transition effect: \(i(\.teff, 1).wrappedValue)", value: i(\.teff, 1), in: 0...10)
            Stepper("Transition speed: \(i(\.tspeed, 400).wrappedValue)ms", value: i(\.tspeed, 400), in: 0...2000, step: 50)
            Stepper("Scroll speed: \(i(\.sspeed, 100).wrappedValue)%", value: i(\.sspeed, 100), in: 10...500, step: 10)
            Picker("Overlay", selection: s(\.overlay, "clear")) {
                ForEach(overlays, id: \.self) { Text($0.capitalized).tag($0) }
            }
        } header: {
            Text("General")
        } footer: {
            Text("“Block buttons” may be briefly overridden while a Pomodoro is running.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var nativeAppsSection: some View {
        Section {
            Toggle("Time", isOn: b(\.tim))
            Toggle("Date", isOn: b(\.dat))
            Toggle("Temperature", isOn: b(\.temp))
            Toggle("Humidity", isOn: b(\.hum))
            Toggle("Battery", isOn: b(\.bat))
        } header: {
            Text("Native Apps")
        } footer: {
            Text("Changes to the built-in apps apply after a clock reboot.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var timeDateSection: some View {
        Section {
            TextField("Time format", text: s(\.tformat, "%H %M"))
            TextField("Date format", text: s(\.dformat, "%d.%m.%y"))
            Toggle("Start week on Monday", isOn: b(\.som))
            Stepper("Time mode: \(i(\.tmode, 1).wrappedValue)", value: i(\.tmode, 1), in: 0...6)
            ColorHexPicker(title: "Calendar header", hex: s(\.chcol, "#FF0000"))
            ColorHexPicker(title: "Calendar body", hex: s(\.cbcol, "#FFFFFF"))
            ColorHexPicker(title: "Calendar text", hex: s(\.ctcol, "#000000"))
            Toggle("Show weekday", isOn: b(\.wd))
            ColorHexPicker(title: "Active weekday", hex: s(\.wdca, "#FFFFFF"))
            ColorHexPicker(title: "Inactive weekday", hex: s(\.wdci, "#666666"))
        } header: {
            Text("Time & Date")
        }
    }

    @ViewBuilder private var actionsSection: some View {
        Section {
            Button("Dismiss notification") { Task { await perform { try await env.device.dismiss() } } }
            Button("Reboot clock", role: .destructive) { confirmReboot = true }
        } header: {
            Text("Actions")
        } footer: {
            statusCaption
        }
    }

    @ViewBuilder private var statusCaption: some View {
        switch save {
        case .idle:   EmptyView()
        case .saving: Text("Saving…").font(.caption).foregroundStyle(.secondary)
        case .saved:  Label("Saved", systemImage: "checkmark.circle").font(.caption).foregroundStyle(.secondary)
        case .error(let m): Label(m, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.red)
        }
    }

    // MARK: Binding helpers (optional settings field <-> non-optional control)

    private func b(_ kp: WritableKeyPath<DeviceSettings, Bool?>, _ def: Bool = false) -> Binding<Bool> {
        Binding(get: { settings[keyPath: kp] ?? def }, set: { settings[keyPath: kp] = $0 })
    }
    private func i(_ kp: WritableKeyPath<DeviceSettings, Int?>, _ def: Int) -> Binding<Int> {
        Binding(get: { settings[keyPath: kp] ?? def }, set: { settings[keyPath: kp] = $0 })
    }
    private func doubleBinding(_ kp: WritableKeyPath<DeviceSettings, Int?>, _ def: Int) -> Binding<Double> {
        Binding(get: { Double(settings[keyPath: kp] ?? def) }, set: { settings[keyPath: kp] = Int($0) })
    }
    private func s(_ kp: WritableKeyPath<DeviceSettings, String?>, _ def: String) -> Binding<String> {
        Binding(get: { settings[keyPath: kp] ?? def }, set: { settings[keyPath: kp] = $0 })
    }

    // MARK: Load / save

    private func load() async {
        save = .idle
        do {
            async let s = env.device.settings()
            async let st = try? env.device.stats()
            settings = try await s
            stats = await st
            lastApplied = settings
            loadError = nil
        } catch let e as APIError where e.isUnauthorized {
            loadError = "Unauthorized — check the token in Connection."
        } catch {
            loadError = "Clock unreachable — check Connection and discovery."
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
