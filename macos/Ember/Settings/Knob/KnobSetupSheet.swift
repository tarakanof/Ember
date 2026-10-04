import SwiftUI
import CoreWLAN
import EmberKit

/// Sets the knob up over USB: Improv for Wi-Fi, `CINDER1` for Ember, a
/// token minted by the server. Also changes Wi-Fi only.
struct KnobSetupSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    let mode: KnobSetupModel.Mode
    @State private var model: KnobSetupModel?
    @State private var confirmReplace = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Group {
                if mode == .setup { Text("Set Up Knob") } else { Text("Change Knob Wi-Fi") }
            }
            .font(.headline)
            if let model {
                KnobSetupContent(model: model, replaces: replaces)
                buttons(model)
            } else {
                Spacer()
            }
        }
        .padding(20)
        .frame(width: 520, height: 580)
        .onExitCommand { if !(model?.isBusy ?? false) { dismiss() } }
        .task { await start() }
        .onChange(of: env.knob.connectedPort) { _, port in model?.attach(port) }
        .onDisappear(perform: finish)
        .confirmationDialog(replaceTitle, isPresented: $confirmReplace, titleVisibility: .visible) {
            Button("Replace") { send() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Ember shows one knob. After this setup it forgets the other knob and revokes its token.")
        }
    }

    private var replaces: KnobDevice? { model?.replaces }

    private var replaceTitle: Text {
        Text("Replace “\(replaces?.name ?? "")”?",
             comment: "Knob setup confirm title; the argument is the name of the knob already set up (\"Desk knob\").")
    }

    @ViewBuilder private func buttons(_ model: KnobSetupModel) -> some View {
        HStack {
            if case .failed(let e) = model.stage {
                if e == .emberUnauthorized {
                    Button("Re-mint Token") {
                        model.remint { await env.knob.didSetUp($0, replacing: $1) }
                    }
                }
                Button("Back") { model.edit() }
            }
            Spacer()
            switch model.stage {
            case .done:
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            case .sending:
                Button("Cancel") { dismiss() }.disabled(true)
                    .help("The knob is saving; wait until it finishes.")
            default:
                Button("Cancel", role: .cancel) { dismiss() }
                Button {
                    if replaces != nil { confirmReplace = true } else { send() }
                } label: {
                    if mode == .setup { Text("Set Up") } else { Text("Change Wi-Fi") }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!model.canSend)
            }
        }
    }

    private func send() {
        model?.send { await env.knob.didSetUp($0, replacing: $1) }
    }

    private func start() async {
        let knob = env.knob
        knob.portBusy = true
        let provisioner = KnobProvisioner(opener: knob.opener, service: KnobService(client: env.connection.client))
        let m = KnobSetupModel(mode: mode, provisioner: provisioner, emberURL: nil,
                               name: mode == .setup ? "" : (knob.knob?.name ?? ""), preferredSSID: nil)
        m.registered = { [weak knob] in knob?.knob }
        model = m
        // A probe from the pane may hold the port: wait, then take it.
        await knob.waitForProbe()
        m.attach(knob.connectedPort)
        let server = env.serverURL
        let suggestion = await Task.detached {
            KnobEmberURL.suggest(server: server, thisMac: KnobEmberURL.thisMacLANIPv4(),
                                 resolve: KnobEmberURL.resolveIPv4)
        }.value
        m.suggest(suggestion)
        m.prefer(ssid: CWWiFiClient.shared().interface()?.ssid())
    }

    private func finish() {
        guard let model else { return }
        model.close()
        let knob = env.knob
        knob.portBusy = false
        if let port = model.port, let id = model.identity { knob.record(.cinder(id), for: port) }
        Task {
            await knob.load()
            await knob.probePorts()
        }
    }
}

private struct KnobSetupContent: View {
    @Bindable var model: KnobSetupModel
    let replaces: KnobDevice?

