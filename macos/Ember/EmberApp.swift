import SwiftUI
import AppKit
import EmberKit

@main
struct EmberApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    private var env: AppEnvironment { delegate.env }

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView()
                .environment(env)
                .capturesOpenWindow(into: env)
        } label: {
            MenuBarLabel(state: env.menuBarLabel)
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
                .frame(width: SettingsRootView.windowWidth)
                .frame(minHeight: 520)
                .windowMinimizeBehavior(.disabled)
                .environment(env)
                .capturesOpenWindow(into: env)
                .onAppear { env.isSettingsOpen = true }
                .onDisappear { env.isSettingsOpen = false }
        }
        .defaultSize(width: SettingsRootView.windowWidth, height: 640)
        .windowResizability(.contentSize)
        .defaultLaunchBehavior(.suppressed)
        .restorationBehavior(.disabled)
    }
}

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
                    NotificationCenter.default.post(name: .emberRefreshRequested, object: nil)
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

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let env = AppEnvironment()

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.accessory)
        BotAnimator.shared.activationPolicyDidChange(.accessory)
        let nc = NotificationCenter.default
        nc.addObserver(self, selector: #selector(syncPolicy),
                       name: NSWindow.didBecomeKeyNotification, object: nil)
        nc.addObserver(self, selector: #selector(syncPolicy),
                       name: NSWindow.willCloseNotification, object: nil)
    }

    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        DockMenu.make(env: env)
    }

    @objc private func syncPolicy() {
        DispatchQueue.main.async {
            let hasWindow = NSApp.windows.contains { w in
                w.isVisible && w.styleMask.contains(.titled) && !(w is NSPanel)
            }
            let wanted: NSApplication.ActivationPolicy = hasWindow ? .regular : .accessory
            if NSApp.activationPolicy() != wanted {
                NSApp.setActivationPolicy(wanted)
                BotAnimator.shared.activationPolicyDidChange(wanted)
                if wanted == .regular {
                    NSApp.activate()
                    AppEnvironment.applyAppIcon(AppEnvironment.loadPrefs().appIcon)
                }
            }
        }
    }
}
