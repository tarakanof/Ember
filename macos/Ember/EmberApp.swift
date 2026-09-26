import SwiftUI
import AppKit
import EmberKit

@main
struct EmberApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    /// Owned by the delegate so AppKit callbacks (the Dock menu) reach it.
    private var env: AppEnvironment { delegate.env }

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView()
                .environment(env)
                .capturesOpenWindow(into: env)
        } label: {
            MenuBarLabel(session: env.live.winningSession, connection: env.live.connection, prefs: env.prefs)
        }
        .menuBarExtraStyle(.menu)
        .commands { EmberCommands(env: env) }

        Window("Ember", id: WindowID.dashboard) {
            DashboardWindow()
                .environment(env)
                .capturesOpenWindow(into: env)
        }
        .defaultSize(width: 980, height: 720)
        .windowResizability(.contentMinSize)
        .defaultLaunchBehavior(UserDefaults.standard.bool(forKey: "dashboard.openAtLaunch") ? .presented : .suppressed)
        .restorationBehavior(.disabled)

        Window("Settings", id: WindowID.settings) {
            SettingsRootView()
                .frame(minWidth: 720, minHeight: 520)
                .windowMinimizeBehavior(.disabled)
                .environment(env)
                .capturesOpenWindow(into: env)
                .onAppear { env.isSettingsOpen = true }
                .onDisappear { env.isSettingsOpen = false }
        }
        .defaultSize(width: 800, height: 640)
        .windowResizability(.contentMinSize)
        .defaultLaunchBehavior(.suppressed)
        .restorationBehavior(.disabled)
    }
}

/// App-menu commands: ⌘, Settings (the classic `Settings` scene doesn't suit
/// an `LSUIElement` app), ⌘0 Dashboard, ⌘R refresh. Views inside a
/// `CommandGroup` pick up the scene's `openWindow`.
private struct EmberCommands: Commands {
    let env: AppEnvironment

    var body: some Commands {
        CommandGroup(replacing: .appSettings) { OpenWindowCommand(title: "Settings…", id: WindowID.settings, key: ",") }
        CommandGroup(before: .windowArrangement) {
            OpenWindowCommand(title: "Open Dashboard", id: WindowID.dashboard, key: "0")
            Divider()
        }
        CommandGroup(after: .toolbar) {
            Button("Refresh") {
                Task {
                    await env.live.refreshNow()
                    if env.isSettingsOpen {
                        await env.settings.loadAll()
                        await env.deviceSettings.load(force: true)
                    }
                }
            }
            .keyboardShortcut("r", modifiers: .command)
        }
    }
}

private struct OpenWindowCommand: View {
    let title: LocalizedStringKey
    let id: String
    let key: KeyEquivalent
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Button(title) { presentWindow(id: id, using: openWindow) }
        .keyboardShortcut(key, modifiers: .command)
    }
}

/// Ember is a menu-bar agent (`LSUIElement`), so it normally has no Dock icon.
/// We promote it to a regular app (Dock icon + app menu) only while a real
/// window — Settings or the Dashboard — is on screen, and demote it back to an
/// accessory when the last one closes. The Ember icon shown in the Dock is the
/// runtime `applicationIconImage` set by `AppEnvironment.applyAppIcon`.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let env = AppEnvironment()

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.accessory)
        let nc = NotificationCenter.default
        nc.addObserver(self, selector: #selector(syncPolicy),
                       name: NSWindow.didBecomeKeyNotification, object: nil)
        nc.addObserver(self, selector: #selector(syncPolicy),
                       name: NSWindow.willCloseNotification, object: nil)
    }

    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        DockMenu.make(env: env)
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
                if wanted == .regular {
                    NSApp.activate()
                    // Promoting to .regular makes the Dock fall back to the
                    // bundle's static AppIcon; the runtime icon set at launch
                    // (while still an accessory) is lost. Re-apply it.
                    AppEnvironment.applyAppIcon(AppEnvironment.loadPrefs().appIcon)
                }
            }
        }
    }
}
