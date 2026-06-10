import SwiftUI
import EmberKit

struct AppTab: View {
    @Environment(AppEnvironment.self) private var env
    @State private var loginOn = LoginItemService.isEnabled
    @State private var loginError: String?
    @State private var serverVersion: String?

    private var appVersion: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "?"
        let build = info?["CFBundleVersion"] as? String ?? "?"
        return "\(short) (\(build))"
    }

    var body: some View {
        @Bindable var env = env
        Form {
            Section("Appearance") {
                Picker("Dock & app icon", selection: $env.prefs.appIcon) {
                    ForEach(appIconPalettes, id: \.self) { p in
                        HStack {
                            if let img = NSImage(named: "appicon-\(p)") {
                                Image(nsImage: img).resizable()
                                    .frame(width: 18, height: 18)
                                    .clipShape(RoundedRectangle(cornerRadius: 4))
                            }
                            Text(appIconDisplayName(p))
                        }.tag(p)
                    }
                }
                glyphPicker("Claude glyph", $env.prefs.trayClaudeGlyph)
                glyphPicker("Codex glyph", $env.prefs.trayCodexGlyph)
                glyphPicker("Idle / other glyph", $env.prefs.trayIdleGlyph)
            }

            Section("Startup") {
                Toggle("Launch at login", isOn: Binding(
                    get: { loginOn },
                    set: { newValue in
                        loginError = LoginItemService.setEnabled(newValue)
                        loginOn = LoginItemService.isEnabled
                    }))
                Text(LoginItemService.statusText)
                    .font(.caption).foregroundStyle(.secondary)
                if let loginError {
                    Text(loginError).font(.caption).foregroundStyle(.red)
                }
            }

            Section("About") {
                LabeledContent("App version") { Text(appVersion).foregroundStyle(.secondary) }
                LabeledContent("Server") { Text(serverVersion ?? "—").foregroundStyle(.secondary) }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("App")
        .task { serverVersion = await env.serverVersion() }
    }

    private func glyphPicker(_ label: String, _ binding: Binding<String>) -> some View {
        Picker(label, selection: binding) {
            ForEach(trayGlyphs, id: \.self) { g in
                HStack {
                    if let img = NSImage(named: "tray-\(g)") {
                        Image(nsImage: img).resizable().renderingMode(.template).frame(width: 16, height: 16)
                    }
                    Text(trayGlyphDisplayName(g))
                }.tag(g)
            }
        }
    }
}
