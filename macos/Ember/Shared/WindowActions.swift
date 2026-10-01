import SwiftUI
import EmberKit

enum WindowID {
    static let dashboard = "dashboard"
    static let settings = "settings"
}

@MainActor
func presentWindow(id: String, using openWindow: OpenWindowAction) {
    if NSApp.activationPolicy() != .regular {
        NSApp.setActivationPolicy(.regular)
        BotAnimator.shared.activationPolicyDidChange(.regular)
        AppEnvironment.applyAppIcon(AppEnvironment.loadPrefs().appIcon)
    }
    openWindow(id: id)
    raise(id: id, attempts: 10)
}

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

@MainActor
private func raise(id: String, attempts: Int) {
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) {
        MainActor.assumeIsolated {
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

@MainActor
private func confirmActivation(of window: NSWindow?) {
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
        MainActor.assumeIsolated {
            guard !NSApp.isActive || (window.map { !$0.isKeyWindow } ?? false) else { return }
            (NSApp as LegacyActivation).activate(ignoringOtherApps: true)
            window?.makeKeyAndOrderFront(nil)
        }
    }
}

@MainActor @objc private protocol LegacyActivation {
    @objc(activateIgnoringOtherApps:) func activate(ignoringOtherApps flag: Bool)
}

extension NSApplication: @MainActor LegacyActivation {}

@MainActor
func openSettings(pane: String? = nil, using openWindow: OpenWindowAction) {
    if let pane { UserDefaults.standard.set(pane, forKey: "settings.pane") }
    presentWindow(id: WindowID.settings, using: openWindow)
}

extension View {
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