    var body: some View {
        switch model.stage {
        case .waitingForKnob:
            placeholder {
                Image(systemName: "cable.connector").font(.largeTitle).foregroundStyle(.secondary)
                Text("Plug the knob into this Mac with a USB-C cable.")
                Text("Its screen says “Connect me to your Mac” when it isn't set up yet.")
                    .font(.callout).foregroundStyle(.secondary)
            }
        case .connecting:
            placeholder {
                ProgressView()
                Text("Connecting to the knob…")
            }
        case .notCinder:
            placeholder {
                Image(systemName: "exclamationmark.triangle.fill").font(.largeTitle).foregroundStyle(.orange)
                Text("An ESP32 board is connected but isn't running cinder.")
                Text("Flash cinder onto it first; Ember can't flash firmware.")
                    .font(.callout).foregroundStyle(.secondary)
                Button("Try Again") { model.connect() }
            }
        default:
            form
        }
    }

    private func placeholder<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        VStack(spacing: 10) { content() }
            .multilineTextAlignment(.center)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var editable: Bool { model.stage == .ready }

    private var form: some View {
        Form {
            if let id = model.identity {
                Section("Knob") {
                    LabeledContent("Name on the knob") { Text(verbatim: id.info.name) }
                    LabeledContent("Model") { Text(verbatim: id.info.chip) }
                    LabeledContent("Firmware") { Text(verbatim: "\(id.info.firmware) \(id.info.version)") }
                    if let short = id.shortID {
                        LabeledContent("ID") { Text(verbatim: short).monospaced() }
                    }
                    if let replaces {
                        Label {
                            Text("This replaces “\(replaces.name)”. Ember forgets that knob after setup.",
                                 comment: "Knob setup warning; the argument is the name of the knob already set up.")
                        } icon: {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        }
                    }
                }
            }
            switch model.stage {
            case .sending(let phase):
                progress(phase)
            case .done:
                progress(.done)
                Section {
                    Label {
                        if model.mode == .setup {
                            Text("The knob is set up and talking to Ember.")
                        } else {
                            Text("The knob is on the new network.")
                        }
                    } icon: {
                        Image(systemName: "checkmark.circle.fill")
                    }
                    .foregroundStyle(.green)
                }
            case .failed(let error):
                Section {
                    Label { KnobSetupErrorText(error: error) } icon: {
                        Image(systemName: "exclamationmark.triangle.fill")
                    }
                    .foregroundStyle(.red)
                    if model.oldTokenRevoked {
                        Text("The knob's old token no longer works. Run the setup again to finish.")
                            .foregroundStyle(.secondary)
                    }
                }
                fields
            default:
                fields
            }
        }
        .formStyle(.grouped)
    }

