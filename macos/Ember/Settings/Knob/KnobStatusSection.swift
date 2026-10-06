import SwiftUI
import EmberKit

struct KnobStatusSection: View {
    @Environment(AppEnvironment.self) private var env
    let setUp: () -> Void
    @State private var confirmForget = false

    var body: some View {
        let model = env.knob
        if let knob = model.knob {
            let checkin = knob.lastCheckin
            Section {
                LabeledContent("Connection") { online(knob) }
                LabeledContent("Address") {
                    Text(verbatim: checkin?.ip.nonEmpty ?? "—").textSelection(.enabled)
                }
                LabeledContent("Firmware") { Text(verbatim: checkin?.fw.nonEmpty ?? "—") }
                if let c = checkin, c.rssi != 0 || c.wifi?.hasLinkDetails == true {
                    wifiRow(c.rssi, c.wifi)
                }
                if let wifi = checkin?.wifi {
                    LabeledContent("Reconnects") { reconnects(wifi) }
                        .help("Times the knob lost Wi-Fi since it started. Each one starts a reconnect.")
                }
                if let up = checkin?.uptimeS {
                    LabeledContent("Uptime") { Text(DurationText.uptime(up)) }
                }
                LabeledContent("Last check-in") {
                    if let seen = checkin?.seenAt {
                        Text(seen, format: .relative(presentation: .named))
                    } else {
                        Text("Never").foregroundStyle(.secondary)
                    }
                }
                if let c = checkin, c.heapInternalFree > 0 {
                    LabeledContent("Free memory") {
                        Text("\(bytes(c.heapInternalFree)) · largest block \(bytes(c.heapInternalLargest))",
                             comment: "Settings › Knob: the knob's free internal RAM, then the largest free block (\"46 KB · largest block 31 KB\").")
                    }
                    .help("Internal RAM on the knob. Below about 24 KB in one block, pages may fail to draw.")
                }
                if let diag = checkin?.diag {
                    diagRows(diag)
                }
                if let mhz = checkin?.linkMHz, mhz > 0 {
                    LabeledContent("Display link") {
                        if checkin?.linkFallback == true {
                            Text("\(mhz) MHz (fell back after a check failed)",
                                 comment: "Settings › Knob: the knob's panel link clock after it fell back from 80 MHz (\"40 MHz (fell back after a check failed)\").")
                        } else {
                            Text(verbatim: "\(mhz) MHz")
                        }
                    }
                }
                LabeledContent("Settings") { applied(knob) }
                LabeledContent("Hardware stats") {
                    Button("Show Hardware") { showKnobHardware(knob.id) }
                }
                KnobUSBRow()
                HStack {
                    Button("Set Up Knob…", action: setUp)
                    Spacer()
                    Button("Forget Knob…", role: .destructive) { confirmForget = true }
                        .disabled(model.running.contains(.forget))
                }
            } header: {
                Text("Status")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("The knob checks in with Ember every minute and picks up setting changes within a few seconds.")
                    if let e = model.actionErrors[.forget] {
                        Label { Text("Couldn't forget the knob: \(Text(e.message))",
                                     comment: "Settings › Knob error under Status; the argument is a short reason (\"Server unreachable\").") }
                            icon: { Image(systemName: "exclamationmark.triangle.fill") }
                            .foregroundStyle(.red)
                    }
                }
            }
            .confirmationDialog(Text("Forget “\(knob.name)”?",
                                     comment: "Settings › Knob confirm title; the argument is the knob's name (\"Desk knob\")."),
                                isPresented: $confirmForget, titleVisibility: .visible) {
                Button("Forget Knob", role: .destructive) { Task { await model.forget() } }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("Ember revokes the knob's token at once. The knob keeps showing the mood and weather but can't start a Pomodoro until you set it up again over USB.")
            }
        }
    }

    @ViewBuilder private func online(_ knob: KnobDevice) -> some View {
        if knob.lastCheckin == nil {
            Label("Waiting for its first check-in", systemImage: "clock")
                .foregroundStyle(.secondary)
        } else if knob.isOnline(now: .now) {
            Label { Text("Online") } icon: { Image(systemName: "circle.fill").foregroundStyle(.green) }
        } else {
            Label { Text("Offline") } icon: { Image(systemName: "circle.fill").foregroundStyle(.secondary) }
        }
    }

    @ViewBuilder private func applied(_ knob: KnobDevice) -> some View {
        let running = knob.lastCheckin?.appliedVersion ?? 0
        if knob.configApplied {
            Text("Applied (version \(knob.configVersion))",
                 comment: "Settings › Knob: the knob runs the latest settings; the argument is the settings version number.")
        } else if knob.lastCheckin == nil {
            Text("Not delivered yet").foregroundStyle(.secondary)
        } else {
            Text("Waiting for the knob (has \(running), latest \(knob.configVersion))",
                 comment: "Settings › Knob: the knob hasn't picked up the latest settings; the settings version it runs, then the latest.")
                .foregroundStyle(.orange)
        }
    }

    @ViewBuilder private func wifiRow(_ rssi: Int, _ wifi: KnobWifi?) -> some View {
        let row = LabeledContent("Wi-Fi") { wifiLinkText(rssi, wifi) }
        if let bssid = wifi?.bssid {
            row.help(Text("Access point \(bssid)",
                          comment: "Settings › Knob: tooltip on the Wi-Fi row naming the access point the knob uses (\"Access point 78:45:58:4b:c2:cd\")."))
        } else {
            row
        }
    }

    private func wifiLinkText(_ rssi: Int, _ wifi: KnobWifi?) -> Text {
        var text = rssi != 0 ? wifiSignalText(rssi)
            : Text("Signal unknown", comment: "Settings › Knob Wi-Fi row when the knob could not read its signal, before the lowest signal and channel (\"Signal unknown, channel 6\").")
        if let low = wifi?.rssiMin, low != 0 {
            text = Text("\(text) (min \(low) dBm)",
                        comment: "Settings › Knob Wi-Fi row: the signal, then its lowest value since the last check-in (\"Weak · -74 dBm (min -83 dBm)\").")
        }
        if let channel = wifi?.channel, channel > 0 {
            text = Text("\(text), channel \(channel)",
                        comment: "Settings › Knob Wi-Fi row: the signal, then the Wi-Fi channel (\"Weak · -74 dBm, channel 6\").")
        }
        return text
    }

    private func reconnects(_ wifi: KnobWifi) -> Text {
        guard let code = wifi.lastReason, code > 0 else { return Text(verbatim: "\(wifi.disconnects)") }
        if let reason = KnobWifiReason.label(code) {
            return Text("\(wifi.disconnects) (last: \(code) \(reason))",
                        comment: "Settings › Knob: Wi-Fi reconnects since the knob started, then the last disconnect's ESP-IDF reason code and its meaning (\"3 (last: 203 association failed)\").")
        }
        return Text("\(wifi.disconnects) (last: reason \(code))",
                    comment: "Settings › Knob: Wi-Fi reconnects since the knob started, then the last disconnect's ESP-IDF reason code this app has no words for (\"3 (last: reason 250)\").")
    }

    @ViewBuilder private func diagRows(_ diag: KnobDiag) -> some View {
        if let crash = diag.crash {
            LabeledContent("Crash") { crashText(crash).foregroundStyle(.orange).textSelection(.enabled) }
                .help("The knob saved a crash dump in flash. It reports it on every check-in until the dump is erased over USB (idf.py coredump-erase or a reflash).")
        }
        if let reset = diag.resetReason?.nonEmpty {
            LabeledContent("Last restart") { resetText(reset, diag) }
                .help("Why the knob last started, as ESP-IDF reports it.")
        }
        if let boots = diag.boots, boots > 0 {
            LabeledContent("Boots") { bootsText(boots, reboots: diag.reboots ?? 0) }
                .help("Times the knob has started since its flash was erased.")
        }
        if let low = diag.heapInternalMin, low > 0 {
            LabeledContent("Lowest free memory") {
                if let largest = diag.largestBlockMin {
                    Text("\(bytes(low)), largest block down to \(bytes(largest))",
                         comment: "Settings › Knob: the lowest free internal RAM since the knob started, then the smallest its largest free block got (\"69 KB, largest block down to 30 KB\").")
                } else {
                    Text(verbatim: bytes(low))
                }
            }
            .help("The low point of internal RAM since the knob started.")
        }
    }

    private func resetText(_ code: String, _ diag: KnobDiag) -> Text {
        let now = KnobResetReason.label(code)
        if let before = diag.prevResetReason?.nonEmpty {
            return Text("\(now) (before: \(KnobResetReason.label(before)))",
                        comment: "Settings › Knob Last restart row: why the knob last started, then why it started the time before (\"Crash (before: Power on)\").")
        }
        if let unseen = diag.unseenRestarts {
            return Text("\(now) (\(unseen) restarts since last seen)",
                        comment: "Settings › Knob Last restart row: why the knob last started, then how many times it restarted between two check-ins, so the earlier reasons are unknown (\"Crash (2 restarts since last seen)\").")
        }
        return Text(verbatim: now)
    }

    private func bootsText(_ boots: Int, reboots: Int) -> Text {
        guard reboots > 0 else { return Text(verbatim: "\(boots)") }
        return Text("\(boots) (restarts seen by Ember: \(reboots))",
                    comment: "Settings › Knob Boots row: times the knob has started, then the restarts Ember noticed between check-ins (\"42 (restarts seen by Ember: 3)\").")
    }

    private func crashText(_ crash: KnobCrash) -> Text {
        let reason = KnobCrashReason.label(crash.reason?.nonEmpty ?? "unknown")
        switch (crash.task?.nonEmpty, crash.pc?.nonEmpty) {
        case let (task?, pc?):
            return Text("\(reason) in \(task) at \(pc)",
                        comment: "Settings › Knob Crash row: the crash reason, the task that crashed and the program counter (\"Panic in ember at 0x4201a2b3\").")
        case let (task?, nil):
            return Text("\(reason) in \(task)",
                        comment: "Settings › Knob Crash row: the crash reason and the task that crashed (\"Panic in ember\").")
        case let (nil, pc?):
            return Text("\(reason) at \(pc)",
                        comment: "Settings › Knob Crash row: the crash reason and the program counter (\"Panic at 0x4201a2b3\").")
        case (nil, nil):
            return Text(verbatim: reason)
        }
    }

    private func bytes(_ n: Int) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(n), countStyle: .memory)
    }
}
