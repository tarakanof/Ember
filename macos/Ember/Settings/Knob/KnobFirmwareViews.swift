import AppKit
import SwiftUI
import UniformTypeIdentifiers
import EmberKit

struct KnobFirmwareRow: View {
    @Environment(AppEnvironment.self) private var env
    let checkin: KnobCheckin?

    var body: some View {
        let ota = env.knob.ota
        LabeledContent("Firmware") {
            VStack(alignment: .trailing, spacing: 4) {
                HStack {
                    versionText.textSelection(.enabled)
                    if let status = ota.status, let version = status.updateVersion {
                        Button {
                            Task { await ota.update(to: version) }
                        } label: {
                            Text("Update to \(version)",
                                 comment: "Settings › Knob Firmware row button; the argument is the newer firmware version stored on Ember (\"Update to 0.9.14\").")
                        }
                        .disabled(!status.canRollBack || ota.running.contains(.update))
                    }
                }
                if let status = ota.status {
                    KnobOTAProgress(status: status)
                }
            }
        }
    }

    private var versionText: Text {
        guard let fw = checkin?.fw.nonEmpty else { return Text(verbatim: "—") }
        if let build = checkin?.fwBuild?.nonEmpty { return Text(verbatim: "\(fw) (\(build))") }
        return Text(verbatim: fw)
    }
}

struct KnobOTAProgress: View {
    @Environment(AppEnvironment.self) private var env
    let status: KnobOTAStatus

    var body: some View {
        let ota = env.knob.ota
        VStack(alignment: .trailing, spacing: 4) {
            if status.isBusy {
                if status.phase == .downloading, let p = status.progress {
                    ProgressView(value: p).frame(width: 180)
                } else {
                    ProgressView().progressViewStyle(.linear).frame(width: 180)
                }
                phaseText.foregroundStyle(.secondary)
                if status.canCancel {
                    Button("Cancel Update") { Task { await ota.cancel() } }
                        .disabled(ota.running.contains(.cancel))
                }
            } else if status.phase.isFailure {
                failureText.foregroundStyle(.red)
                HStack {
                    Button("Dismiss") { Task { await ota.cancel() } }
                        .disabled(ota.running.contains(.cancel))
                        .help("Clear this failed update.")
                    Button("Retry") { Task { await ota.retry() } }
                        .disabled(!status.canRollBack || ota.running.contains(.retry))
                }
            } else if status.phase == .done, let at = status.finishedAt, at > .now.addingTimeInterval(-3600),
                      let fw = status.running?.fw {
                Text("Updated to \(fw)",
                     comment: "Settings › Knob Firmware row after an update; the argument is the firmware the knob runs now (\"Updated to 0.9.14\").")
                    .foregroundStyle(.secondary)
            }
            if status.running != nil, !status.canRollBack {
                Text("This knob's bootloader can't roll back. Flash it once over USB (see cinder docs/workflow.md).")
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.trailing)
            }
            if let e = ota.errors[.update] ?? ota.errors[.retry] {
                Text("Couldn't start the update: \(Text(e.message))",
                     comment: "Settings › Knob Firmware row error; the argument is a short reason (\"Server unreachable\").")
                    .foregroundStyle(.red)
            }
            if let e = ota.errors[.cancel] {
                Text("Couldn't clear the update: \(Text(e.message))",
                     comment: "Settings › Knob Firmware row error after Dismiss or Cancel Update; the argument is a short reason (\"Server unreachable\").")
                    .foregroundStyle(.red)
            }
        }
        .font(.callout)
    }

    private var phaseText: Text {
        switch status.phase {
        case .downloading:
            if let pct = status.progressPct {
                return Text("Downloading \(pct) %",
                            comment: "Settings › Knob Firmware row while the knob downloads an update; the argument is the percentage sent (\"Downloading 42 %\").")
            }
            return Text("Downloading")
        case .installing: return Text("Installing")
        case .restarting: return Text("Restarting")
        case .verifying: return Text("Checking the new firmware")
        case .offered:
            if status.waitingFor == .idleInput {
                return Text("Waiting until the knob has been idle for 10 minutes")
            }
            return Text("Waiting for the knob to start")
        case .idle, .done, .failed, .rolledBack:
            switch status.waitingFor {
            case .pomodoro?: return Text("Waiting for the Pomodoro to end")
            case .coredump?: return Text("Waiting for the crash dump upload")
            default: return Text("Waiting for the knob to check in")
            }
        }
    }

    private var failureText: Text {
        let reason = KnobOTAError.label(status.error ?? "")
        if status.phase == .rolledBack {
            let fw = status.running?.fw ?? status.from ?? "—"
            return Text("Rolled back to \(fw): \(reason)",
                        comment: "Settings › Knob Firmware row after a failed update; the firmware the knob went back to, then why (\"Rolled back to 0.9.13: the knob could not reach Ember after the update\").")
        }
        let version = status.attemptVersion ?? "—"
        return Text("Couldn't update to \(version): \(reason)",
                    comment: "Settings › Knob Firmware row after a failed download or install; the version, then why (\"Couldn't update to 0.9.14: the download failed\").")
    }
}

