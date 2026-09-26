import SwiftUI
import EmberKit

/// Scene ids, so no caller types the string.
enum WindowID {
    static let dashboard = "dashboard"
    static let settings = "settings"
}

/// Opens Settings on a pane by its raw name ("connection"). The pane is
/// handed over through the `settings.pane` default the Settings window
/// restores, so callers don't depend on the Settings types.
@MainActor
func openSettings(pane: String? = nil, using openWindow: OpenWindowAction) {
    if let pane { UserDefaults.standard.set(pane, forKey: "settings.pane") }
    NSApp.activate()
    openWindow(id: WindowID.settings)
}

extension View {
    /// Hands the scene's `openWindow` to the environment for AppKit callers
    /// (the Dock menu). Applied to every window root and the menu.
    func capturesOpenWindow(into env: AppEnvironment) -> some View {
        modifier(CaptureOpenWindow(env: env))
    }
}

private struct CaptureOpenWindow: ViewModifier {
    let env: AppEnvironment
    @Environment(\.openWindow) private var openWindow

    func body(content: Content) -> some View {
        content.onAppear { env.openWindowAction = openWindow }
    }
}
