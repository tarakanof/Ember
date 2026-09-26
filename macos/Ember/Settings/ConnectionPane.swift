import SwiftUI
import AppKit
import EmberKit

/// The server this Mac talks to and how it identifies itself. Stored in
/// producer.env (shared with the producers) through `env.settings.connectionEnv`
/// and `env.envStore`.
struct ConnectionPane: View {
    @Environment(AppEnvironment.self) private var env

    // Source and Server URL commit on Return or when the field loses focus:
    // saving a half-typed URL would repoint every model at a wrong server.
    @State private var source = ""
    @State private var serverURL = ""
    @State private var token = ""
    @State private var tokenIsSet = false
    @State private var tokenError: String?
    @State private var tokenSaved = false
    @State private var probe: ConnectionProbe.Result?
    @State private var probing = false

    private enum Field: Hashable { case source, serverURL, token }
    @FocusState private var focused: Field?

    private var model: EnvConfigModel<ConnectionSettings> { env.settings.connectionEnv }

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                TextField("Server URL", text: $serverURL, prompt: Text(verbatim: "http://192.168.0.2:3627"))
                    .textContentType(.URL)
                    .focused($focused, equals: .serverURL)
                    .onSubmit { commit(\.serverURL, serverURL) }
                LabeledContent("Status") {
                    HStack(spacing: 10) {
                        statusLabel
                            .lineLimit(1)
                            .fixedSize()
                        Button("Test Connection") { Task { await runProbe() } }
                            .disabled(probing)
                    }
                }
            } header: {
                Text("Server")
            } footer: {
                SaveErrorFooter(error: model.saveError)
            }

            discoveredSection

            Section {
                TextField("Source", text: $source, prompt: Text(verbatim: "m4"))
                    .focused($focused, equals: .source)
                    .onSubmit { commit(\.source, source) }
                Toggle("Use source color", isOn: Binding(
                    get: { !model.draft.sourceColor.isEmpty },
                    set: { model.draft.sourceColor = $0 ? "#FF8800" : "" }))
                if !model.draft.sourceColor.isEmpty {
                    HexColorRow(title: "Source color", hex: $model.draft.sourceColor, fallback: "#FF8800")
                }
                LabeledContent("Token") {
                    HStack {
                        SecureField("Token", text: $token,
                                    prompt: Text(tokenIsSet ? "Saved — leave blank to keep" : "Not set"))
                            .labelsHidden()
                            .focused($focused, equals: .token)
                            .onSubmit { Task { await saveToken() } }
                        Button("Save Token") { Task { await saveToken() } }
                            .disabled(tokenIsBlank)
                    }
                }
            } header: {
                Text("This Mac")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("The source names this Mac on the clock; up to 4 characters fit best. The token must match the server's EMBER_TOKEN.")
                    if let tokenError {
                        Label(tokenError, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                    } else if tokenSaved {
                        Label("Token saved", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .disabled(!model.isLoaded)
        .autosaves(model)
        .onChange(of: focused) { old, _ in commitOnFocusLeave(old) }
        // Leaving the pane or closing the window tears the view down without
        // a focus change; don't lose a typed source or URL. The token is only
        // ever saved by Save Token or Return.
        .onDisappear {
            commitOnFocusLeave(.source)
            commitOnFocusLeave(.serverURL)
        }
        .onChange(of: model.applied) { _, applied in
            guard let applied else { return }
            if focused != .source { source = applied.source }
            if focused != .serverURL { serverURL = applied.serverURL }
        }
        .onChange(of: env.serverURL) { _, _ in Task { await runProbe() } }
        .reloads {
            await model.load()
            if let applied = model.applied {
                source = applied.source
                serverURL = applied.serverURL
            }
            tokenIsSet = ConnectionSettings.tokenIsSet(in: await env.envStore.read())
            await runProbe()
        }
    }

    // MARK: Status

    @ViewBuilder private var statusLabel: some View {
        if probing {
            ProgressView().controlSize(.small)
        } else {
            switch probe {
            case .connected(let version?):
                Label { Text("Connected · \(version)") } icon: {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                }
            case .connected(nil):
                Label { Text("Connected") } icon: {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                }
            case .notConfigured:
                Label { Text("No server URL") } icon: {
                    Image(systemName: "questionmark.circle").foregroundStyle(.secondary)
                }
            case .unauthorized:
                Label { Text("Token rejected") } icon: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                }
            case .rateLimited:
                Label { Text("Rate-limited — reachable, token not checked") } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
            case .unreachable:
                Label { Text("Unreachable") } icon: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                }
            case .serverError(let status):
                Label { Text("Server error (HTTP \(status))") } icon: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                }
            case nil:
                Text(verbatim: "—").foregroundStyle(.secondary)
            }
        }
    }

    private func runProbe() async {
        probing = true
        defer { probing = false }
        let file = await env.envStore.read()
        probe = await ConnectionProbe.run(APIClient(producerEnv: file))
    }

    // MARK: Discovered servers

    @ViewBuilder private var discoveredSection: some View {
        Section {
            if env.serverDiscovery.servers.isEmpty {
                switch env.serverDiscovery.status {
                case .needsAccess:
                    Label("Local Network access is off for Ember", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                case .unavailable:
                    Label("Network discovery is unavailable", systemImage: "wifi.exclamationmark")
                        .foregroundStyle(.secondary)
                case .searching:
                    LabeledContent {
                        ProgressView().controlSize(.small)
                    } label: {
                        Text("Searching the local network…").foregroundStyle(.secondary)
                    }
                }
                LabeledContent {
                    Button("Grant Local Network Access…") {
                        openSystemSettings("x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork")
                    }
                } label: {
                    Text("No server found?")
                }
            } else {
                ForEach(env.serverDiscovery.servers) { s in
                    LabeledContent {
                        if s.urlString == model.applied?.serverURL {
                            Image(systemName: "checkmark")
                                .foregroundStyle(.tint)
                                .accessibilityLabel("In use")
                        } else {
                            Button("Use") {
                                serverURL = s.urlString
                                commit(\.serverURL, s.urlString)
                            }
                        }
                    } label: {
                        Text(s.name)
                        Text(verbatim: "\(s.host):\(s.port)")
                    }
                }
            }
        } header: {
            HStack {
                Text("Discovered Servers")
                Spacer()
                Button("Rescan") { env.serverDiscovery.restart() }
                    .buttonStyle(.link)
                    .font(.caption)
            }
        } footer: {
            Text("Found via Bonjour. Click Use to fill in the server URL. The server must run on host networking, and Ember needs Local Network access.")
        }
    }

    // MARK: Saving

    private var tokenIsBlank: Bool { token.trimmingCharacters(in: .whitespaces).isEmpty }

    private func commitOnFocusLeave(_ field: Field?) {
        switch field {
        case .source: commit(\.source, source)
        case .serverURL: commit(\.serverURL, serverURL)
        case .token, .none: break
        }
    }

    private func commit(_ key: WritableKeyPath<ConnectionSettings, String>, _ value: String) {
        guard model.isLoaded, model.draft[keyPath: key] != value else { return }
        model.draft[keyPath: key] = value
        Task { await model.saveNow() }
    }

    /// The token is saved on its own and only on request: it's a secret and a
    /// partly typed value must never reach producer.env.
    private func saveToken() async {
        guard !tokenIsBlank else { return }
        let value = token
        do {
            try await env.envStore.update { file in
                try ConnectionSettings(reading: file).applyTolerant(to: &file, token: value)
            }
            token = ""
            tokenIsSet = true
            tokenError = nil
            tokenSaved = true
            env.reloadConnection()
            await runProbe()
        } catch let e as ValidationError {
            tokenError = String(localized: e.message)
        } catch {
            tokenError = error.localizedDescription
        }
    }
}