struct KnobFirmwareSection: View {
    @Environment(AppEnvironment.self) private var env
    let checkin: KnobCheckin?
    @AppStorage(SettingsGroup.storageKey) private var collapsedGroups = ""
    @State private var channel = KnobFirmwareImage.test
    @State private var confirmDelete: KnobFirmwareImage?
    @State private var confirmDeleteOld: [KnobFirmwareImage]?
    @State private var confirmRelease: KnobFirmwareImage?
    @State private var writeError: String?

    var body: some View {
        let ota = env.knob.ota
        CollapsibleSection("Firmware & updates", group: .knobFirmware, summary: summary) {
            KnobFirmwareRow(checkin: checkin)
            Picker("Firmware updates", selection: Binding(
                get: { ota.status?.mode ?? .manual },
                set: { mode in Task { await ota.setMode(mode) } })) {
                Text("Ask first").tag(KnobOTAMode.manual)
                Text("Automatic (release builds, when the knob is idle)").tag(KnobOTAMode.auto)
            }
            .disabled(ota.status == nil || ota.running.contains(.mode))
            if !ota.unsupported {
                if ota.images.isEmpty {
                    Text("No firmware stored on Ember yet.").foregroundStyle(.secondary)
                }
                ForEach(ota.images) { image in row(image) }
                uploadRow
            }
        } footer: {
            footer
        }
        .task(id: env.knob.knob?.id) { await ota.followProgress { await env.knob.load() } }
        .onChange(of: ota.status) { old, new in
            if KnobOTAStatus.expandsGroup(from: old, to: new) {
                collapsedGroups = SettingsGroup.knobFirmware.setCollapsed(false, in: collapsedGroups)
            }
        }
        .confirmationDialog(deleteTitle, isPresented: Binding(
            get: { confirmDelete != nil },
            set: { if !$0 { confirmDelete = nil } }), presenting: confirmDelete) { image in
            Button("Delete", role: .destructive) { Task { await ota.delete(image) } }
            Button("Cancel", role: .cancel) {}
        } message: { image in
            if ota.runsOnKnob(image), image.elf {
                Text("The knob runs this build. Without its ELF, crash dumps from it can't be decoded.")
            } else if image.elf {
                Text("Ember removes the image and its ELF.")
            } else {
                Text("Ember removes the image.")
            }
        }
        .confirmationDialog(deleteOldTitle, isPresented: Binding(
            get: { confirmDeleteOld != nil },
            set: { if !$0 { confirmDeleteOld = nil } }), presenting: confirmDeleteOld) { images in
            Button("Delete", role: .destructive) { Task { await ota.deleteOldBuilds(images) } }
            Button("Cancel", role: .cancel) {}
        } message: { images in
            Text("Ember removes \(images.map(\.version).formatted(.list(type: .and))) and their ELFs. It keeps the build on each knob, the newest Release (and a newer Test build), and any build an update is installing or offering.",
                 comment: "Settings › Knob Firmware & updates: confirmation before Delete Old Builds; the argument lists the versions that go (\"0.9.39, 0.9.40 and 0.9.41\").")
        }
        .confirmationDialog(releaseTitle, isPresented: Binding(
            get: { confirmRelease != nil },
            set: { if !$0 { confirmRelease = nil } }), presenting: confirmRelease) { image in
            Button("Mark as Release") { Task { await ota.setChannel(image, to: KnobFirmwareImage.release) } }
            Button("Cancel", role: .cancel) {}
        } message: { image in
            if ota.status?.mode == .auto {
                Text("Automatic updates are on: if \(image.version) is newer than the knob's firmware, the knob installs it once it has been idle for 10 minutes.",
                     comment: "Settings › Knob Firmware & updates: confirmation before marking an image as Release while Automatic updates are on; the argument is its version.")
            } else {
                Text("With Automatic updates on, the knob would install \(image.version) once it has been idle for 10 minutes, if it is newer than the knob's firmware.",
                     comment: "Settings › Knob Firmware & updates: confirmation before marking an image as Release while updates are set to Ask first; the argument is its version.")
            }
        }
    }

