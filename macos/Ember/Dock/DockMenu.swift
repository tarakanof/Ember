import AppKit
import EmberKit

/// The Dock icon's menu (shown while a window keeps Ember in the Dock): the
/// Pomodoro controls that apply now, then Open Dashboard. Built fresh each
/// time the menu opens, from the same `PomodoroControls` as the menu bar.
@MainActor
enum DockMenu {
    static func make(env: AppEnvironment) -> NSMenu {
        let menu = NSMenu()
        menu.autoenablesItems = false
        let pomodoroOn = env.live.pomodoro.error != .featureOff
        if pomodoroOn {
            for item in PomodoroControls.items(for: env.live.pomodoro.value) {
                let menuItem = ActionMenuItem(title: String(localized: item.title)) {
                    Task { await env.actions.run(.pomodoro(item.action)) }
                }
                menuItem.image = NSImage(systemSymbolName: item.systemImage, accessibilityDescription: nil)
                menuItem.isEnabled = env.live.connection.isOnline
                menu.addItem(menuItem)
            }
            menu.addItem(.separator())
        }
        menu.addItem(ActionMenuItem(title: String(localized: "Open Dashboard")) {
            env.openWindow(id: WindowID.dashboard)
        })
        return menu
    }
}

/// An `NSMenuItem` that runs a closure.
@MainActor
private final class ActionMenuItem: NSMenuItem {
    private let handler: @MainActor () -> Void

    init(title: String, handler: @escaping @MainActor () -> Void) {
        self.handler = handler
        super.init(title: title, action: #selector(fire), keyEquivalent: "")
        target = self
    }

    @available(*, unavailable)
    required init(coder: NSCoder) { fatalError("init(coder:) is not used") }

    @objc private func fire() { handler() }
}
