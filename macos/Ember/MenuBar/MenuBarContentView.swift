import SwiftUI
import EmberKit

/// Native AppKit menu (`.menu` style): status, Pomodoro controls, per-app
/// visibility toggles, then Dashboard/Settings/About/Quit. Reads `LiveModel`
/// and runs actions through `ActionRunner`; it never polls.
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

		if live.connection == .unconfigured {
			Button("Set Up Ember…") { openSettings(pane: "connection", using: openWindow) }
			Divider()
		}

		// Status header (disabled text rows).
		if let s = live.winningSession {
			let p = SessionPresentation(s)
			Text(p.title)
			if let sub = p.subtitle(maxLength: 48) { Text(verbatim: sub) }
		} else {
			Text(statusText(live.connection))
		}

		Divider()

		// Pomodoro: phase line while active, then the controls that apply.
		if let p = live.pomodoro.value, p.mode != .idle {
			Text("\(Text(p.phaseEnum.displayName)) · \(DurationText.remaining(p.remainingSec)) · round \(p.round)")
		}
		Group {
			ForEach(PomodoroControls.items(for: live.pomodoro.value)) { item in
				Button {
					Task { await env.actions.run(.pomodoro(item.action)) }
				} label: {
					Label { Text(item.title) } icon: { Image(systemName: item.systemImage) }
				}
				.modifier(PrimaryShortcut(key: item.shortcutKey))
			}
		}
		.labelStyle(.titleAndIcon)
		.disabled(!live.connection.isOnline || live.pomodoro.error == .featureOff)
		if let failure = env.actions.lastError {
			Text("Couldn't do that: \(Text(failure.error.message))")
		}

		Divider()

		// Per-app clock visibility toggles (dynamic; future apps appear here).
		let apps = live.apps.value ?? []
		ForEach(apps, id: \.name) { app in
			Toggle(isOn: Binding(
				get: { app.enabled },
				set: { on in Task { await env.actions.run(.setApp(app.name, enabled: on)) } }
			)) {
				Text(AppNames.display(app.name))
			}
		}
		if !apps.isEmpty { Divider() }

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

	private func statusText(_ c: ConnectionHealth) -> LocalizedStringKey {
		switch c {
		case .unconfigured: "Not set up"
		case .connecting: "Connecting…"
		case .offline: "Offline"
		case .online, .degraded: "Idle"
		}
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
