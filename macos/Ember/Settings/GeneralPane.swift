import SwiftUI
import ServiceManagement
import EmberKit

/// This Mac's look and startup. Mac-local prefs, applied at once.
struct GeneralPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var login = LoginItemService.status
    @State private var loginError: String?
    @AppStorage("dashboard.openAtLaunch") private var openDashboardAtLaunch = false

    var body: some View {
        @Bindable var env = env
        Form {
            Section("Menu Bar") {
                Picker("Menu bar icon", selection: $env.prefs.trayStyle) {
                    Text("Animated bot").tag("bot")
                    Text("Tool glyphs").tag("glyphs")
                }
                Picker("Menu bar color", selection: $env.prefs.trayTint) {
                    Text("Colored").tag("color")
                    Text("Monochrome").tag("mono")
                }
                .pickerStyle(.segmented)
            }

            if env.prefs.trayStyle == "glyphs" {
                Section {
                    glyphPicker("Claude", $env.prefs.trayClaudeGlyph)
                    glyphPicker("Codex", $env.prefs.trayCodexGlyph)
                    glyphPicker("Idle or other", $env.prefs.trayIdleGlyph)
                } header: {
                    Text("Tool Glyphs")
                } footer: {
                    Text("The menu bar shows the glyph of the tool that's most active.")
                }
            }

            Section("Dock") {
                Picker("Dock icon", selection: $env.prefs.appIcon) {
                    ForEach(appIconPalettes, id: \.self) { p in
                        Label {
                            Text(appIconTitle(p))
                        } icon: {
                            if let img = p == "bot" ? BotAnimator.staticIcon(size: 36) : NSImage(named: "appicon-\(p)") {
                                Image(nsImage: img).resizable().frame(width: 16, height: 16)
                            }
                        }
                        .tag(p)
                    }
                }
            }

            Section {
                Toggle("Open Dashboard at launch", isOn: $openDashboardAtLaunch)
                Toggle("Launch at login", isOn: Binding(
                    get: { login == .enabled || login == .requiresApproval },
                    set: { on in
                        loginError = LoginItemService.setEnabled(on)
                        login = LoginItemService.status
                    }))
            } header: {
                Text("Startup")
            } footer: {
                VStack(alignment: .leading, spacing: 6) {
                    switch login {
                    case .requiresApproval:
                        Text("Ember needs your approval in System Settings › General › Login Items.")
                        Button("Open Login Items Settings…") {
                            SMAppService.openSystemSettingsLoginItems()
                        }
                    case .notFound:
                        Text("Ember can open at login only when it runs from the Applications folder.")
                    default:
                        EmptyView()
                    }
                    if let loginError {
                        Label(loginError, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .reloads { login = LoginItemService.status }
    }

    private func glyphPicker(_ label: LocalizedStringKey, _ binding: Binding<String>) -> some View {
        Picker(label, selection: binding) {
            ForEach(trayGlyphs, id: \.self) { g in
                Label {
                    Text(glyphTitle(g))
                } icon: {
                    if let img = NSImage(named: "tray-\(g)") {
                        Image(nsImage: img).resizable().renderingMode(.template).frame(width: 16, height: 16)
                    }
                }
                .tag(g)
            }
        }
    }

    private func appIconTitle(_ id: String) -> LocalizedStringKey {
        switch id {
        case "bot": "Bot (animated)"
        case "spark": "Spark"
        case "pixel-e": "Pixel E"
        default: LocalizedStringKey(id.capitalized)
        }
    }

    private func glyphTitle(_ id: String) -> LocalizedStringKey {
        switch id {
        case "ember": "Ember flame"
        case "ember-e": "Ember E"
        case "ember-e-pixel": "Ember E (pixel)"
        case "claude": "Claude"
        case "codex": "Codex"
        case "pomodoro": "Pomodoro"
        case "coffee": "Coffee"
        default: LocalizedStringKey(id.capitalized)
        }
    }
}
