import SwiftUI
import EmberKit

/// The menu-bar menu (`.menu` style, so every row becomes an `NSMenuItem`):
/// glance rows, Pomodoro, the Clock submenu, then Dashboard/Settings/About/
/// Quit. Row rules live in `MenuRows` (EmberKit, unit-tested); this view only
/// lays them out. It reads `LiveModel`, runs actions through `ActionRunner`,
/// and never polls: `.task` and timers don't run in a `.menu` extra, and the
/// rows update live because the model is `@Observable`.
struct MenuBarContentView: View {
	@Environment(AppEnvironment.self) private var env
	@Environment(\.openWindow) private var openWindow

	var body: some View {
		Group { items }
			// Fires when the menu opens. Catches up glance feeds whose poll is
			// overdue (after a backoff); 60 s so opening the menu often can't
			// push stats past one request a minute (each fetch also pushes the
			// next poll back).
			.onAppear {
				Task { await env.live.refreshNow([.stats, .meetings, .usage], ifOlderThan: .seconds(60)) }
			}
	}

	@ViewBuilder
	private var items: some View {
		let live = env.live
		let now = Date()

		if live.connection == .unconfigured {
			Button("Set Up Ember…") { openSettings(pane: "connection", using: openWindow) }
			Divider()
		}

		glanceRows(live, now: now)
		Divider()
		pomodoroRows(live)
		Divider()
		clockMenu(live)
		Divider()

		Button("Open Dashboard") { presentWindow(id: WindowID.dashboard, using: openWindow) }
			.keyboardShortcut("0", modifiers: .command)
		Button("Settings…") { openSettings(using: openWindow) }
			.keyboardShortcut(",", modifiers: .command)
		Button("About Ember") {
			NSApp.activate()
			NSApp.orderFrontStandardAboutPanel(nil)
		}

		Divider()

		Button("Quit Ember") { NSApplication.shared.terminate(nil) }
			.keyboardShortcut("q", modifiers: .command)
	}

	// MARK: Glance

	/// Session header + activity, other sessions, 5h usage, next event.
	/// Disabled text rows: they're read, not clicked.
	@ViewBuilder
	private func glanceRows(_ live: LiveModel, now: Date) -> some View {
		let header = MenuRows.header(connection: live.connection, hasEverLoaded: live.snapshot.value != nil,
		                             winning: live.winningSession)
		Text(header.title)
		if let detail = header.detail { Text(verbatim: "   \(detail)") }

		let others = MenuRows.otherSessions(live.sessions, winning: live.winningSession)
		if !others.rows.isEmpty {
			Menu("Other Sessions") {
				ForEach(others.rows) { Text($0.text) }
				if let overflow = others.overflow { Text(overflow) }
			}
		}

		ForEach(MenuRows.usage(live.usage, sessions: live.sessions, now: now)) { Text($0.text) }

		if let next = MenuRows.nextEvent(meetings: live.meetings.value, reminders: reminders, now: now) {
			Text(next)
		}
	}

	/// Apple Reminders the app already watches locally (no server call).
	private var reminders: [MenuRows.Reminder] {
		let watcher = env.reminderWatcher
		guard watcher.prefs.enabled else { return [] }
		return watcher.upcoming.map { MenuRows.Reminder(title: $0.title, due: $0.due) }
	}

	// MARK: Pomodoro

	/// Status line, the controls that apply (icons on the whole group, per
	/// the HIG's all-or-none), today vs the goal, and the last failed action.
	@ViewBuilder
	private func pomodoroRows(_ live: LiveModel) -> some View {
		if let group = MenuRows.pomodoroControls(live.pomodoro, connection: live.connection) {
			if let status = MenuRows.pomodoroStatus(live.pomodoro.value) { Text(status) }
			Group {
				ForEach(group.items) { item in
					Button {
						Task { await env.actions.run(.pomodoro(item.action)) }
					} label: {
						Label { Text(item.title) } icon: { Image(systemName: item.systemImage) }
					}
					.modifier(PrimaryShortcut(key: item.shortcutKey))
					.disabled(env.actions.running.contains(.pomodoro(item.action)))
				}
			}
			.labelStyle(.titleAndIcon)
			.disabled(!group.isEnabled)
		}
		if let today = MenuRows.today(live.stats.value) { Text(today) }
		if let failure = env.actions.lastError {
			Text(MenuRows.failure(failure.action, failure.error))
		}
	}

	// MARK: Clock

	@ViewBuilder
	private func clockMenu(_ live: LiveModel) -> some View {
		// The matrix state is only current while something (the Dashboard)
		// holds the clock-health feed; the menu doesn't poll it.
		let matrixPower = live.isTracked(.clockHealth) ? live.clockHealth.value?.device?.matrixPower : nil
		let power = MenuRows.displayPower(usage: live.usage, clockHealth: live.clockHealth, matrixPower: matrixPower)
		let apps = MenuRows.showOnClock(live.apps)

		Menu("Clock") {
			Button("Next App") { run(.clock(.next)) }
			Button("Previous App") { run(.clock(.previous)) }
			Button("Dismiss Notification") { run(.clock(.dismiss)) }
			if !power.isEmpty {
				Divider()
				ForEach(power) { item in
					Button { run(.clock(.power(item.on))) } label: { Text(item.title) }
				}
			}
			if !apps.isEmpty {
				Divider()
				Menu("Show on Clock") {
					ForEach(apps) { app in
						Toggle(isOn: Binding(
							get: { app.enabled },
							set: { on in run(.setApp(app.name, enabled: on)) }
						)) {
							Text(app.title)
						}
					}
				}
			}
		}
		.disabled(!live.connection.isOnline)
	}

	private func run(_ action: EmberAction) {
		Task { await env.actions.run(action) }
	}
}

/// ⇧⌘P on the context-sensitive primary Pomodoro item.
private struct PrimaryShortcut: ViewModifier {
	let key: Character?

	func body(content: Content) -> some View {
		if let key {
			content.keyboardShortcut(KeyEquivalent(key), modifiers: [.command, .shift])
		} else {
			content
		}
	}
}
