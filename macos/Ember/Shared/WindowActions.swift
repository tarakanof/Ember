import SwiftUI
import EmberKit

/// Scene ids, so no caller types the string.
enum WindowID {
    static let dashboard = "dashboard"
    static let settings = "settings"
}

/// Opens (or raises) a window scene and makes it the key window in front of
/// every other app. `openWindow` alone leaves an `LSUIElement` app's new
/// window behind the frontmost app: the app is still an accessory when the
/// request runs, so it's promoted first and the window is ordered front once
/// SwiftUI has created it.
@MainActor
func presentWindow(id: String, using openWindow: OpenWindowAction) {
    if NSApp.activationPolicy() != .regular { NSApp.setActivationPolicy(.regular) }
    NSApp.activate()
    openWindow(id: id)
    raise(id: id, attempts: 5)
}

/// Orders the scene's window front, retrying while SwiftUI builds it.
@MainActor
private func raise(id: String, attempts: Int) {
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) {
        MainActor.assumeIsolated {
            // SwiftUI names a Window scene's NSWindow "<id>-AppWindow-<n>".
            guard let window = NSApp.windows.first(where: { $0.identifier?.rawValue.hasPrefix(id) == true }) else {
                if attempts > 1 { raise(id: id, attempts: attempts - 1) }
                return
            }
            NSApp.activate()
            window.makeKeyAndOrderFront(nil)
            window.orderFrontRegardless()
        }
    }
}

/// Opens Settings on a pane by its raw name ("connection"). The pane is
/// handed over through the `settings.pane` default the Settings window
/// restores, so callers don't depend on the Settings types.
@MainActor
func openSettings(pane: String? = nil, using openWindow: OpenWindowAction) {
    if let pane { UserDefaults.standard.set(pane, forKey: "settings.pane") }
    presentWindow(id: WindowID.settings, using: openWindow)
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
