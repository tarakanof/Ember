import SwiftUI
import EmberKit

/// Native AppKit menu (`.menu` style): dim status + Pomodoro phase line + actions,
/// per-app visibility toggles, then Dashboard/Settings/Quit.
struct MenuBarContentView: View {
	@Environment(AppEnvironment.self) private var env
	@Environment(\.openWindow) private var openWindow
	@Environment(\.openSettings) private var openSettings

	private func mmss(_ sec: Int) -> String {
		let s = max(0, sec); return String(format: "%d:%02d", s / 60, s % 60)
	}
	private func act(_ a: PomodoroAction) {
		Task { try? await env.pomodoro.action(a); await env.model.refresh() }
	}

	var body: some View {
		let model = env.model

		// Status header (dim).
		if let s = model.winningSession {
			Text("\(s.source) · \(s.tool) · \(s.state)")
		} else {
			Text(model.connected ? "Idle" : "Offline")
		}

		Divider()

		// Pomodoro: dim phase line (when active) + phase-appropriate actions.
		if let p = model.pomoState, p.phase != "idle" {
			Text("\(p.phase.capitalized) · \(mmss(p.remainingSec)) · round \(p.round)")
			if p.running && !p.paused {
				Button("Pause") { act(.pause) }
				Button("Skip") { act(.skip) }
				Button("Stop") { act(.stop) }
			} else {
				// Paused or parked (auto-advance makes parked rare): resume or stop.
				Button("Resume") { act(.resume) }
				Button("Stop") { act(.stop) }
			}
		} else {
			Button("Start Focus") { act(.start) }
		}

		Divider()

		// Per-app clock visibility toggles (dynamic; future apps appear here).
		ForEach(model.apps, id: \.name) { app in
			Toggle(app.name.capitalized, isOn: Binding(
				get: { app.enabled },
				set: { newValue in Task { await env.model.setApp(app.name, enabled: newValue) } }
			))
		}
		if !model.apps.isEmpty { Divider() }

		Button("Dashboard…") {
			NSApplication.shared.activate(ignoringOtherApps: true)
			openWindow(id: "dashboard")
		}
		Button("Settings…") {
			NSApplication.shared.activate(ignoringOtherApps: true)
			openSettings()
		}
		.keyboardShortcut(",", modifiers: .command)

		Divider()

		Button("Quit Ember") { NSApplication.shared.terminate(nil) }
			.keyboardShortcut("q", modifiers: .command)
	}
}
