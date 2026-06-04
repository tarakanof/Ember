import SwiftUI
import AppKit
import EmberKit

@main
struct EmberApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var env = AppEnvironment()   // self-starts polling in its init

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView()
                .environment(env)
        } label: {
            MenuBarLabel(session: env.model.winningSession, prefs: env.prefs)
        }
        .menuBarExtraStyle(.window)

        Settings {
            SettingsView()
                .environment(env)
        }

        Window("Dashboard", id: "dashboard") {
            DashboardView()
                .environment(env)
        }
    }
}

/// Ember is a menu-bar agent (`LSUIElement`), so it normally has no Dock icon.
/// We promote it to a regular app (Dock icon + app menu) only while a real
/// window — Settings or the Dashboard — is on screen, and demote it back to an
/// accessory when the last one closes. The Ember icon shown in the Dock is the
/// runtime `applicationIconImage` set by `AppEnvironment.applyAppIcon`.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.accessory)
        let nc = NotificationCenter.default
        nc.addObserver(self, selector: #selector(syncPolicy),
                       name: NSWindow.didBecomeKeyNotification, object: nil)
        nc.addObserver(self, selector: #selector(syncPolicy),
                       name: NSWindow.willCloseNotification, object: nil)
    }

    /// A window "counts" toward Dock presence only if it's a visible, titled
    /// window — i.e. Settings or the Dashboard. The borderless MenuBarExtra panel
    /// and the status-bar window are untitled, so clicking the menu bar never
    /// summons a Dock icon. Deferred to the next runloop tick so `isVisible` is
    /// already updated when this fires from `willClose`.
    @objc private func syncPolicy() {
        DispatchQueue.main.async {
            let hasWindow = NSApp.windows.contains { w in
                w.isVisible && w.styleMask.contains(.titled) && !(w is NSPanel)
            }
            let wanted: NSApplication.ActivationPolicy = hasWindow ? .regular : .accessory
            if NSApp.activationPolicy() != wanted {
                NSApp.setActivationPolicy(wanted)
                if wanted == .regular { NSApp.activate(ignoringOtherApps: true) }
            }
        }
    }
}
