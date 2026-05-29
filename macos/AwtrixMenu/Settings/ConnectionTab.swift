import SwiftUI
import AwtrixMenuKit

struct ConnectionTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var source = ""
    @State private var serverURL = ""
    @State private var sourceColor = ""
    @State private var token = ""           // blank = keep current
    @State private var tokenIsSet = false
    @State private var saveError: String?
    @State private var testResult: String?
    @State private var loaded = false

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section("Producer") {
                    TextField("Source", text: $source)
                    TextField("Server URL", text: $serverURL)
                        .textContentType(.URL)
                    HStack {
                        TextField("Source color", text: $sourceColor, prompt: Text("#RRGGBB · blank = none"))
                        if let c = RGB(hex: sourceColor).map({ Color($0) }) {
                            RoundedRectangle(cornerRadius: 3).fill(c).frame(width: 18, height: 18)
                        }
                    }
                    SecureField("Token", text: $token,
                                prompt: Text(tokenIsSet ? "set — blank keeps it" : "not set"))
                }
            }
            .formStyle(.grouped)

            Divider()
            HStack(spacing: 12) {
                Button("Test Connection") { Task { await test() } }
                if let saveError {
                    Text(saveError).font(.caption).foregroundStyle(.red).lineLimit(1)
                } else if let testResult {
                    Text(testResult).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                Button("Save") { save() }.keyboardShortcut(.defaultAction)
            }
            .padding(12)
        }
        .task { if !loaded { load(); loaded = true } }
    }

    private func load() {
        let envFile = env.currentEnv()
        let c = ConnectionSettings(reading: envFile)
        source = c.source; serverURL = c.serverURL; sourceColor = c.sourceColor
        tokenIsSet = ConnectionSettings.tokenIsSet(in: envFile)
        token = ""
    }

    private func save() {
        saveError = nil; testResult = nil
        var envFile = env.currentEnv()
        let c = ConnectionSettings(source: source, serverURL: serverURL, sourceColor: sourceColor)
        do {
            try c.apply(to: &envFile, token: token)
            try envFile.write(to: env.producerEnvPath)
            env.reloadConnection()
            load()                    // refresh tokenIsSet / clear the secure field
            testResult = "Saved."
        } catch let e as ValidationError {
            saveError = e.message
        } catch {
            saveError = error.localizedDescription
        }
    }

    private func test() async {
        testResult = "Testing…"; saveError = nil
        let envFile = env.currentEnv()
        let effToken = token.trimmingCharacters(in: .whitespaces).isEmpty
            ? envFile.get(SettingsKeys.token) : token
        let url = serverURL.trimmingCharacters(in: .whitespaces)
        let client = APIClient(baseURL: url.isEmpty ? nil : URL(string: url),
                               token: effToken.isEmpty ? nil : effToken)
        do {
            let _: PomoConfig = try await client.get("/v1/pomodoro/config")
            testResult = "✓ Connected (auth OK)"
        } catch let e as APIError {
            switch e {
            case .notConfigured: testResult = "✗ No/invalid server URL"
            case .http(401, _):  testResult = "✗ Unauthorized — check token"
            case .http(let s, _): testResult = "✗ Server error (HTTP \(s))"
            case .transport:     testResult = "✗ Unreachable"
            case .decoding:      testResult = "✓ Reached server (unexpected body)"
            }
        } catch {
            testResult = "✗ \(error.localizedDescription)"
        }
    }
}
