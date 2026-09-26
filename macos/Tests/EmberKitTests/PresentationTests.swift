import Testing
import Foundation
@testable import EmberKit

private let en = Locale(identifier: "en_US")

private func session(_ json: String) throws -> Session {
    try JSONDecoder().decode(Session.self, from: Data(json.utf8))
}

@Test func sessionTitleReadsLikeASentence() throws {
    let s = try session(#"{"source":"m4","tool":"claude","state":"running","activity":"Bash: sed -n 1,237p","message":"Bash","context_pct":8}"#)
    let p = SessionPresentation(s)
    #expect(p.title == "Claude on m4 — Running")
    #expect(p.toolDisplayName == "Claude")
    #expect(p.stateName == "Running")
    #expect(p.subtitle == "Bash: sed -n 1,237p")
    #expect(p.contextText(locale: en) == "8% context")
}

@Test func sessionPresentationFallsBack() throws {
    let s = try session(#"{"tool":"codex","state":"waiting","message":"needs input"}"#)
    let p = SessionPresentation(s)
    #expect(p.title == "Codex — Waiting")
    #expect(p.subtitle == "needs input")
    #expect(p.contextText(locale: en) == nil)
    #expect(SessionPresentation(try session(#"{"tool":"codex","state":"done"}"#)).subtitle == nil)
}

@Test(arguments: [
    ("claude", "Claude"), ("codex", "Codex"), ("ember-weather", "Weather"),
    ("pomodoro", "Pomodoro"), ("ember-pomodoro", "Pomodoro"), ("ember-air", "Air Quality"),
    ("ember-usage-claude", "Usage"), ("my_app", "My App"), ("Time", "Time"), ("", ""),
])
func appDisplayNames(wire: String, name: String) {
    #expect(AppNames.display(wire) == name)
}

@Test func remainingIsMinutesAndSeconds() {
    #expect(DurationText.remaining(1122, locale: en) == "18:42")
    #expect(DurationText.remaining(59, locale: en) == "0:59")
    #expect(DurationText.remaining(-5, locale: en) == "0:00")
    #expect(DurationText.remaining(3725, locale: en) == "1:02:05")
}

@Test func minutesAreHoursAndMinutes() {
    #expect(DurationText.minutes(75, locale: en) == "1h 15m")
    #expect(DurationText.minutes(45, locale: en) == "45m")
    #expect(DurationText.minutes(120, locale: en) == "2h")
    #expect(DurationText.minutes(0, locale: en) == "0m")
}

@Test func uptimeKeepsTwoUnits() {
    #expect(DurationText.uptime(6 * 3600 + 40 * 60 + 12, locale: en) == "6h 40m")
    #expect(DurationText.uptime(3 * 86_400 + 4 * 3600 + 5 * 60, locale: en) == "3d 4h")
    #expect(DurationText.uptime(12 * 60, locale: en) == "12m")
}

@Test func percentFormats() {
    #expect(Percent.text(47, locale: en) == "47%")
    #expect(Percent.text(0, locale: en) == "0%")
    #expect(Percent.text(ratio: 0.8333, locale: en) == "83%")
    // German puts a (non-breaking) space before the sign: the locale is honoured.
    #expect(Percent.text(47, locale: Locale(identifier: "de_DE")) != "47%")
}

private func pomo(_ phase: String, running: Bool = false, paused: Bool = false) -> PomoState {
    PomoState(phase: phase, running: running, paused: paused, remainingSec: 0, plannedSec: 0, round: 0)
}

@Test func pomodoroControlsPerMode() {
    func actions(_ s: PomoState?) -> [PomodoroAction] { PomodoroControls.items(for: s).map(\.action) }
    #expect(actions(nil) == [.start])
    #expect(actions(pomo("idle")) == [.start])
    #expect(actions(pomo("focus", running: true)) == [.pause, .skip, .stop])
    #expect(actions(pomo("focus", running: true, paused: true)) == [.resume, .skip, .stop])
    // Parked: the menu offered Start while the Dashboard offered Resume (audit #3).
    #expect(actions(pomo("short_break")) == [.resume, .stop])
}

@Test func onlyThePrimaryControlCarriesTheShortcut() {
    let running = PomodoroControls.items(for: pomo("focus", running: true))
    #expect(running.map(\.shortcutKey) == ["p", nil, nil])
    #expect(running.map(\.title) == ["Pause", "Skip Phase", "Stop"])
    #expect(running.map(\.systemImage) == ["pause.fill", "forward.end.fill", "stop.fill"])
    #expect(PomodoroControls.items(for: nil).first?.title == "Start Focus")
}

@Test func connectionSubtitles() {
    let utc = TimeZone(identifier: "UTC")!
    let since = Date(timeIntervalSince1970: 10 * 3600 + 42 * 60)
    #expect(ConnectionHealth.unconfigured.subtitle(serverHost: nil) == "Not set up")
    #expect(ConnectionHealth.connecting.subtitle(serverHost: "192.168.0.2") == "Connecting to 192.168.0.2…")
    #expect(ConnectionHealth.online(since: since).subtitle(serverHost: "192.168.0.2") == "Connected to 192.168.0.2")
    #expect(ConnectionHealth.degraded(failures: 1).subtitle(serverHost: "") == "Connected")
    #expect(ConnectionHealth.offline(since: since).subtitle(serverHost: "h", locale: en, timeZone: utc)
        .hasPrefix("Offline since 10:42"))
}
