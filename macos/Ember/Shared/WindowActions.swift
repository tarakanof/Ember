import SwiftUI
import EmberKit

/// Scene ids, so no caller types the string.
enum WindowID {
    static let dashboard = "dashboard"
    static let settings = "settings"
}

/// Opens (or raises) a window scene and makes Ember the active app with that
/// window key, in front of every other app.
///
/// Why it takes this many steps, for an `LSUIElement` app on macOS 26+:
/// 1. Promote to `.regular` first. An accessory app can't own the key window
///    of the frontmost app, and the Dock icon the delegate adds while a window
///    is open must already exist when activation is asked for.
/// 2. Defer the raise to a later runloop turn. From a `MenuBarExtra(.menu)`
///    item the action runs inside the menu's tracking loop; activating there
///    is undone when the menu closes and hands focus back, so the window came
///    up in front but inactive (grey traffic lights). The Dock menu path
///    didn't show it because AppKit had already activated Ember for the click.
/// 3. `NSApp.activate()` (cooperative) plus `makeKeyAndOrderFront` and
///    `orderFrontRegardless`, retried while SwiftUI builds the window.
/// 4. If the system still refused the cooperative request (the frontmost app
///    didn't yield), fall back to `activate(ignoringOtherApps:)`: deprecated
///    since macOS 14 but still honoured, and the user did ask for the window.
@MainActor
func presentWindow(id: String, using openWindow: OpenWindowAction) {
    if NSApp.activationPolicy() != .regular { NSApp.setActivationPolicy(.regular) }
    openWindow(id: id)
    raise(id: id, attempts: 10)
}

/// Makes Ember the active app for something the user just asked to see
/// (the About panel), from a menu action: deferred past the menu's close.
@MainActor
func activateForUser(then action: @escaping @MainActor () -> Void = {}) {
    DispatchQueue.main.async {
        MainActor.assumeIsolated {
            NSApp.activate()
            action()
            confirmActivation(of: nil)
        }
    }
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
            confirmActivation(of: window)
        }
    }
}

/// Step 4 above: a beat later, if Ember still isn't active (or `window`
/// isn't key), activate without waiting for the frontmost app to yield.
@MainActor
private func confirmActivation(of window: NSWindow?) {
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
        MainActor.assumeIsolated {
            guard !NSApp.isActive || (window.map { !$0.isKeyWindow } ?? false) else { return }
            // Called through a protocol so the deprecation doesn't warn: the
            // cooperative API has no forcing variant, and this is a direct
            // response to the user's click.
            (NSApp as LegacyActivation).activate(ignoringOtherApps: true)
            window?.makeKeyAndOrderFront(nil)
        }
    }
}

/// `NSApplication.activate(ignoringOtherApps:)` without the deprecation
/// warning at the call site (see `confirmActivation`).
@MainActor @objc private protocol LegacyActivation {
    @objc(activateIgnoringOtherApps:) func activate(ignoringOtherApps flag: Bool)
}

extension NSApplication: @MainActor LegacyActivation {}

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
