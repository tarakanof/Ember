import SwiftUI
import EmberKit

/// The menu-bar dropdown, rendered as a native AppKit menu (`.menu` style).
/// Content is a flat list of menu items: the `Text` row becomes a dim, disabled
/// status header; the `Button`s become clickable items. Pomodoro controls live
/// in the Dashboard, not here.
struct MenuBarContentView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openWindow) private var openWindow
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        let model = env.model

        // Status header (dim).
        if let s = model.winningSession {
            Text("\(s.source) · \(s.tool) · \(s.state)")
        } else {
            Text(model.connected ? "Idle" : "Offline")
        }

        Divider()

        Button("Dashboard…") {
            NSApplication.shared.activate(ignoringOtherApps: true)
            openWindow(id: "dashboard")
        }
        // A plain SettingsLink does NOT activate this LSUIElement app, so the
        // Settings window opens behind/unfocused. Activate explicitly, then open.
        Button("Settings…") {
            NSApplication.shared.activate(ignoringOtherApps: true)
            openSettings()
        }
        .keyboardShortcut(",", modifiers: .command)

        Divider()

        Button("Quit Ember") { NSApplication.shared.terminate(nil) }
            .keyboardShortcut("q", modifiers: .command)
    }
}
