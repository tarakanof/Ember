import SwiftUI
import AwtrixMenuKit

@main
struct AwtrixMenuApp: App {
    @State private var env = AppEnvironment()   // self-starts polling in its init

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView()
                .environment(env)
        } label: {
            MenuBarLabel(session: env.model.winningSession, prefs: .default)
        }
        .menuBarExtraStyle(.window)

        Settings {
            SettingsView()
                .environment(env)
        }
    }
}
