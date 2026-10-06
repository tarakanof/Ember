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
                    if let status = ota.status, let version = status.available, !status.isBusy {
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
        .task(id: env.knob.knob?.id) { await poll() }
    }

    private var versionText: Text {
        guard let fw = checkin?.fw.nonEmpty else { return Text(verbatim: "—") }
        if let build = checkin?.fwBuild?.nonEmpty { return Text(verbatim: "\(fw) (\(build))") }
        return Text(verbatim: fw)
    }

    private func poll() async {
        let ota = env.knob.ota
        while !Task.isCancelled {
            try? await Task.sleep(for: ota.pollInterval)
            guard ota.status?.phase.isInProgress == true else { continue }
            await ota.loadStatus()
            if ota.status?.phase.isInProgress == false { await env.knob.load() }
        }
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
            } else if status.phase.isFailure {
                failureText.foregroundStyle(.red)
                Button("Retry") { Task { await ota.retry() } }
                    .disabled(!status.canRollBack || ota.running.contains(.retry))
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
        let version = status.target ?? "—"
        return Text("Couldn't update to \(version): \(reason)",
                    comment: "Settings › Knob Firmware row after a failed download or install; the version, then why (\"Couldn't update to 0.9.14: the download failed\").")
    }
}

struct KnobFirmwareSection: View {
    @Environment(AppEnvironment.self) private var env
    @State private var showSheet = false

    var body: some View {
        let ota = env.knob.ota
        Section {
            Picker("Firmware updates", selection: Binding(
                get: { ota.status?.mode ?? .manual },
                set: { mode in Task { await ota.setMode(mode) } })) {
                Text("Ask first").tag(KnobOTAMode.manual)
                Text("Automatic (release builds, when the knob is idle)").tag(KnobOTAMode.auto)
            }
            .disabled(ota.status == nil || ota.running.contains(.mode))
            LabeledContent {
                Button("Manage Firmware…") { showSheet = true }
            } label: {
                Text("Firmware images stored on Ember").foregroundStyle(.secondary).font(.callout)
            }
        } header: {
            Text("Firmware")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                if ota.unsupported {
                    Text("This server can't store knob firmware. Update the Ember server.")
                } else {
                    Text("Ember never installs firmware during a Pomodoro. With Automatic, it installs a newer release build after the knob has been untouched for 10 minutes.")
                }
                if let e = ota.errors[.mode] {
                    Text("Couldn't change the update mode: \(Text(e.message))",
                         comment: "Settings › Knob Firmware section error; the argument is a short reason (\"Server unreachable\").")
                        .foregroundStyle(.red)
                }
            }
        }
        .sheet(isPresented: $showSheet) { KnobFirmwareSheet() }
    }
}

struct KnobFirmwareSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var channel = KnobFirmwareImage.test
    @State private var confirmDelete: String?
    @State private var writeError: String?

    var body: some View {
        let ota = env.knob.ota
        VStack(alignment: .leading, spacing: 12) {
            Text("Knob Firmware").font(.headline)
            Form {
                if ota.images.isEmpty {
                    Text("No firmware stored on Ember yet.").foregroundStyle(.secondary)
                }
                ForEach(ota.images) { image in row(image) }
            }
            .formStyle(.grouped)
            if let p = ota.uploadProgress {
                HStack {
                    ProgressView(value: p)
                    Text(ota.uploadStage == .elf ? "Uploading the ELF…" : "Uploading the image…")
                        .foregroundStyle(.secondary)
                }
            }
            errors
            HStack {
                Picker("Channel", selection: $channel) {
                    Text("Test").tag(KnobFirmwareImage.test)
                    Text("Release").tag(KnobFirmwareImage.release)
                }
                .fixedSize()
                Button("Upload…") { Task { await upload() } }
                    .disabled(ota.running.contains(.upload))
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            Text("Choose cinder.bin from a release build. If cinder.elf is next to it, Ember stores it too, for decoding crash dumps. Ember refuses builds that contain Wi-Fi or token secrets.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .padding(20)
        .frame(width: 560, height: 480)
        .task { await ota.loadImages() }
    }

    @ViewBuilder private var errors: some View {
        let ota = env.knob.ota
        ForEach([KnobOTAAction.upload, .promote, .delete, .elf], id: \.self) { action in
            if let e = ota.errors[action] {
                Label { Text(e.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .foregroundStyle(.red)
            }
        }
        if let writeError {
            Label { Text("Couldn't save the ELF: \(writeError)",
                         comment: "Knob firmware sheet error after an ELF download; the argument is the file system's reason.") }
                icon: { Image(systemName: "exclamationmark.triangle.fill") }
                .foregroundStyle(.red)
        }
    }

    @ViewBuilder private func row(_ image: KnobFirmwareImage) -> some View {
        let ota = env.knob.ota
        LabeledContent {
            HStack {
                if !image.isRelease {
                    Button("Mark as Release") { Task { await ota.promote(image) } }
                        .disabled(ota.running.contains(.promote))
                }
                if image.elf {
                    Button("Download ELF…") { Task { await saveELF(image) } }
                        .disabled(ota.running.contains(.elf))
                }
                if confirmDelete == image.version {
                    Button("Delete", role: .destructive) {
                        Task { if await ota.delete(image) { confirmDelete = nil } }
                    }
                    Button("Cancel") { confirmDelete = nil }
                } else {
                    Button("Delete…") { confirmDelete = image.version }
                        .disabled(ota.running.contains(.delete))
                }
            }
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(verbatim: image.version).font(.body.monospacedDigit())
                    Text(image.isRelease ? "Release" : "Test")
                        .font(.caption)
                        .padding(.horizontal, 5)
                        .background(Capsule().fill(image.isRelease ? Color.green.opacity(0.2) : Color.secondary.opacity(0.2)))
                }
                details(image).font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func details(_ image: KnobFirmwareImage) -> Text {
        let size = ByteCountFormatter.string(fromByteCount: Int64(image.size), countStyle: .file)
        let date = image.uploadedAt.formatted(date: .abbreviated, time: .shortened)
        if image.elf {
            return Text("\(size) · \(date) · IDF \(image.idfVer) · ELF",
                        comment: "Knob firmware sheet row details: image size, upload date, ESP-IDF version, and that the ELF is stored (\"1.6 MB · 6 Oct 2026 at 12:00 · IDF v5.5.5 · ELF\").")
        }
        return Text("\(size) · \(date) · IDF \(image.idfVer) · no ELF",
                    comment: "Knob firmware sheet row details: image size, upload date, ESP-IDF version, and that no ELF is stored (\"1.6 MB · 6 Oct 2026 at 12:00 · IDF v5.5.5 · no ELF\").")
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
