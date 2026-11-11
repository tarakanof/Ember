import SwiftUI
import AppKit
import ServiceManagement
import EmberKit

struct ConnectionPane: View {
    @Environment(AppEnvironment.self) private var env

    @State private var source = ""
    @State private var serverURL = ""
    @State private var token = ""
    @State private var tokenIsSet = false
    @State private var tokenError: String?
    @State private var tokenSaved = false
    @State private var probe: ConnectionProbe.Result?
    @State private var probing = false
    @State private var stoppedHere = false

    private enum Field: Hashable { case source, serverURL, token }
    @FocusState private var focused: Field?

    private var model: EnvConfigModel<ConnectionSettings> { env.settings.connectionEnv }
    private var server: BuiltInServerModel { env.builtInServer }
    private var runsHere: Bool { server.mode == .thisMac }

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                Picker("Server", selection: Binding(
                    get: { server.mode },
                    set: { next in
                        stoppedHere = next == .anotherServer
                        Task { await server.select(next) }
                    })) {
                    Text("Run on this Mac").tag(ServerMode.thisMac)
                    Text("Use another server").tag(ServerMode.anotherServer)
                }
                .pickerStyle(.radioGroup)
                .disabled(!server.isLoaded || server.isWorking)
                if runsHere {
                    builtInRows
                } else {
                    TextField("Server URL", text: $serverURL, prompt: Text(verbatim: "http://ember.local:3627"))
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
                }
            } header: {
                Text("Server")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    if runsHere {
                        Text("The clock, a knob and other Macs reach this Mac on port \(String(BuiltInServer.port)).",
                             comment: "Settings › Connection footer while the built-in server runs; the port number.")
                    } else if stoppedHere {
                        Text("The clock returns to its own apps within about 30 seconds.")
                    }
                    if let error = server.error {
                        Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                    }
                    SaveErrorFooter(error: model.saveError)
                }
            }

            if !runsHere { discoveredSection }

            Section {
                TextField("Source", text: $source, prompt: Text(verbatim: "mac"))
                    .focused($focused, equals: .source)
                    .onSubmit { commit(\.source, source) }
                InfoToggle("Use source color", isOn: Binding(
                    get: { !model.draft.sourceColor.isEmpty },
                    set: { model.draft.sourceColor = $0 ? "#FF8800" : "" }),
                    info: .sourceColor, requirement: .loading)
                if !model.draft.sourceColor.isEmpty {
                    HexColorRow(title: "Source color", hex: $model.draft.sourceColor, fallback: "#FF8800")
                }
                if !runsHere {
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
                }
            } header: {
                Text("This Mac")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    if runsHere {
                        Text("The source names this Mac on the clock; up to 4 characters fit best.")
                    } else {
                        Text("The source names this Mac on the clock; up to 4 characters fit best. The token must match the server's EMBER_TOKEN.")
                    }
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
        .task { await env.serverDiscovery.browse() }
        .task(id: server.mode) {
            guard server.mode == .thisMac else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                await server.refresh()
            }
        }
        .reloads {
            await server.refresh()
            await model.load()
            if let applied = model.applied {
                source = applied.source
                serverURL = applied.serverURL
            }
            tokenIsSet = ConnectionSettings.tokenIsSet(in: await env.envStore.read())
            await runProbe()
        }
    }

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
            case .timedOut:
                Label { Text("Not responding", comment: "Connection status: the server didn't answer in time.") } icon: {
                    Image(systemName: "clock.badge.exclamationmark").foregroundStyle(.orange)
                }
            case .localNetworkDenied:
                HStack(spacing: 8) {
                    Label { Text("Local Network access off") } icon: {
                        Image(systemName: "wifi.exclamationmark").foregroundStyle(.orange)
                    }
                    Button("Review Permissions…") { showSettings(.app(.permissions)) }
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

    @ViewBuilder private var builtInRows: some View {
        LabeledContent("Status") {
            builtInStatus
                .lineLimit(1)
                .fixedSize()
        }
        if env.serverDiscovery.status == .needsAccess {
            LabeledContent {
                Button("Review Permissions…") { showSettings(.app(.permissions)) }
            } label: {
                Label { Text("Local Network access off") } icon: {
                    Image(systemName: "wifi.exclamationmark").foregroundStyle(.orange)
                }
            }
        }
        LabeledContent {
            HStack(spacing: 10) {
                Button("Show Log") { open(server.paths.log) }
                Button("Show Data Folder") { open(server.paths.dataDir) }
            }
        } label: {
            Text("Files")
        }
    }

    @ViewBuilder private var builtInStatus: some View {
        switch server.status {
        case .off:
            Text("Off").foregroundStyle(.secondary)
        case .starting:
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text("Starting…")
            }
        case .running(let version?):
            Label { Text("Running · \(version)", comment: "Settings › Connection built-in server status; the server's version.") } icon: {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            }
        case .running(nil):
            Label { Text("Running") } icon: {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            }
        case .notRunning:
            HStack(spacing: 8) {
                Label { Text("Not running") } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
                Button("Start") { Task { await server.start() } }
                    .disabled(server.isWorking)
            }
        case .portInUse:
            Label { Text("Port \(String(BuiltInServer.port)) is in use by another program",
                         comment: "Settings › Connection built-in server status when something else listens on its port; the port number.") } icon: {
                Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
            }
        case .needsApproval:
            HStack(spacing: 8) {
                Label { Text("Needs approval in System Settings › Login Items") } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
                Button("Open Login Items…") { SMAppService.openSystemSettingsLoginItems() }
            }
        case .missing:
            Label { Text("The app is missing the server's launch agent.") } icon: {
                Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
            }
        }
    }

    private func open(_ url: URL) {
        let target = FileManager.default.fileExists(atPath: url.path) ? url : url.deletingLastPathComponent()
        NSWorkspace.shared.open(target)
    }

    private func runProbe() async {
        probing = true
        defer { probing = false }
        let file = await env.envStore.read()
        probe = await ConnectionProbe.run(APIClient(producerEnv: file))
    }

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
                    Button("Review Permissions…") { showSettings(.app(.permissions)) }
                } label: {
                    Text("No server found?")
                }
            } else {
                if needsServerChoice {
                    Label("Several Ember servers found. Pick one with Use.", systemImage: "questionmark.circle.fill")
                        .foregroundStyle(.orange)
                }
                ForEach(env.serverDiscovery.servers) { s in
                    LabeledContent {
                        if isInUse(s) {
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

    private var needsServerChoice: Bool {
        let configured = (model.applied?.serverURL ?? serverURL).trimmingCharacters(in: .whitespaces)
        guard configured.isEmpty || configured.lowercased() == "auto", env.serverDiscovery.servers.count > 1 else { return false }
        guard let cached = cachedServerURL else { return true }
        return !env.serverDiscovery.servers.contains { $0.urlString == cached }
    }

    private var cachedServerURL: String? {
        ProducerServerCache.url(home: FileManager.default.homeDirectoryForCurrentUser)
    }

    private func isInUse(_ s: ServerDiscovery.Found) -> Bool {
        let configured = (model.applied?.serverURL ?? "").trimmingCharacters(in: .whitespaces)
        if configured.isEmpty || configured.lowercased() == "auto" { return s.urlString == cachedServerURL }
        return s.urlString == configured
    }

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
