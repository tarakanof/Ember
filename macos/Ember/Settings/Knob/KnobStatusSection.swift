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
                if let rssi = checkin?.rssi, rssi != 0 {
                    LabeledContent("Wi-Fi") { wifiSignalText(rssi) }
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

    private func bytes(_ n: Int) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(n), countStyle: .memory)
    }
}
