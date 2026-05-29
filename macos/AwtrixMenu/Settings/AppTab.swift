import SwiftUI
import AwtrixMenuKit

struct AppTab: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        @Bindable var env = env
        Form {
            Section("App icon") {
                Picker("Palette", selection: $env.prefs.appIcon) {
                    ForEach(appIconPalettes, id: \.self) { p in
                        HStack {
                            if let img = NSImage(named: "appicon-\(p)") {
                                Image(nsImage: img).resizable().frame(width: 18, height: 18)
                            }
                            Text(p)
                        }.tag(p)
                    }
                }
            }

            Section("Tray glyphs") {
                glyphPicker("Claude", $env.prefs.trayClaudeGlyph)
                glyphPicker("Codex", $env.prefs.trayCodexGlyph)
                glyphPicker("Idle / other", $env.prefs.trayIdleGlyph)
            }
        }
        .padding()
    }

    private func glyphPicker(_ label: String, _ binding: Binding<String>) -> some View {
        Picker(label, selection: binding) {
            ForEach(trayGlyphs, id: \.self) { g in
                HStack {
                    if let img = NSImage(named: "tray-\(g)") {
                        Image(nsImage: img).resizable().renderingMode(.template).frame(width: 16, height: 16)
                    }
                    Text(g)
                }.tag(g)
            }
        }
    }
}
