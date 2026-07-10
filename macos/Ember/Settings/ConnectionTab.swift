import SwiftUI
import AppKit
import EmberKit

struct ConnectionTab: View {
    @Environment(AppEnvironment.self) private var env

    // Live editable fields.
    @State private var source = ""
    @State private var serverURL = ""
    @State private var useSourceColor = false
    @State private var sourceColorHex = "#FF8800"
    @State private var token = ""            // blank = keep current
    @State private var tokenIsSet = false

    // Last-committed-valid snapshot of the env-backed fields. All writes apply
    // from THIS (mutating one field), so a half-typed invalid field can't drop
    // another field's value through ConnectionSettings' all-or-nothing apply.
    @State private var committed = ConnectionSettings(source: "", serverURL: "", sourceColor: "")
    @State private var save: SaveState = .idle
    @State private var testResult: String?
    @State private var loaded = false
    @State private var serverVersion: String?

    private enum Field: Hashable { case source, serverURL, token }
    @FocusState private var focusedField: Field?

    var body: some View {
        Form {
            Section {
                TextField("Source", text: $source)
                    .focused($focusedField, equals: .source)
                    .onSubmit { commit { $0.source = source } }
                Text("Short source names (≤ 4 chars) display best on the clock.")
                    .font(.caption).foregroundStyle(.secondary)
                TextField("Server URL", text: $serverURL)
                    .textContentType(.URL)
                    .focused($focusedField, equals: .serverURL)
                    .onSubmit { commit { $0.serverURL = serverURL } }

                Toggle("Use source color", isOn: $useSourceColor)
                    .disabled(!connectionConfigured)
                    .onChange(of: useSourceColor) { _, on in
                        commit { $0.sourceColor = on ? sourceColorHex : "" }
                    }
                if useSourceColor {
                    ColorHexPicker(title: "Source color", hex: $sourceColorHex)
                        .disabled(!connectionConfigured)
                        .onChange(of: sourceColorHex) { _, hex in
                            commit { $0.sourceColor = hex }
                        }
                }

                SecureField("Token", text: $token,
                            prompt: Text(tokenIsSet ? "set — blank keeps it" : "not set"))
                    .focused($focusedField, equals: .token)
                    .onSubmit { commitToken() }
            } header: {
                Text("Producer")
            } footer: {
                statusCaption
            }

            Section {
                LabeledContent("Server version") {
                    Text(serverVersion ?? "—").foregroundStyle(.secondary)
                }
            } header: {
                Text("Server")
            } footer: {
                Text("Build reported by the connected server (GET /version). Shows “—” while unreachable.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                if !env.serverDiscovery.servers.isEmpty {
                    ForEach(env.serverDiscovery.servers) { s in
                        Button {
                            serverURL = s.urlString
                            commit { $0.serverURL = s.urlString }
                        } label: {
                            LabeledContent(s.name) {
                                Text(s.urlString).foregroundStyle(.secondary)
                            }
                        }
                        .buttonStyle(.plain)
                    }
                } else {
                    switch env.serverDiscovery.status {
                    case .needsAccess:
                        Label("Local Network access needed", systemImage: "exclamationmark.triangle.fill")
                            .font(.caption).foregroundStyle(.orange)
                    case .unavailable:
                        Label("Network discovery unavailable", systemImage: "wifi.exclamationmark")
                            .font(.caption).foregroundStyle(.secondary)
                    case .searching:
                        Label("Searching the local network…", systemImage: "antenna.radiowaves.left.and.right")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    // macOS doesn't reliably signal a denied Local Network grant, so
                    // whenever nothing is found, surface the grant path explicitly.
                    Text("No server found? Ember needs Local Network access to discover one via Bonjour.")
                        .font(.caption2).foregroundStyle(.secondary)
                    Button {
                        openLocalNetworkSettings()
                    } label: {
                        Label("Grant Local Network Access…", systemImage: "lock.shield")
                    }
                }
                Button("Rescan") { env.serverDiscovery.restart() }
            } header: {
                Text("Discovered servers")
            } footer: {
                Text("Ember servers found on your network via Bonjour. Tap one to fill the Server URL. Requires the server on host networking and Local Network access for Ember.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .onChange(of: focusedField) { old, _ in commitOnFocusLeave(old) }
        .toolbar {
            ToolbarItem {
                Button("Test Connection") { Task { await test() } }
            }
        }
        .task { if !loaded { load(); loaded = true } }
        .task(id: committed.serverURL) { serverVersion = await env.serverVersion() }
    }

    /// A source-color tint can only be written once Source + Server URL are set,
    /// because `ConnectionSettings.apply` validates every field together. Until
    /// then the colour controls are disabled (rather than throwing a confusing
    /// "source must not be empty" on a colour action).
    private var connectionConfigured: Bool { committed.isComplete }

    @ViewBuilder private var statusCaption: some View {
        switch save {
        case .idle:
            if !connectionConfigured {
                Text("Set Source and Server URL first to enable a colour tint.")
                    .font(.caption).foregroundStyle(.secondary)
            } else if let testResult {
                Text(testResult).font(.caption).foregroundStyle(.secondary)
            }
        case .saving: Text("Saving…").font(.caption).foregroundStyle(.secondary)
        case .saved:  Label("Saved", systemImage: "checkmark.circle")
                        .font(.caption).foregroundStyle(.secondary)
        case .error(let m): Label(m, systemImage: "exclamationmark.triangle")
                        .font(.caption).foregroundStyle(.red)
        }
    }

    private func load() {
        let envFile = env.currentEnv()
        committed = ConnectionSettings(reading: envFile)
        source = committed.source
        serverURL = committed.serverURL
        useSourceColor = !committed.sourceColor.isEmpty
        if useSourceColor { sourceColorHex = committed.sourceColor }
        tokenIsSet = ConnectionSettings.tokenIsSet(in: envFile)
        token = ""
    }

    /// Commits whichever field just lost focus, so an edit is saved even if the
    /// user tabs/clicks away without pressing Return. Idempotent with `.onSubmit`
    /// (the `commit`/`commitToken` no-op guards swallow the duplicate).
    private func commitOnFocusLeave(_ field: Field?) {
        switch field {
        case .source:    commit { $0.source = source }
        case .serverURL: commit { $0.serverURL = serverURL }
        case .token:     commitToken()
        case .none:      break
        }
    }

    /// Applies a one-field mutation of the committed snapshot. Uses the tolerant
    /// apply so a fresh install can be configured one field at a time in any order:
    /// a still-empty required sibling (e.g. Source while the user is filling the
    /// Server URL) is not treated as an error. The one field being changed is still
    /// validated, so a genuinely bad value (e.g. a malformed URL) shows the red
    /// error; the OTHER fields come from the known-valid committed snapshot.
    private func commit(_ mutate: (inout ConnectionSettings) -> Void) {
        var next = committed
        mutate(&next)
        guard next != committed else { return }   // no-op (e.g. Return then blur): skip the write
        var envFile = env.currentEnv()
        do {
            try next.applyTolerant(to: &envFile, token: nil)
            try envFile.write(to: env.producerEnvPath)
            committed = next
            env.reloadConnection()
            save = .saved
        } catch let e as ValidationError {
            save = .error(e.message)
        } catch {
            save = .error(error.localizedDescription)
        }
    }

    private func commitToken() {
        guard !token.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        var envFile = env.currentEnv()
        do {
            // Tolerant apply so a token can be saved on a fresh install before the
            // Source / Server URL fields have been filled in.
            try committed.applyTolerant(to: &envFile, token: token)
            try envFile.write(to: env.producerEnvPath)
            env.reloadConnection()
            token = ""; tokenIsSet = true
            save = .saved
        } catch let e as ValidationError {
            save = .error(e.message)
        } catch {
            save = .error(error.localizedDescription)
        }
    }

    private func test() async {
        testResult = "Testing…"; save = .idle
        let envFile = env.currentEnv()
        let effToken = token.trimmingCharacters(in: .whitespaces).isEmpty
            ? envFile.get(SettingsKeys.token) : token
        let url = serverURL.trimmingCharacters(in: .whitespaces)
        let client = APIClient(baseURL: url.isEmpty ? nil : URL(string: url),
                               token: effToken.isEmpty ? nil : effToken)
        do {
            // Probe an always-mounted, auth-required endpoint. (Pomodoro's
            // config route is only registered when the feature is enabled, so
            // it 404s on a perfectly healthy connection when Pomodoro is off.)
            try await client.send("GET", "/v1/apps")
            testResult = "✓ Connected (auth OK)"
        } catch let e as APIError {
            switch e {
            case .notConfigured:  testResult = "✗ No/invalid server URL"
            case .http(401, _):   testResult = "✗ Unauthorized — check token"
            case .http(let s, _): testResult = "✗ Server error (HTTP \(s))"
            case .transport:      testResult = "✗ Unreachable"
            case .decoding:       testResult = "✓ Reached server (unexpected body)"
            }
        } catch {
            testResult = "✗ \(error.localizedDescription)"
        }
    }

    /// Opens System Settings at the Local Network privacy pane so the user can
    /// enable Ember (needed for Bonjour server discovery).
    private func openLocalNetworkSettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork") {
            NSWorkspace.shared.open(url)
        }
    }
}
