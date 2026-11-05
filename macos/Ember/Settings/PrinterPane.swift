import SwiftUI
import EmberKit

struct PrinterSourcePane: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let client = env.connection.client
        PrinterPaneBody(model: PrinterSettingsModel(client: client))
            .id("\(client.baseURL?.absoluteString ?? "")|\(client.token?.hashValue ?? 0)")
    }
}

private struct PrinterPaneBody: View {
    @State var model: PrinterSettingsModel

    var body: some View {
        @Bindable var config = model.config
        let c = config.draft
        Form {
            LoadStateSection(isLoaded: config.isLoaded, error: config.loadError,
                             offMessage: "This server doesn't support the printer. Update the Ember server.",
                             retry: { await model.load() })

            Section {
                Toggle("Show the printer", isOn: $config.draft.enabled)
            } header: {
                Text("Printer")
            } footer: {
                SaveErrorFooter(error: config.saveError)
            }
            .disabled(!config.isLoaded)

            Section {
                statusRow
                Picker("Address", selection: addressMode) {
                    Text("Auto").tag(true)
                    Text("Manual").tag(false)
                }
                .pickerStyle(.segmented)
                if c.isAuto {
                    if let host = model.foundHost {
                        LabeledContent("Found") { Text(verbatim: host) }
                    } else if c.enabled, model.status != .unknown {
                        LabeledContent("Found") { Text("Nothing yet").foregroundStyle(.secondary) }
                    }
                } else {
                    TextField("Printer address", text: $config.draft.url, prompt: Text(verbatim: "http://192.168.1.50"))
                        .autocorrectionDisabled()
                }
            } header: {
                Text("Connection")
            } footer: {
                Text("Auto looks for the printer on your network by its PrusaLink name. The password is read by the server from EMBER_PRUSALINK_PASSWORD; this app never asks for it.")
            }
            .disabled(!config.isLoaded || !c.enabled)

            Section {
                Toggle("Show on the clock", isOn: $config.draft.clockApp)
                Toggle("Notify when a print is done", isOn: $config.draft.notifyDone)
                Toggle("Notify when the printer needs you", isOn: $config.draft.notifyAttention)
                StepperRow(title: "Keep \"done\" on the clock for", value: $config.draft.doneHoldMin,
                           range: PrinterConfig.holdRange.including(c.doneHoldMin), step: 5) { Text("\($0) min") }
            } header: {
                Text("Clock")
            }
            .disabled(!config.isLoaded || !c.enabled)

            if let response = model.preview.response {
                Section {
                    PreviewCanvas(frames: response.frames, maxPitch: PanelPreview.maxPitch)
                        .ledBezel()
                        .frame(maxWidth: .infinity)
                        .settingsPreviewRow()
                        .accessibilityHidden(true)
                } header: {
                    Text("On the Clock")
                }
            }
        }
        .formStyle(.grouped)
        .autosaves(config)
        .reloads {
            await model.load()
            model.refreshPreview()
        }
        .task {
            config.onSaved = { _, _ in Task { await model.saved() } }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(10))
                await model.refreshState()
            }
        }
        .onDisappear { model.preview.cancel() }
    }

    private var addressMode: Binding<Bool> {
        Binding(
            get: { model.config.draft.isAuto },
            set: { model.config.draft.url = $0 ? PrinterConfig.auto : "" })
    }

    @ViewBuilder private var statusRow: some View {
        let status = model.status
        LabeledContent("Status") {
            Text(status.title)
                .foregroundStyle(status.isProblem ? .orange : (status == .unknown ? .secondary : .primary))
        }
        if let detail = status.detail {
            Label { Text(detail) } icon: { Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange) }
                .font(.callout)
        }
    }
}
