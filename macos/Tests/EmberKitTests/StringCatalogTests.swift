import Testing
import Foundation
@testable import EmberKit

/// Every user-facing string EmberKit hands the app. Xcode only extracts
/// literals from the app target, so these keys are added to the catalog by
/// hand; this list keeps the two in step.
private func emberKitStrings() -> [LocalizedStringResource] {
    var out: [LocalizedStringResource] = []
    let states: [Session.State] = [.running, .waiting, .done, .error, .idle, .unknown(""), .unknown("x")]
    out += states.map(\.displayName)
    let phases: [PomoPhase] = [.idle, .focus, .shortBreak, .longBreak, .unknown("x")]
    out += phases.map(\.displayName)
    let timers = ["idle", "focus", "short_break"].flatMap { phase in
        [(false, false), (true, false), (true, true)].map {
            PomoState(phase: phase, running: $0.0, paused: $0.1, remainingSec: 0, plannedSec: 0, round: 0)
        }
    }
    out += timers.flatMap { PomodoroControls.items(for: $0).map(\.title) }
    out += ["claude", "codex", "weather", "ember-weather-popup", "ember-forecast", "ember-air", "ember-air-popup",
            "ember-sun-popup", "pomodoro", "ember-meet", "ember-meeting", "ember-reminder", "ember-notify",
            "ember-usage-alarm", "ember-usage-claude", "mystery"].map(AppNames.display)
    let session = try! JSONDecoder().decode(Session.self, from: Data(#"{"tool":"claude","state":"running","context_pct":5}"#.utf8))
    var sourced = session
    sourced.source = "m4"
    out += [SessionPresentation(session).title, SessionPresentation(sourced).title,
            SessionPresentation(session).contextText()!]
    let health: [ConnectionHealth] = [.unconfigured, .connecting, .online(since: .now),
                                      .degraded(failures: 1), .offline(since: .now)]
    out += health.flatMap { [$0.subtitle(serverHost: nil), $0.subtitle(serverHost: "h")] }
    let errors: [FeedError] = [.offline, .unauthorized, .rateLimited, .featureOff, .server("x")]
    out += errors.flatMap { [$0.message, $0.saveMessage] }
    out += [AggregateSaveStatus.saving, .saved, .failed].compactMap(\.subtitle)
    return out
}

@Test func emberKitStringsAreInTheAppCatalog() throws {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("Ember/Localizable.xcstrings")
    let catalog = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
    let keys = Set((catalog["strings"] as? [String: Any] ?? [:]).keys)
    let missing = Set(emberKitStrings().map(\.key)).subtracting(keys).sorted()
    #expect(missing.isEmpty, "add to Localizable.xcstrings: \(missing)")
}
