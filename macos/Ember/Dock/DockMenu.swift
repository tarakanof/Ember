import AppKit
import EmberKit

@MainActor
enum DockMenu {
    static func make(env: AppEnvironment) -> NSMenu {
        let live = env.live
        let menu = NSMenu()
        menu.autoenablesItems = false

        let header = MenuRows.header(connection: live.connection, hasEverLoaded: live.snapshot.value != nil,
                                     winning: live.winningSession, offlineReason: live.snapshot.error)
        menu.addItem(textItem(String(localized: header.title)))
        menu.addItem(.separator())

        if let group = MenuRows.pomodoroControls(live.pomodoro, connection: live.connection) {
            if let status = MenuRows.pomodoroStatus(live.pomodoro.value) {
                menu.addItem(textItem(String(localized: status)))
            }
            for item in group.items {
                let menuItem = ActionMenuItem(title: String(localized: item.title)) {
                    Task { await env.actions.run(.pomodoro(item.action)) }
                }
                menuItem.image = NSImage(systemSymbolName: item.systemImage, accessibilityDescription: nil)
                menuItem.isEnabled = group.isEnabled && !env.actions.running.contains(.pomodoro(item.action))
                menu.addItem(menuItem)
            }
        }
        if let failure = env.actions.lastError {
            menu.addItem(textItem(String(localized: MenuRows.failure(failure.action, failure.error))))
        }
        if menu.items.last?.isSeparatorItem == false { menu.addItem(.separator()) }

        menu.addItem(ActionMenuItem(title: String(localized: "Open Dashboard")) {
            env.openWindow(id: WindowID.dashboard)
        })
        menu.addItem(ActionMenuItem(title: String(localized: "Settings…")) {
            env.openWindow(id: WindowID.settings)
        })
        return menu
    }

    private static func textItem(_ title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }
}

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
