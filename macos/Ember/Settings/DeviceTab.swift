import SwiftUI
import AppKit
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
    @State private var config: DeviceConfig?
    @State private var discovered: [DiscoveredClock] = []
    @State private var discovering = false
    @State private var buttons: ButtonStatus?
    @State private var screen: [Int]?

    private let overlays = ["clear", "snow", "rain", "drizzle", "storm", "thunder", "frost"]

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
            nativeAppsSection
            timeDateSection
            actionsSection
            buttonsSection
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
        .task { await pollScreen() }
        .onChange(of: settings) { _, _ in scheduleSave() }
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
            MatrixScreenView(pixels: screen ?? Array(repeating: 0, count: 256))
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity)
                .background(.black)
                .listRowInsets(EdgeInsets())
        }
    }

    @ViewBuilder private var clockSection: some View {
        Section {
            LabeledContent { Text(config?.baseURL ?? "—").foregroundStyle(.secondary) } label: {
                RowLabel("Address", symbol: "globe", tint: .blue)
            }
            LabeledContent { Text(config?.source ?? "—").foregroundStyle(.secondary) } label: {
                RowLabel("Source", symbol: "point.3.connected.trianglepath.dotted", tint: .indigo)
            }
            LabeledContent { Text(stats?.bat.map { "\($0)%" } ?? "—") } label: {
                RowLabel("Battery", symbol: "battery.75percent", tint: .green)
            }
            LabeledContent { Text(stats?.version ?? "—") } label: {
                RowLabel("Firmware", symbol: "cpu", tint: .gray)
            }
            Button {
                Task { await discoverClocks() }
            } label: {
                RowLabel(discovering ? "Scanning…" : "Discover clocks",
                         symbol: "antenna.radiowaves.left.and.right", tint: .teal)
            }
            .disabled(discovering)
            ForEach(discovered) { c in
                Button {
                    Task { await pickClock(c) }
                } label: {
                    HStack {
                        Image(systemName: c.baseURL == config?.baseURL ? "checkmark.circle.fill" : "circle")
                            .foregroundStyle(c.baseURL == config?.baseURL ? .green : .secondary)
                        VStack(alignment: .leading) {
                            Text(c.baseURL)
                            Text("\(c.host) · fw \(c.version)").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
                .buttonStyle(.plain)
            }
        } header: {
            Text("Clock")
        } footer: {
            Text("Ember auto-discovers the clock via mDNS. Use “Discover clocks” to pick a specific one; your choice is saved on the server and overrides auto-discovery.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var generalSection: some View {
        Section {
            Toggle(isOn: b(\.uppercase)) {
                RowLabel("Uppercase letters", symbol: "textformat", tint: .blue)
            }
            Toggle(isOn: b(\.blockn)) {
                RowLabel("Block buttons", symbol: "hand.raised.fill", tint: .orange)
            }
            Toggle(isOn: b(\.abri)) {
                RowLabel("Auto brightness", symbol: "sun.max.fill", tint: .yellow)
            }
            sliderRow("Brightness", symbol: "sun.min.fill", tint: .yellow,
                      value: i(\.bri, 80), range: 0...255) { "\($0)" }
                .disabled(b(\.abri).wrappedValue)
            sliderRow("Volume", symbol: "speaker.wave.2.fill", tint: .pink,
                      value: i(\.vol, 25), range: 0...30) { "\($0)" }
            sliderRow("App time", symbol: "timer", tint: .orange,
                      value: i(\.atime, 7), range: 1...60) { "\($0)s" }
            ColorHexPicker(title: "Text color", symbol: "paintpalette.fill", tint: .teal, hex: s(\.tcol, "#FFFFFF"))
            Toggle(isOn: b(\.atrans)) {
                RowLabel("Auto transition", symbol: "arrow.left.arrow.right", tint: .green)
            }
            Picker(selection: i(\.teff, 1)) {
                ForEach(TransitionEffect.allCases) { e in
                    Text(e.displayName).tag(e.rawValue)
                }
                if TransitionEffect(rawValue: i(\.teff, 1).wrappedValue) == nil {
                    Text("Effect \(i(\.teff, 1).wrappedValue)").tag(i(\.teff, 1).wrappedValue)
                }
            } label: {
                RowLabel("Transition effect", symbol: "sparkles", tint: .purple)
            }
            sliderRow("Transition speed", symbol: "gauge.with.needle", tint: .cyan,
                      value: i(\.tspeed, 400), range: 0...2000, step: 50) { "\($0)ms" }
            sliderRow("Scroll speed", symbol: "forward.fill", tint: .mint,
                      value: i(\.sspeed, 100), range: 10...500, step: 10) { "\($0)%" }
            Picker(selection: s(\.overlay, "clear")) {
                ForEach(overlays, id: \.self) { Text($0.capitalized).tag($0) }
            } label: {
                RowLabel("Overlay", symbol: "cloud.snow.fill", tint: .blue)
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
            Toggle(isOn: b(\.tim)) { RowLabel("Time", symbol: "clock.fill", tint: .blue) }
            Toggle(isOn: b(\.dat)) { RowLabel("Date", symbol: "calendar", tint: .red) }
            Toggle(isOn: b(\.temp)) { RowLabel("Temperature", symbol: "thermometer.medium", tint: .orange) }
            Toggle(isOn: b(\.hum)) { RowLabel("Humidity", symbol: "humidity.fill", tint: .teal) }
            Toggle(isOn: b(\.bat)) { RowLabel("Battery", symbol: "battery.50percent", tint: .green) }
        } header: {
            Text("Native Apps")
        } footer: {
            Text("Changes to the built-in apps apply after a clock reboot.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var timeDateSection: some View {
        Section {
            formatPicker("Time format", symbol: "clock.badge", tint: .blue,
                         selection: s(\.tformat, "%H %M"), options: DeviceFormats.timeFormats)
            formatPicker("Date format", symbol: "calendar.badge.clock", tint: .red,
                         selection: s(\.dformat, "%d.%m.%y"), options: DeviceFormats.dateFormats)
            Toggle(isOn: b(\.som)) {
                RowLabel("Start week on Monday", symbol: "calendar.day.timeline.left", tint: .purple)
            }
            Picker(selection: i(\.tmode, 1)) {
                ForEach(0...6, id: \.self) { m in
                    Text("Style \(m)").tag(m)
                }
                if !(0...6).contains(i(\.tmode, 1).wrappedValue) {
                    Text("Style \(i(\.tmode, 1).wrappedValue)").tag(i(\.tmode, 1).wrappedValue)
                }
            } label: {
                RowLabel("Time style", symbol: "squares.below.rectangle", tint: .indigo)
            }
            ColorHexPicker(title: "Calendar header", symbol: "calendar.circle.fill", tint: .red, hex: s(\.chcol, "#FF0000"))
            ColorHexPicker(title: "Calendar body", symbol: "square.fill", tint: .gray, hex: s(\.cbcol, "#FFFFFF"))
            ColorHexPicker(title: "Calendar text", symbol: "textformat.123", tint: .brown, hex: s(\.ctcol, "#000000"))
            Toggle(isOn: b(\.wd)) {
                RowLabel("Show weekday", symbol: "w.square.fill", tint: .cyan)
            }
            ColorHexPicker(title: "Active weekday", symbol: "circle.fill", tint: .green, hex: s(\.wdca, "#FFFFFF"))
            ColorHexPicker(title: "Inactive weekday", symbol: "circle", tint: .gray, hex: s(\.wdci, "#666666"))
        } header: {
            Text("Time & Date")
        }
    }

    @ViewBuilder private var actionsSection: some View {
        Section {
            Button { Task { await perform { try await env.device.dismiss() } } } label: {
                RowLabel("Dismiss notification", symbol: "bell.slash.fill", tint: .gray)
            }
            Button(role: .destructive) { confirmReboot = true } label: {
                RowLabel("Reboot clock", symbol: "power", tint: .red)
            }
        } header: {
            Text("Actions")
        } footer: {
            statusCaption
        }
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
            if let cb = expectedCallback {
                LabeledContent {
                    Text(cb).font(.callout.monospaced()).foregroundStyle(.secondary).textSelection(.enabled)
                } label: {
                    RowLabel("Expected callback", symbol: "link", tint: .blue)
                }
                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(cb, forType: .string)
                } label: {
                    RowLabel("Copy callback URL", symbol: "doc.on.doc", tint: .gray)
                }
            }
            if let url = fileManagerURL {
                Button {
                    NSWorkspace.shared.open(url)
                } label: {
                    RowLabel("Open clock file manager", symbol: "folder.fill", tint: .blue)
                }
            }
        } header: {
            Text("Buttons")
        } footer: {
            Text("Drive Pomodoro from the clock's physical buttons. If they don't respond, set button_callback to the Expected callback URL above in the clock's file manager (dev.json), then reboot the clock.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// The clock's button_callback should point at the Ember server. Prefer the
    /// server-reported value (the clock's-eye address); fall back to deriving it
    /// from the configured server URL so it shows even against an older server
    /// that lacks /v1/device/buttons.
    private var expectedCallback: String? {
        if let s = buttons?.expectedCallback, !s.isEmpty { return s }
        let su = env.currentEnv().get(SettingsKeys.serverURL).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !su.isEmpty else { return nil }
        return trimSlash(su) + "/hooks/awtrix/button"
    }

    /// The AWTRIX file editor lives at <clock>/edit (where dev.json is edited).
    private var fileManagerURL: URL? {
        guard let base = config?.baseURL, !base.isEmpty else { return nil }
        return URL(string: trimSlash(base) + "/edit")
    }

    private func trimSlash(_ s: String) -> String {
        s.hasSuffix("/") ? String(s.dropLast()) : s
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

    /// Picker over the firmware's documented format strings, each shown as a
    /// live example ("14:05 · %H:%M"). A custom value already on the device is
    /// kept as an extra option instead of blanking the picker.
    private func formatPicker(_ title: String, symbol: String, tint: Color,
                              selection: Binding<String>, options: [String]) -> some View {
        let current = selection.wrappedValue
        let all = options.contains(current) ? options : options + [current]
        return Picker(selection: selection) {
            ForEach(all, id: \.self) { f in
                Text("\(DeviceFormats.example(f))  ·  \(f)").monospacedDigit().tag(f)
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

    // MARK: Load / save

    /// Mirrors the clock's display while the tab is visible (the .task modifier
    /// cancels this loop on disappear). Prefers the server proxy; against an
    /// older server without /v1/device/screen it falls back to reading the
    /// clock directly, re-probing the proxy every 30 ticks. Backs off to 3s
    /// while nothing is reachable.
    private func pollScreen() async {
        var preferProxy = true
        var tick = 0
        while !Task.isCancelled {
            var s: [Int]?
            if preferProxy || tick % 30 == 0 {
                s = try? await env.device.screen()
                preferProxy = s != nil
            }
            if s == nil, let base = config?.baseURL, !base.isEmpty {
                s = try? await DeviceService.directScreen(clockBaseURL: base)
            }
            // Refresh stats every 5th tick so the battery/firmware rows stay live.
            if tick % 5 == 0, let st = try? await env.device.stats() {
                stats = st
            }
            if Task.isCancelled { return }
            screen = s
            tick += 1
            try? await Task.sleep(for: .seconds(s == nil ? 3 : 1))
        }
    }

    private func load() async {
        save = .idle
        config = try? await env.device.config()
        buttons = try? await env.device.buttons()
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

    /// Browses the LAN (server-side) for AWTRIX clocks and lists them to pick from.
    private func discoverClocks() async {
        discovering = true
        defer { discovering = false }
        discovered = (try? await env.device.discover())?.candidates ?? []
    }

    /// Persists the chosen clock on the server, then reloads from it.
    private func pickClock(_ c: DiscoveredClock) async {
        do {
            try await env.device.setConfig(baseURL: c.baseURL)
            discovered = []
            await load()
        } catch {
            save = .error("Couldn't switch clock: \(error.localizedDescription)")
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