    private var summary: Text {
        let version = Text(verbatim: checkin?.fw.nonEmpty ?? "—")
        switch env.knob.ota.status?.summary {
        case .updateAvailable(let next)?:
            return Text("\(version) · Update to \(next) available",
                        comment: "Settings › Knob Firmware & updates header while collapsed: the running firmware, then the newer version stored on Ember (\"0.9.15 · Update to 0.9.16 available\").")
        case .updating(let pct?)?:
            return Text("\(version) · Updating, \(pct) %",
                        comment: "Settings › Knob Firmware & updates header while collapsed and the knob downloads an update: the running firmware, then the percentage sent (\"0.9.15 · Updating, 42 %\").")
        case .updating(nil)?:
            return Text("\(version) · Updating",
                        comment: "Settings › Knob Firmware & updates header while collapsed and an update runs: the running firmware (\"0.9.15 · Updating\").")
        case .failed(let attempt)?:
            return Text("\(version) · Update to \(attempt) failed",
                        comment: "Settings › Knob Firmware & updates header while collapsed after an update failed: the running firmware, then the version it tried (\"0.9.15 · Update to 0.9.16 failed\").")
                .foregroundStyle(.red)
        case .current?, nil:
            return version
        }
    }

    @ViewBuilder private var footer: some View {
        let ota = env.knob.ota
        VStack(alignment: .leading, spacing: 4) {
            if ota.unsupported {
                Text("This server can't store knob firmware. Update the Ember server.")
            } else {
                Text("Ember never installs firmware during a Pomodoro. With Automatic, it installs a newer release build after the knob has been untouched for 10 minutes.")
            }
            if let status = ota.status, status.running != nil, !status.canRollBack,
               ota.images.contains(where: status.canInstall) {
                Text(KnobOTAError.noRollback)
            }
            if let e = ota.errors[.mode] {
                Text("Couldn't change the update mode: \(Text(e.message))",
                     comment: "Settings › Knob Firmware section error; the argument is a short reason (\"Server unreachable\").")
                    .foregroundStyle(.red)
            }
            ForEach([KnobOTAAction.upload, .install, .channel, .elf], id: \.self) { action in
                if let e = ota.errors[action] {
                    Label { Text(e.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .foregroundStyle(.red)
                }
            }
            if let e = ota.errors[.delete] {
                Label { Text("Couldn't check what to delete, so nothing was deleted: \(Text(e.message))",
                             comment: "Settings › Knob Firmware & updates error when Delete Old Builds can't reload the knob status, the stored images or the knob list first; the argument is a short reason (\"Server unreachable\").") }
                    icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .foregroundStyle(.red)
            }
            if let writeError {
                Label { Text("Couldn't save the ELF: \(writeError)",
                             comment: "Settings › Knob Firmware & updates error after an ELF download; the argument is the file system's reason.") }
                    icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .foregroundStyle(.red)
            }
        }
    }

    @ViewBuilder private var uploadRow: some View {
        let ota = env.knob.ota
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Upload a build")
                Spacer()
                Picker("Channel", selection: $channel) {
                    Text("Test").tag(KnobFirmwareImage.test)
                    Text("Release").tag(KnobFirmwareImage.release)
                }
                .labelsHidden()
                .fixedSize()
                Button("Upload…") { Task { await upload() } }
                    .disabled(ota.running.contains(.upload))
            }
            HStack {
                Spacer()
                Button {
                    confirmDeleteOld = oldBuilds
                } label: {
                    Text("Delete Old Builds…",
                         comment: "Settings › Knob Firmware & updates button: deletes every stored image except the one on each knob, the newest Release (and a newer Test build), and any build an update is installing or offering.")
                }
                .disabled(oldBuilds.isEmpty || ota.running.contains(.delete))
                .help(Text("Delete every stored build except the one on each knob, the newest Release (and a newer Test build), and any build an update is installing or offering.",
                           comment: "Settings › Knob Firmware & updates: tooltip on the Delete Old Builds button."))
            }
            if let p = ota.uploadProgress {
                HStack {
                    ProgressView(value: p)
                    Text(ota.uploadStage == .elf ? "Uploading the ELF…" : "Uploading the image…")
                        .foregroundStyle(.secondary)
                }
            }
            Text("Choose cinder.bin from a release build. If cinder.elf is next to it, Ember stores it too, for decoding crash dumps. Ember refuses builds that contain Wi-Fi or token secrets.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, 2)
    }

    @ViewBuilder private func row(_ image: KnobFirmwareImage) -> some View {
        let ota = env.knob.ota
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(verbatim: image.version).font(.body.monospacedDigit())
                ForEach(ota.badges(for: image), id: \.self) { KnobFirmwareBadgeView(badge: $0) }
            }
            details(image).font(.callout).foregroundStyle(.secondary)
            HStack {
                Spacer()
                if let status = ota.status, status.canInstall(image) {
                    Button("Install") { Task { await ota.install(image) } }
                        .disabled(!status.canRollBack || ota.running.contains(.install))
                        .help(status.canRollBack ? Text("Install this build on the knob.") : Text(KnobOTAError.noRollback))
                }
                Picker("Channel", selection: Binding(
                    get: { image.channel },
                    set: { channel in
                        if KnobFirmwareImage.needsConfirmation(from: image.channel, to: channel) {
                            confirmRelease = image
                        } else {
                            Task { await ota.setChannel(image, to: channel) }
                        }
                    })) {
                    Text("Test").tag(KnobFirmwareImage.test)
                    Text("Release").tag(KnobFirmwareImage.release)
                }
                .labelsHidden()
                .fixedSize()
                .disabled(ota.running.contains(.channel))
                .help("Automatic updates install only Release builds.")
                if image.elf {
                    Button("Download ELF…") { Task { await saveELF(image) } }
                        .disabled(ota.running.contains(.elf))
                }
                Button("Delete…") { confirmDelete = image }
                    .disabled(ota.running.contains(.delete))
            }
            if let e = ota.deleteError(for: image) {
                Label { Text("Couldn't delete \(image.version): \(Text(e.message))",
                             comment: "Settings › Knob Firmware & updates image row error after Delete; the version, then why (\"Couldn't delete 0.9.17: A knob is updating to this version or waiting to. …\").") }
                    icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .font(.callout)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, 2)
    }

    private var releaseTitle: Text {
        Text("Mark \(confirmRelease?.version ?? "") as Release?",
             comment: "Settings › Knob Firmware & updates: confirmation title before moving a stored image to the Release channel; the argument is its version (\"Mark 0.9.17 as Release?\").")
    }

    private var deleteTitle: Text {
        Text("Delete \(confirmDelete?.version ?? "") from Ember?",
             comment: "Settings › Knob Firmware & updates: confirmation before deleting a stored image; the argument is its version (\"Delete 0.9.17 from Ember?\").")
    }

    private var oldBuilds: [KnobFirmwareImage] {
        env.knob.ota.oldBuilds(otherKnobs: KnobOTAModel.runningVersions(env.knob.devices))
    }

    private var deleteOldTitle: Text {
        Text("Delete ^[\(confirmDeleteOld?.count ?? 0) old build](inflect: true) from Ember?",
             comment: "Settings › Knob Firmware & updates: confirmation title before Delete Old Builds; the argument is how many images go (\"Delete 4 old builds from Ember?\").")
    }

    private func details(_ image: KnobFirmwareImage) -> Text {
        let size = ByteCountFormatter.string(fromByteCount: Int64(image.size), countStyle: .file)
        let date = image.uploadedAt.formatted(date: .abbreviated, time: .shortened)
        if image.elf {
            return Text("\(size) · \(date) · IDF \(image.idfVer) · ELF",
                        comment: "Settings › Knob Firmware & updates image row details: image size, upload date, ESP-IDF version, and that the ELF is stored (\"1.6 MB · 6 Oct 2026 at 12:00 · IDF v5.5.5 · ELF\").")
        }
        return Text("\(size) · \(date) · IDF \(image.idfVer)",
                    comment: "Settings › Knob Firmware & updates image row details for an image without an ELF (a No ELF badge says so): image size, upload date, ESP-IDF version (\"1.6 MB · 6 Oct 2026 at 12:00 · IDF v5.5.5\").")
    }

    private func upload() async {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [UTType(filenameExtension: "bin") ?? .data]
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        await env.knob.ota.upload(binary: url, channel: channel, elf: KnobOTAModel.elfURL(besides: url))
    }

    private func saveELF(_ image: KnobFirmwareImage) async {
        writeError = nil
        let panel = NSSavePanel()
        panel.nameFieldStringValue = image.elfFilename
        panel.canCreateDirectories = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        guard let data = await env.knob.ota.elfData(version: image.version) else { return }
        do {
            try data.write(to: url, options: .atomic)
        } catch {
            writeError = error.localizedDescription
        }
    }
}

struct KnobFirmwareBadgeView: View {
    let badge: KnobFirmwareBadge

