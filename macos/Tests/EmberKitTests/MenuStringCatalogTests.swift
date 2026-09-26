import Testing
import Foundation
@testable import EmberKit

/// Every string `MenuRows` hands the menu. Xcode doesn't extract EmberKit's
/// literals, so these keys are added to the catalog by hand; this keeps the
/// two in step (as `StringCatalogTests` does for the foundations).
private func menuStrings() throws -> [LocalizedStringResource] {
    let now = Date(timeIntervalSince1970: 1_790_416_800)
    let utc = TimeZone(identifier: "UTC")!
    var s = try JSONDecoder().decode(Session.self, from: Data(#"{"tool":"claude","state":"running","session":"a","context_pct":5}"#.utf8))
    var out: [LocalizedStringResource] = []

    let connections: [ConnectionHealth] = [.unconfigured, .connecting, .online(since: now), .offline(since: now)]
    for c in connections {
        for loaded in [false, true] {
            out.append(MenuRows.header(connection: c, hasEverLoaded: loaded, winning: nil).title)
        }
        out.append(MenuRows.accessibilityValue(connection: c, winning: nil))
    }

    var other = s
    other.session = "b"
    var noContext = s
    noContext.session = "c"
    noContext.contextPct = nil
    let others = MenuRows.otherSessions([s, other, noContext], winning: nil, limit: 2)
    out += others.rows.map(\.text)
    out += [others.overflow].compactMap { $0 }
    var bare = noContext
    bare.session = "d"
    out += MenuRows.otherSessions([bare, noContext], winning: nil).rows.map(\.text)

    for pct in [5, 100] {
        s.rateWindowPct = pct
        for reset: Int64 in [0, Int64(now.timeIntervalSince1970) + 60] {
            s.rateResetAt = reset
            out += MenuRows.usage(.loading, sessions: [s], now: now).map(\.text)
        }
    }

    for offset: TimeInterval in [30, 600, 7200, 23 * 3600, 35 * 3600] {
        let m = MeetingsState(upcoming: [MeetingsState.Item(title: "M", start: now.addingTimeInterval(offset))])
        out += [MenuRows.nextEvent(meetings: m, reminders: [], now: now, timeZone: utc)].compactMap { $0 }
    }
    let lateNight = Date(timeIntervalSince1970: 1_790_416_800 + 13 * 3600) // 23:00 UTC
    let dayAfter = MeetingsState(upcoming: [MeetingsState.Item(title: "M", start: lateNight.addingTimeInterval(34 * 3600))])
    out += [MenuRows.nextEvent(meetings: dayAfter, reminders: [], now: lateNight, timeZone: utc)].compactMap { $0 }
    out.append("Reminder: \("R")")

    let timers = [(true, false), (true, true), (false, false)].map {
        PomoState(phase: "focus", running: $0.0, paused: $0.1, remainingSec: 1, plannedSec: 1, round: 1)
    }
    out += timers.compactMap { MenuRows.pomodoroStatus($0) }
    for (done, goal) in [(1, 0), (2, 0), (2, 8)] {
        let stats = PomoStats(today: PomoDayStat(date: "", completedFocus: done, focusMin: 0), history: [], streak: 0,
                              goal: GoalStatus(dailySessions: goal))
        out += [MenuRows.today(stats)].compactMap { $0 }
    }

    let clock: [ClockAction] = [.next, .previous, .dismiss, .power(true), .power(false), .reboot]
    let actions: [EmberAction] = PomodoroAction.allCases.map { .pomodoro($0) }
        + [.setApp("x", enabled: true), .setApp("x", enabled: false)]
        + clock.map { .clock($0) }
    out += actions.map { MenuRows.failure($0, .offline) }
    out += ["server unreachable", "unauthorized", "rate-limited", "not supported by this server", "server error"]

    let usage = Loadable<UsageSnapshot>.loaded(UsageSnapshot(generatedAt: now, staleAfterSec: 1, tools: []), at: now)
    out += MenuRows.displayPower(usage: usage, clockHealth: .loading, matrixPower: nil).map(\.title)
    return out
}

@Test func menuStringsAreInTheAppCatalog() throws {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("Ember/Localizable.xcstrings")
    let catalog = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
    let keys = Set((catalog["strings"] as? [String: Any] ?? [:]).keys)
    let strings = try menuStrings()
    #expect(strings.count > 40)
    let missing = Set(strings.map(\.key)).subtracting(keys).sorted()
    #expect(missing.isEmpty, "add to Localizable.xcstrings: \(missing)")
}
