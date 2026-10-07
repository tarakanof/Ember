import AppKit
import SwiftUI
import EmberKit

struct KnobStatusSection: View {
    @Environment(AppEnvironment.self) private var env
    let setUp: () -> Void
    @State private var confirmForget = false
    @State private var dumpWriteError: String?
    @State private var confirmDeleteDump: String?

    var body: some View {
        let model = env.knob
        if let knob = model.knob {
            let checkin = knob.lastCheckin
            statusGroup(knob, model: model)
            KnobFirmwareSection(checkin: checkin)
            diagnosticsGroup(knob, checkin: checkin)
            crashDumpsGroup(knob, checkin: checkin)
        }
    }

    private func statusGroup(_ knob: KnobDevice, model: KnobModel) -> some View {
        CollapsibleSection("Status", group: .knobStatus) {
            LabeledContent("Connection") { online(knob) }
            LabeledContent("Address") {
                Text(verbatim: knob.lastCheckin?.ip.nonEmpty ?? "—").textSelection(.enabled)
            }
            LabeledContent("Last check-in") {
                if let seen = knob.lastCheckin?.seenAt {
                    Text(seen, format: .relative(presentation: .named))
                } else {
                    Text("Never").foregroundStyle(.secondary)
                }
            }
            LabeledContent("Settings") { applied(knob) }
            KnobUSBRow()
            HStack {
                Button("Set Up Knob…", action: setUp)
                Spacer()
                Button("Forget Knob…", role: .destructive) { confirmForget = true }
                    .disabled(model.running.contains(.forget))
            }
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

    private func diagnosticsGroup(_ knob: KnobDevice, checkin: KnobCheckin?) -> some View {
        CollapsibleSection("Diagnostics", group: .knobDiagnostics) {
            if let wifi = checkin?.wifi {
                if let channel = wifi.channel, channel > 0 {
                    wifiRow(channel, wifi)
                }
                LabeledContent("Reconnects") { reconnects(wifi) }
                    .help("Times the knob lost Wi-Fi since it started. Each one starts a reconnect.")
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
            LabeledContent {
                Button("Show Hardware") { showKnobHardware(knob.id) }
            } label: {
                Text("Wi-Fi signal, memory and uptime are in Hardware.").foregroundStyle(.secondary).font(.callout)
            }
        }
    }

    @ViewBuilder private func crashDumpsGroup(_ knob: KnobDevice, checkin: KnobCheckin?) -> some View {
        let model = env.knob
        let crash = checkin?.diag?.crash
        let stored = model.coredumps.filter { $0.id != crash?.id }
        if crash != nil || !stored.isEmpty {
            CollapsibleSection("Crash dumps", group: .knobCrashDumps) {
                if let crash {
                    crashRow(crash, device: knob.id)
                }
                ForEach(stored) { dump in
                    dumpRow(dump, device: knob.id)
                }
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Ember keeps the newest three crash dumps. Decode one with `esp-coredump info_corefile -c <dump.bin> <cinder.elf>`. Download ELF saves the cinder.elf of the build that crashed when Ember has it; otherwise use build/cinder.elf from the matching firmware build.")
                        .textSelection(.enabled)
                    if let e = model.actionErrors[.coredump] {
                        Label { Text("Couldn't download the crash dump: \(Text(e.message))",
                                     comment: "Settings › Knob error under Crash dumps; the argument is a short reason (\"Server unreachable\").") }
                            icon: { Image(systemName: "exclamationmark.triangle.fill") }
                            .foregroundStyle(.red)
                    }
                    if let e = model.actionErrors[.deleteCoredump] {
                        Label { Text("Couldn't delete the crash dump: \(Text(e.message))",
                                     comment: "Settings › Knob error under Crash dumps after deleting a stored crash dump failed; the argument is a short reason (\"Server unreachable\").") }
                            icon: { Image(systemName: "exclamationmark.triangle.fill") }
                            .foregroundStyle(.red)
                    }
                    if let e = dumpWriteError {
                        Label { Text("Couldn't save the crash dump: \(e)",
                                     comment: "Settings › Knob error under Crash dumps after a crash dump download; the argument is the file system's reason.") }
                            icon: { Image(systemName: "exclamationmark.triangle.fill") }
                            .foregroundStyle(.red)
                    }
                }
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

    @ViewBuilder private func wifiRow(_ channel: Int, _ wifi: KnobWifi) -> some View {
        let row = LabeledContent("Wi-Fi") {
            Text("Channel \(channel)",
                 comment: "Settings › Knob Diagnostics: the Wi-Fi channel the knob uses (\"Channel 6\").")
        }
        if let bssid = wifi.bssid {
            row.help(Text("Access point \(bssid)",
                          comment: "Settings › Knob: tooltip on the Wi-Fi row naming the access point the knob uses (\"Access point 78:45:58:4b:c2:cd\")."))
        } else {
            row
        }
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
        if let reset = diag.resetReason?.nonEmpty {
            LabeledContent("Last restart") { resetText(reset, diag) }
                .help("Why the knob last started, as ESP-IDF reports it.")
        }
        if let boots = diag.boots, boots > 0 {
            LabeledContent("Boots") { bootsText(boots, reboots: diag.reboots ?? 0) }
                .help("Times the knob has started since its flash was erased.")
        }
    }

    private func crashRow(_ crash: KnobCrash, device: String) -> some View {
        let dump = env.knob.coredump(for: crash)
        return VStack(alignment: .leading, spacing: 4) {
            crashText(crash).foregroundStyle(.orange).textSelection(.enabled)
            Group {
                if let dump {
                    dumpDetails(dump)
                } else if env.knob.coredumpsLoaded {
                    Text("Still in the knob's flash")
                }
            }
            .font(.callout)
            .foregroundStyle(.secondary)
            HStack {
                Spacer()
                if let dump { downloadButton(dump, device: device) }
                elfButton(crash.elf)
            }
        }
        .padding(.vertical, 2)
        .help("The knob saved a crash dump in flash. It uploads the dump to Ember and erases it once Ember has it; older firmware reports it on every check-in until it is erased over USB (idf.py coredump-erase or a reflash).")
    }

    private func dumpRow(_ dump: KnobCoredump, device: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            crashText(KnobCrash(pc: dump.pc.nonEmpty, reason: dump.reason.nonEmpty, task: dump.task.nonEmpty))
                .textSelection(.enabled)
            dumpDetails(dump).font(.callout).foregroundStyle(.secondary)
            HStack {
                Spacer()
                downloadButton(dump, device: device)
                elfButton(dump.elf)
                deleteControl(dump)
            }
        }
        .padding(.vertical, 2)
    }

    private func dumpDetails(_ dump: KnobCoredump) -> Text {
        let when = dump.receivedAt.formatted(date: .abbreviated, time: .shortened)
        let size = ByteCountFormatter.string(fromByteCount: Int64(dump.size), countStyle: .memory)
        let fw = dump.firmware(images: env.knob.ota.images)
        switch (fw.crashed, fw.uploadedBy) {
        case let (crashed?, uploader?):
            return Text("Firmware \(crashed), uploaded by \(uploader) · \(size) · \(when)",
                        comment: "Settings › Knob Crash dumps row details: the firmware that crashed, the firmware that uploaded the dump later, the dump's size and when Ember received it (\"Firmware 0.9.13, uploaded by 0.9.14 · 64 KB · 6 Oct 2026 at 10:00\").")
        case let (crashed?, nil):
            return Text("Firmware \(crashed) · \(size) · \(when)",
                        comment: "Settings › Knob Crash dumps row details: the firmware that crashed, the dump's size and when Ember received it (\"Firmware 0.9.14 · 64 KB · 6 Oct 2026 at 10:00\").")
        case let (nil, uploader?):
            return Text("Uploaded by \(uploader) · \(size) · \(when)",
                        comment: "Settings › Knob Crash dumps row details: the firmware that uploaded the dump (the crashed build is unknown), the dump's size and when Ember received it (\"Uploaded by 0.9.14 · 64 KB · 6 Oct 2026 at 10:00\").")
        case (nil, nil):
            return Text(verbatim: "\(size) · \(when)")
        }
    }

    @ViewBuilder private func deleteControl(_ dump: KnobCoredump) -> some View {
        if confirmDeleteDump == dump.id {
            Text("Delete this dump?").foregroundStyle(.secondary)
            Button("Delete", role: .destructive) {
                Task {
                    if await env.knob.deleteCoredump(dump) { confirmDeleteDump = nil }
                }
            }
            .disabled(env.knob.running.contains(.deleteCoredump))
            Button("Cancel") { confirmDeleteDump = nil }
        } else {
            Button("Delete…") {
                env.knob.clearError(.deleteCoredump)
                confirmDeleteDump = dump.id
            }
            .disabled(env.knob.running.contains(.deleteCoredump))
        }
    }

    private func downloadButton(_ dump: KnobCoredump, device: String) -> some View {
        Button("Download Crash Dump…") { Task { await saveDump(dump, device: device) } }
            .disabled(env.knob.running.contains(.coredump))
    }

    @ViewBuilder private func elfButton(_ build: String?) -> some View {
        if let image = env.knob.ota.elfImage(build: build) {
            Button("Download ELF…") { Task { await saveELF(image) } }
                .disabled(env.knob.ota.running.contains(.elf))
        }
    }

    private func saveELF(_ image: KnobFirmwareImage) async {
        dumpWriteError = nil
        let panel = NSSavePanel()
        panel.nameFieldStringValue = image.elfFilename
        panel.canCreateDirectories = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        guard let data = await env.knob.ota.elfData(build: image.build) else { return }
        do {
            try data.write(to: url, options: .atomic)
        } catch {
            dumpWriteError = error.localizedDescription
        }
    }

    private func saveDump(_ dump: KnobCoredump, device: String) async {
        dumpWriteError = nil
        let panel = NSSavePanel()
        panel.nameFieldStringValue = dump.filename(device: device)
        panel.canCreateDirectories = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        guard let data = await env.knob.coredumpData(dump) else { return }
        do {
            try data.write(to: url, options: .atomic)
        } catch {
            dumpWriteError = error.localizedDescription
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
}
