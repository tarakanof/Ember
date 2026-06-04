import SwiftUI
import EmberKit

struct AppTab: View {
    @Environment(AppEnvironment.self) private var env
    @State private var loginOn = LoginItemService.isEnabled
    @State private var loginError: String?

    var body: some View {
        @Bindable var env = env
        Form {
            Section("App icon") {
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
            }

            Section("Tray glyphs") {
                glyphPicker("Claude", $env.prefs.trayClaudeGlyph)
                glyphPicker("Codex", $env.prefs.trayCodexGlyph)
                glyphPicker("Idle / other", $env.prefs.trayIdleGlyph)
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
        }
        .formStyle(.grouped)
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