    @ViewBuilder private var fields: some View {
        Section {
            if model.networks.isEmpty {
                TextField("Network", text: $model.ssid, prompt: Text("Network name"))
            } else {
                Picker("Network", selection: $model.ssid) {
                    ForEach(model.networks) { n in
                        Text("\(n.ssid) · \(Text(wifiQuality(n.rssi)))",
                             comment: "Knob setup Wi-Fi picker row: network name, then signal strength in words (\"Good\").")
                            .tag(n.ssid)
                    }
                    if !model.ssid.isEmpty, !model.networks.contains(where: { $0.ssid == model.ssid }) {
                        Text(verbatim: model.ssid).tag(model.ssid)
                    }
                }
            }
            SecureField("Password", text: $model.password)
            if model.passwordInvalid {
                Text("Wi-Fi passwords are 8 to 63 characters.").foregroundStyle(.red)
            }
            HStack {
                if model.isScanning {
                    ProgressView().controlSize(.small)
                    Text("The knob is scanning…").foregroundStyle(.secondary)
                } else if model.scanFailed {
                    Text("The knob didn't return a scan. Type the network name.").foregroundStyle(.secondary)
                }
                Spacer()
                Button("Scan Again") { Task { await model.scan() } }
                    .disabled(model.isScanning)
            }
        } header: {
            Text("Wi-Fi")
        } footer: {
            Text("Networks come from the knob's own scan. The knob uses 2.4 GHz only.")
        }
        .disabled(!editable)

        if model.mode == .setup {
            Section {
                TextField("Ember address", text: $model.emberURL, prompt: Text(verbatim: "http://192.168.0.2:3627"))
                TextField("Knob name", text: $model.name, prompt: Text("Desk knob"))
            } header: {
                Text("Ember")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    if let host = model.replacedHostNote {
                        Text("The knob can't reach “\(host)”, so it gets this Mac's network address instead.",
                             comment: "Knob setup: the Ember server address (\"localhost\" or \"mini.local\") was replaced with a LAN IP.")
                    }
                    if !model.emberURL.isEmpty, !model.emberURLValid {
                        Text("Use http://address:port, with no path.").foregroundStyle(.red)
                    }
                    Text("Ember gives the knob its own token; your server token never leaves this Mac.")
                }
            }
            .disabled(!editable)
        }
    }

    private func progress(_ phase: KnobSetupPhase) -> some View {
        let steps: [KnobSetupPhase] = [.saving, .restarting, .joining(ssid: model.ssid), .reachingEmber, .done]
        let current = steps.firstIndex { same($0, phase) } ?? 0
        return Section("Progress") {
            ForEach(Array(steps.enumerated()), id: \.offset) { i, step in
                if model.mode == .setup || !same(step, .reachingEmber) {
                    let state: LocalizedStringKey = i < current || phase == .done ? "done"
                        : i == current ? "in progress" : "pending"
                    HStack(spacing: 8) {
                        Group {
                            if i < current || phase == .done {
                                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                            } else if i == current {
                                ProgressView().controlSize(.small)
                            } else {
                                Image(systemName: "circle").foregroundStyle(.tertiary)
                            }
                        }
                        .frame(width: 18)
                        stepTitle(step)
                            .foregroundStyle(i <= current ? .primary : .secondary)
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(stepTitle(step))
                    .accessibilityValue(Text(state))
                }
            }
        }
    }

    private func same(_ a: KnobSetupPhase, _ b: KnobSetupPhase) -> Bool {
        switch (a, b) {
        case (.joining, .joining): true
        default: a == b
        }
    }

    private func stepTitle(_ step: KnobSetupPhase) -> Text {
        switch step {
        case .saving: Text("Saving")
        case .restarting: Text("Restarting")
        case .joining(let ssid):
            Text("Joining \(ssid)", comment: "Knob setup progress step; the argument is the Wi-Fi network name.")
        case .reachingEmber: Text("Reaching Ember")
        case .done: Text("Done")
        }
    }
}

/// The setup errors from the spec, in words.
struct KnobSetupErrorText: View {
    let error: KnobSetupError

    var body: some View {
        switch error {
        case .notCinder:
            Text("This board isn't running cinder.")
        case .disconnected:
            Text("The knob was disconnected. Plug it back in and try again.")
        case .noHardwareID:
            Text("The knob didn't report its hardware ID. Update its firmware.")
        case .mint(let e):
            Text("Ember couldn't register the knob: \(Text(e.message))",
                 comment: "Knob setup error; the argument is a short reason (\"Unauthorized — check the token in Connection settings.\").")
        case .rejected("bad_url"):
            Text("The knob rejected the Ember address. Use http://address:port, with no path.")
        case .rejected(let code):
            Text("The knob rejected the settings (\(code)).",
                 comment: "Knob setup error; the argument is the knob's error code (\"too_long\").")
        case .wifi(let ssid):
            Text("Couldn't join \(ssid). Check the password; 5 GHz-only networks don't work.",
                 comment: "Knob setup error; the argument is the Wi-Fi network name.")
        case .improv(let code):
            Text("The knob reported Wi-Fi error \(Int(code)).",
                 comment: "Knob setup error; the argument is the Improv error code number.")
        case .emberUnreachable(let ip?, let url):
            Text("The knob is on Wi-Fi (\(ip)) but can't reach \(url).",
                 comment: "Knob setup error; the knob's IP address, then the Ember URL it tried.")
        case .emberUnreachable(nil, let url):
            Text("The knob is on Wi-Fi but can't reach \(url).",
                 comment: "Knob setup error; the argument is the Ember URL it tried.")
        case .emberUnauthorized:
            Text("Ember rejected the knob's token.")
        case .timedOut:
            Text("The knob stopped answering. Check that it's plugged in and try again.")
        case .invalid:
            Text("Use http://address:port for the Ember address, with no path.")
        }
    }
}