    var body: some View {
        let style = Self.style(badge)
        label
            .font(.caption.weight(.semibold))
            .foregroundStyle(style.foreground)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(Capsule().fill(style.background))
            .fixedSize()
    }

    private var label: Text {
        switch badge {
        case .onKnob: Text("On the knob", comment: "Settings › Knob Firmware & updates image row badge (green): the knob runs this build.")
        case .latest: Text("Latest", comment: "Settings › Knob Firmware & updates image row badge (blue): the highest version stored on Ember.")
        case .installing: Text("Installing", comment: "Settings › Knob Firmware & updates: the orange image row badge on the build the knob is downloading or installing; also the Firmware row's progress phase while the knob writes the update.")
        case .failed: Text("Failed", comment: "Settings › Knob Firmware & updates: the red image row badge on a build whose last update failed or rolled back; also the Clock hardware chart line for failed publishes.")
        case .noELF: Text("No ELF", comment: "Settings › Knob Firmware & updates image row badge (warning): Ember has no ELF for this build, so its crash dumps can't be decoded.")
        }
    }

    private static func style(_ badge: KnobFirmwareBadge) -> (foreground: Color, background: Color) {
        switch badge {
        case .onKnob: (.white, adaptive(light: NSColor(srgbRed: 0.08, green: 0.42, blue: 0.18, alpha: 1),
                                        dark: NSColor(srgbRed: 0.08, green: 0.40, blue: 0.17, alpha: 1)))
        case .latest: tinted(light: (0.00, 0.27, 0.62), dark: (0.62, 0.80, 1.00))
        case .installing: tinted(light: (0.50, 0.23, 0.00), dark: (1.00, 0.74, 0.45))
        case .failed: tinted(light: (0.62, 0.06, 0.06), dark: (1.00, 0.66, 0.64))
        case .noELF: tinted(light: (0.40, 0.30, 0.00), dark: (1.00, 0.86, 0.40))
        }
    }

    private static func tinted(light: (Double, Double, Double), dark: (Double, Double, Double)) -> (Color, Color) {
        let fg = { (c: (Double, Double, Double)) in NSColor(srgbRed: c.0, green: c.1, blue: c.2, alpha: 1) }
        let bg = { (c: (Double, Double, Double), a: Double) in NSColor(srgbRed: c.0, green: c.1, blue: c.2, alpha: a) }
        return (adaptive(light: fg(light), dark: fg(dark)), adaptive(light: bg(light, 0.14), dark: bg(dark, 0.22)))
    }

    private static func adaptive(light: NSColor, dark: NSColor) -> Color {
        Color(nsColor: NSColor(name: nil) { $0.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? dark : light })
    }
}
