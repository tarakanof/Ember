import SwiftUI
import AppKit
import EmberKit

/// Where the clock is and how it's doing: address, firmware (with an update
/// badge), battery, climate, Wi-Fi, uptime. Health comes from
/// `/v1/clock/health` while the pane is open; an older server falls back to
/// the raw device stats. Restart sits at the bottom, behind a confirmation.
struct ClockStatusSection: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(DeviceSettingsModel.self) private var device
    @State private var confirmRestart = false
    @State private var showDiscover = false

    private var health: ClockHealth? { env.live.clockHealth.value }
    private var probe: ClockHealth.Device? { health?.device }
    private var celsius: Bool { device.settings.draft.useCelsius ?? true }

    var body: some View {
        Section {
            LabeledContent("Address") {
                Text(verbatim: device.config?.baseURL.nonEmpty ?? "—")
                    .textSelection(.enabled)
            }
            LabeledContent("Firmware") {
                HStack(spacing: 8) {
                    Text(verbatim: probe?.firmware ?? device.stats?.version ?? "—")
                    if health?.updateAvailable == true, let latest = health?.latestFirmware {
                        Button {
                            if let url = device.config?.webURL { NSWorkspace.shared.open(url) }
                        } label: {
                            Label("Update Available: \(latest)", systemImage: "arrow.down.circle.fill")
                        }
                        .buttonStyle(.link)
                        .help("Opens the clock's web page, which installs firmware updates.")
                    }
                }
            }
            if let battery = probe?.batteryPercent ?? device.stats?.batteryPercent {
                LabeledContent("Battery") {
                    HStack(spacing: 6) {
                        if probe?.lowBattery == true {
                            Label("Low", systemImage: "battery.25percent").foregroundStyle(.orange)
                        }
                        Text(verbatim: Percent.text(battery))
                    }
                }
            }
            if let t = probe?.temperatureC ?? device.stats?.temperature {
                LabeledContent("Temperature") { Text(temperature(t)) }
            }
            if let h = probe?.humidityPercent ?? device.stats?.humidity {
                LabeledContent("Humidity") { Text(Percent.text(h)) }
            }
            if let rssi = probe?.wifiRssiDbm {
                LabeledContent("Wi-Fi") { Text("\(Text(wifiQuality(rssi))) · \(rssi) dBm") }
            }
            if let up = probe?.uptimeSec ?? device.stats?.uptimeSeconds {
                LabeledContent("Uptime") { Text(DurationText.uptime(up)) }
            }
            if let ratio = health?.publish.successRatio24h {
                LabeledContent("Updates delivered") {
                    Text("\(Percent.text(ratio: ratio)) in 24 h")
                }
                .help("How many of the server's pushes reached the clock in the last 24 hours.")
            }
            HStack {
                Button("Discover Clocks…") { showDiscover = true }
                Button("Open Web UI") {
                    if let url = device.config?.webURL { NSWorkspace.shared.open(url) }
                }
                .disabled(device.config?.webURL == nil)
                Spacer()
                Button("Restart Clock…", role: .destructive) { confirmRestart = true }
                    .disabled(!device.isLoaded || env.actions.running.contains(.clock(.reboot)))
            }
        } header: {
            Text("Status")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Ember finds the clock on its own. Pick one with Discover Clocks to pin it on the server.")
                if let failure = env.actions.lastError, failure.action == .clock(.reboot) {
                    Label { Text("Couldn't restart the clock: \(Text(failure.error.message))") } icon: {
                        Image(systemName: "exclamationmark.triangle.fill")
                    }
                    .foregroundStyle(.red)
                }
            }
        }
        .task { await env.live.track(.clockHealth) }
        .confirmationDialog("Restart the clock?", isPresented: $confirmRestart, titleVisibility: .visible) {
            Button("Restart", role: .destructive) { Task { await env.actions.run(.clock(.reboot)) } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The clock is unavailable for a few seconds while it restarts.")
        }
        .sheet(isPresented: $showDiscover) { DiscoverClocksSheet() }
    }

    private func temperature(_ celsiusValue: Double) -> String {
        let m = Measurement(value: celsiusValue, unit: UnitTemperature.celsius)
        let shown = celsius ? m : m.converted(to: .fahrenheit)
        return shown.formatted(.measurement(width: .abbreviated, usage: .asProvided,
                                            numberFormatStyle: .number.precision(.fractionLength(1))))
    }

    private func wifiQuality(_ rssi: Int) -> LocalizedStringKey {
        if rssi >= -60 { return "Strong" }
        if rssi >= -70 { return "Good" }
        if rssi >= -80 { return "Weak" }
        return "Very weak"
    }
}

/// Lists the clocks the server can see; Use pins one on the server.
struct DiscoverClocksSheet: View {
    @Environment(DeviceSettingsModel.self) private var device
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Clocks on Your Network").font(.headline)
            Form {
                if device.running.contains(.discover) {
                    LabeledContent {
                        ProgressView().controlSize(.small)
                    } label: {
                        Text("Searching…").foregroundStyle(.secondary)
                    }
                } else if let found = device.discovered, !found.isEmpty {
                    ForEach(found) { c in
                        LabeledContent {
                            if c.baseURL == device.config?.baseURL {
                                Image(systemName: "checkmark").foregroundStyle(.tint)
                                    .accessibilityLabel("In use")
                            } else {
                                Button("Use") {
                                    Task {
                                        await device.use(c)
                                        if device.actionErrors[.useClock] == nil { dismiss() }
                                    }
                                }
                                .disabled(device.running.contains(.useClock))
                            }
                        } label: {
                            Text(verbatim: c.baseURL)
                            Text("\(c.host) · firmware \(c.version)")
                        }
                    }
                } else {
                    Text("No clocks found. The server looks for them with mDNS, which needs host networking.")
                        .foregroundStyle(.secondary)
                }
                if let e = device.actionErrors[.discover] ?? device.actionErrors[.useClock] {
                    Label { Text(e.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            HStack {
                Button("Search Again") { Task { await device.discover() } }
                    .disabled(device.running.contains(.discover))
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 460, height: 340)
        .onExitCommand { dismiss() }
        .task { await device.discover() }
    }
}

extension String {
    /// nil for an empty or blank string.
    var nonEmpty: String? {
        trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : self
    }
}
