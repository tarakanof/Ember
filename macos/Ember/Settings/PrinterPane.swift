import SwiftUI
import EmberKit

struct PrinterSourcePane: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let client = env.connection.client
        PrinterPaneBody(model: PrinterModelCache.shared.model(for: client))
            .id(ObjectIdentifier(PrinterModelCache.shared.model(for: client)))
    }
}

private struct PrinterPaneBody: View {
    @State var model: PrinterSettingsModel
    @FocusState private var addressFocused: Bool
    @Environment(\.appearsActive) private var appearsActive

    var body: some View {
        @Bindable var model = model
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
                if !model.manualMode {
                    if let host = model.foundHost {
                        LabeledContent("Found") { Text(verbatim: host) }
                    } else if c.enabled, model.status == .notFound {
                        LabeledContent("Found") { Text("Nothing yet").foregroundStyle(.secondary) }
                    }
                } else {
                    TextField("Printer address", text: $model.addressText, prompt: Text(verbatim: "http://192.168.1.50"))
                        .autocorrectionDisabled()
                        .focused($addressFocused)
                        .onSubmit { model.commitAddress() }
                        .onChange(of: addressFocused) { _, focused in
                            if !focused { model.commitAddress() }
                        }
                    if model.addressInvalid {
                        Label("Use http:// or https:// and the printer's host, for example http://192.168.1.50.",
                              systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .font(.callout)
                    }
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
                           range: (5...PrinterConfig.holdRange.upperBound).including(c.doneHoldMin), step: 5) { Text("\($0) min") }
            } header: {
                Text("Clock")
            }
            .disabled(!config.isLoaded || !c.enabled)

            if let response = model.previewResponse {
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
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(10))
                if Task.isCancelled { break }
                if model.config.loadError == .featureOff { continue }
                await model.refreshState()
                if !model.previewMissing { model.refreshPreview() }
            }
        }
        .onChange(of: appearsActive) { _, active in
            if !active { model.commitAddress() }
        }
        .onDisappear {
            model.commitAddress()
            model.preview.cancel()
        }
    }

    private var addressMode: Binding<Bool> {
        Binding(
            get: { !model.manualMode },
            set: { if $0 { model.chooseAuto() } else { model.chooseManual() } })
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
