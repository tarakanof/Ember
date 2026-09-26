import Testing
import Foundation
@testable import EmberKit

private let en = Locale(identifier: "en_US")
private let utc = TimeZone(identifier: "UTC")!
/// 2026-09-26 10:00:00 UTC, a Saturday.
private let now = Date(timeIntervalSince1970: 1_790_416_800)

extension String {
    /// Date styles put a narrow no-break space before AM/PM.
    fileprivate var ns: String { replacingOccurrences(of: "\u{202F}", with: " ") }
}

private func session(_ json: String) throws -> Session {
    try JSONDecoder().decode(Session.self, from: Data(json.utf8))
}

private func session(tool: String, source: String = "", state: String, id: String = "s",
                     updated: TimeInterval = 0, context: Int? = nil, rate: Int? = nil,
                     rateResetAt: Int64 = 0) throws -> Session {
    var s = try session(#"{"tool":"\#(tool)","state":"\#(state)","session":"\#(id)"}"#)
    s.source = source
    s.updatedAt = now.addingTimeInterval(updated)
    s.contextPct = context
    s.rateWindowPct = rate
    s.rateResetAt = rateResetAt
    return s
}

private func pomo(_ phase: String, running: Bool = false, paused: Bool = false,
                  remaining: Int = 1122, round: Int = 2) -> PomoState {
    PomoState(phase: phase, running: running, paused: paused, remainingSec: remaining,
              plannedSec: 1500, round: round)
}

// MARK: Header

@Test func headerShowsTheWinningSessionAndItsActivity() throws {
    let s = try session(#"{"source":"m4","tool":"claude","state":"running","activity":"Bash: sed -n 1,237p cmd/ember/device.go and then some more text"}"#)
    let h = MenuRows.header(connection: .online(since: now), hasEverLoaded: true, winning: s, locale: en, timeZone: utc)
    #expect(h.title.text == "Claude on m4 — Running")
    #expect(h.detail?.count == 48)
    #expect(h.detail?.hasPrefix("Bash: sed -n 1,237p") == true)
}

@Test func headerSanitisesActivity() throws {
    let s = try session(#"{"tool":"claude","state":"running","activity":"<task-notification>x</task-notification>"}"#)
    let h = MenuRows.header(connection: .online(since: now), hasEverLoaded: true, winning: s, locale: en, timeZone: utc)
    #expect(h.detail == nil)
}

@Test func headerIsIdleWhenConnectedWithoutAWinner() {
    let h = MenuRows.header(connection: .degraded(failures: 1), hasEverLoaded: true, winning: nil, locale: en, timeZone: utc)
    #expect(h.title.text == "Idle")
    #expect(h.detail == nil)
}

@Test func headerExplainsTheConnection() {
    let since = now.addingTimeInterval(-600)
    #expect(MenuRows.header(connection: .unconfigured, hasEverLoaded: false, winning: nil).title.text == "Not set up")
    #expect(MenuRows.header(connection: .connecting, hasEverLoaded: false, winning: nil).title.text == "Connecting…")
    #expect(MenuRows.header(connection: .offline(since: since), hasEverLoaded: false, winning: nil,
                            locale: en, timeZone: utc).title.text.ns == "Offline")
    #expect(MenuRows.header(connection: .offline(since: since), hasEverLoaded: true, winning: nil,
                            locale: en, timeZone: utc).title.text.ns == "Offline — server unreachable since 9:50 AM")
}

@Test func accessibilityValueFollowsTheHeader() throws {
    let s = try session(tool: "codex", source: "m5", state: "waiting")
    #expect(MenuRows.accessibilityValue(connection: .online(since: now), winning: s).text == "Codex on m5 — Waiting")
    #expect(MenuRows.accessibilityValue(connection: .online(since: now), winning: nil).text == "Idle")
    #expect(MenuRows.accessibilityValue(connection: .offline(since: now), winning: nil).text == "Offline")
}

// MARK: Other sessions

@Test func otherSessionsExcludeTheWinnerAndPutAttentionFirst() throws {
    let winner = try session(tool: "claude", source: "m4", state: "waiting", id: "a", updated: 0)
    let running = try session(tool: "codex", source: "m5", state: "running", id: "b", updated: -10, context: 41)
    let done = try session(tool: "claude", source: "m5", state: "done", id: "c", updated: 0)
    let error = try session(tool: "claude", source: "m4", state: "error", id: "d", updated: -50)
    let newerRunning = try session(tool: "claude", source: "m4", state: "running", id: "e", updated: -1)
    let others = MenuRows.otherSessions([done, running, winner, error, newerRunning], winning: winner, locale: en)
    #expect(others.rows.map(\.text.text.ns) == [
        "Claude on m4 — Error",
        "Claude on m4 — Running",
        "Codex on m5 — Running · 41% context",
        "Claude on m5 — Done",
    ])
    #expect(others.overflow == nil)
    #expect(Set(others.rows.map(\.id)).count == 4)
}

@Test func otherSessionsHiddenWithOneSession() throws {
    let only = try session(tool: "claude", state: "running")
    #expect(MenuRows.otherSessions([only], winning: only).rows.isEmpty)
    // One idle session and no winner: still nothing to add.
    let idle = try session(tool: "claude", state: "idle")
    #expect(MenuRows.otherSessions([idle], winning: nil).rows.isEmpty)
}

@Test func otherSessionsOverflowIntoACount() throws {
    let winner = try session(tool: "claude", state: "running", id: "w")
    let rest = try (0..<12).map { try session(tool: "codex", source: "m\($0)", state: "running", id: "r\($0)", updated: -Double($0)) }
    let others = MenuRows.otherSessions([winner] + rest, winning: winner, limit: 8)
    #expect(others.rows.count == 8)
    #expect(others.rows.first?.text.text.ns == "Codex on m0 — Running")
    #expect(others.overflow?.text == "4 more")
}

// MARK: Usage

private func usageSnapshot(_ tools: [UsageSnapshot.Tool]) -> UsageSnapshot {
    UsageSnapshot(generatedAt: now, staleAfterSec: 900, tools: tools)
}

private func tool(_ name: String, pct: Double?, resetsIn: TimeInterval? = nil, label: String? = nil,
                  stale: Bool = false) -> UsageSnapshot.Tool {
    UsageSnapshot.Tool(tool: name, source: nil, updatedAt: now, stale: stale,
                       fiveHour: pct.map { UsageSnapshot.Window(usedPercent: $0, resetsAt: resetsIn.map { now.addingTimeInterval($0) },
                                                                resetLabel: label) },
                       sevenDay: nil, models: [:])
}

@Test func usageRowPerToolWithResetTime() {
    let rows = MenuRows.usage(.loaded(usageSnapshot([tool("claude", pct: 47.4, resetsIn: 3600 + 20 * 60),
                                                     tool("codex", pct: 12)]), at: now),
                              sessions: [], now: now, locale: en, timeZone: utc)
    #expect(rows.map(\.text.text.ns) == ["Claude 5h 47% · resets 11:20 AM", "Codex 5h 12%"])
    #expect(rows.map(\.level) == [.normal, .normal])
}

@Test func usageRowThresholds() {
    let rows = MenuRows.usage(.loaded(usageSnapshot([
        tool("a", pct: 79.4), tool("b", pct: 80), tool("c", pct: 100, resetsIn: 600), tool("d", pct: 130),
        tool("e", pct: -3),
    ]), at: now), sessions: [], now: now, locale: en, timeZone: utc)
    #expect(rows.map(\.level) == [.normal, .high, .limit, .limit, .normal])
    #expect(rows[2].text.text.ns == "C 5h limit reached · resets 10:10 AM")
    #expect(rows[3].text.text.ns == "D 5h limit reached")
    #expect(rows[4].text.text.ns == "E 5h 0%")
}

@Test func usageSkipsStaleToolsAndToolsWithoutAFiveHourWindow() {
    let rows = MenuRows.usage(.loaded(usageSnapshot([tool("claude", pct: 50, stale: true), tool("codex", pct: nil)]), at: now),
                              sessions: [], now: now, locale: en, timeZone: utc)
    #expect(rows.isEmpty)
}

@Test func usageResetFarAwayShowsTheDayAndPastResetFallsBackToTheLabel() {
    let rows = MenuRows.usage(.loaded(usageSnapshot([
        tool("a", pct: 5, resetsIn: 30 * 3600),
        tool("b", pct: 5, resetsIn: -60, label: "in  2h\n"),
    ]), at: now), sessions: [], now: now, locale: en, timeZone: utc)
    #expect(rows[0].text.text.ns == "A 5h 5% · resets Sun 4:00 PM")
    #expect(rows[1].text.text.ns == "B 5h 5% · resets in 2h")
}

@Test func usageFallsBackToStateOnAnOldServer() throws {
    let reset = Int64(now.addingTimeInterval(1800).timeIntervalSince1970)
    let older = try session(tool: "claude", source: "m5", state: "done", id: "o", updated: -100, rate: 10)
    let newer = try session(tool: "claude", source: "m4", state: "running", id: "n", updated: -1, rate: 47, rateResetAt: reset)
    let noRate = try session(tool: "codex", state: "running", id: "c")
    let rows = MenuRows.usage(.failed(.featureOff, last: nil, lastAt: nil), sessions: [older, newer, noRate],
                              now: now, locale: en, timeZone: utc)
    #expect(rows.map(\.text.text.ns) == ["Claude 5h 47% · resets 10:30 AM"])
}

@Test func usageFeedWinsOverStatePerTool() throws {
    let claude = try session(tool: "claude", state: "running", id: "a", rate: 99)
    let codex = try session(tool: "codex", state: "running", id: "b", rate: 20)
    let rows = MenuRows.usage(.loaded(usageSnapshot([tool("claude", pct: 47)]), at: now),
                              sessions: [claude, codex], now: now, locale: en, timeZone: utc)
    #expect(rows.map(\.text.text.ns) == ["Claude 5h 47%", "Codex 5h 20%"])
}

// MARK: Next event

private func meetings(_ items: [(String, TimeInterval)]) -> MeetingsState {
    MeetingsState(upcoming: items.map { MeetingsState.Item(title: $0.0, start: now.addingTimeInterval($0.1)) })
}

@Test func nextMeetingFormatting() {
    func next(_ offset: TimeInterval, _ title: String = "Standup") -> String? {
        MenuRows.nextEvent(meetings: meetings([(title, offset)]), reminders: [], now: now, locale: en, timeZone: utc)?.text.ns
    }
    #expect(next(12 * 60) == "Next: Standup in 12 min")
    #expect(next(11 * 60 + 5) == "Next: Standup in 12 min")
    #expect(next(30) == "Next: Standup now")
    #expect(next(-120) == "Next: Standup now")
    #expect(next(-10 * 60) == nil)
    #expect(next(4 * 3600 + 30 * 60) == "Next: Standup at 2:30 PM")
    #expect(next(23 * 3600) == "Next: Standup tomorrow at 9:00 AM")
    #expect(next(35 * 3600) == "Next: Standup tomorrow at 9:00 PM")
    #expect(next(37 * 3600) == nil)
    #expect(next(3600, "1:1  with\n  Ann") == "Next: 1:1 with Ann at 11:00 AM")
}

@Test func nextEventPicksTheEarliestIncludingReminders() {
    let m = meetings([("Standup", 3600), ("Retro", 7200)])
    let r = [MenuRows.Reminder(title: "Pay rent", due: now.addingTimeInterval(20 * 60))]
    #expect(MenuRows.nextEvent(meetings: m, reminders: r, now: now, locale: en, timeZone: utc)?.text.ns
            == "Next: Reminder: Pay rent in 20 min")
    #expect(MenuRows.nextEvent(meetings: nil, reminders: [], now: now) == nil)
}

// MARK: Pomodoro

@Test func pomodoroStatusLine() {
    #expect(MenuRows.pomodoroStatus(nil) == nil)
    #expect(MenuRows.pomodoroStatus(pomo("idle")) == nil)
    #expect(MenuRows.pomodoroStatus(pomo("focus", running: true), locale: en)?.text == "Focus · 18:42 left · round 2")
    #expect(MenuRows.pomodoroStatus(pomo("short_break", running: true, paused: true), locale: en)?.text
            == "Short Break · paused · 18:42 left")
    #expect(MenuRows.pomodoroStatus(pomo("long_break"), locale: en)?.text == "Long Break · ready to start")
}

@Test func pomodoroControlsParkedVersusPaused() {
    let online = ConnectionHealth.online(since: now)
    let paused = MenuRows.pomodoroControls(.loaded(pomo("focus", running: true, paused: true), at: now), connection: online)
    #expect(paused?.items.map(\.action) == [.resume, .skip, .stop])
    #expect(paused?.isEnabled == true)
    let parked = MenuRows.pomodoroControls(.loaded(pomo("short_break"), at: now), connection: online)
    #expect(parked?.items.map(\.action) == [.resume, .stop])
    #expect(parked?.items.first?.shortcutKey == "p")
    let idle = MenuRows.pomodoroControls(.loading, connection: online)
    #expect(idle?.items.map(\.action) == [.start])
}

@Test func pomodoroControlsHiddenWhenOffAndDisabledWhenOffline() {
    #expect(MenuRows.pomodoroControls(.failed(.featureOff, last: nil, lastAt: nil), connection: .online(since: now)) == nil)
    let offline = MenuRows.pomodoroControls(.loaded(pomo("focus", running: true), at: now), connection: .offline(since: now))
    #expect(offline?.isEnabled == false)
    #expect(offline?.items.map(\.action) == [.pause, .skip, .stop])
}

// MARK: Today

private func stats(done: Int, minutes: Int, goal: Int) -> PomoStats {
    PomoStats(today: PomoDayStat(date: "2026-09-26", completedFocus: done, focusMin: minutes), history: [], streak: 0,
              goal: GoalStatus(dailySessions: goal, todayCompleted: done, dailyMet: done >= goal))
}

@Test func todayRowAgainstTheGoal() {
    #expect(MenuRows.today(nil) == nil)
    #expect(MenuRows.today(stats(done: 3, minutes: 75, goal: 8), locale: en)?.text == "Today 3 of 8 · 1h 15m")
    #expect(MenuRows.today(stats(done: 3, minutes: 75, goal: 0), locale: en)?.text == "Today 3 sessions · 1h 15m")
    #expect(MenuRows.today(stats(done: 1, minutes: 25, goal: 0), locale: en)?.text == "Today 1 session · 25m")
    #expect(MenuRows.today(stats(done: 0, minutes: 0, goal: 0), locale: en)?.text == "Today 0 sessions · 0m")
}

// MARK: Action errors

@Test func failureRowNamesTheActionAndAShortReason() {
    #expect(MenuRows.failure(.pomodoro(.start), .unauthorized).text == "Couldn't start: unauthorized")
    #expect(MenuRows.failure(.pomodoro(.skip), .offline).text == "Couldn't skip: server unreachable")
    #expect(MenuRows.failure(.clock(.power(false)), .featureOff).text
            == "Couldn't turn the display off: not supported by this server")
    #expect(MenuRows.failure(.setApp("ember-weather", enabled: false), .rateLimited).text
            == "Couldn't hide Weather: rate-limited")
    #expect(MenuRows.failure(.clock(.next), .server("boom")).text == "Couldn't switch apps: server error")
}

// MARK: Clock

@Test func displayPowerHiddenUntilTheServerSupportsIt() {
    let off = Loadable<UsageSnapshot>.failed(.featureOff, last: nil, lastAt: nil)
    let health = Loadable<ClockHealth>.loading
    #expect(MenuRows.displayPower(usage: off, clockHealth: health, matrixPower: nil).isEmpty)
    // A 0.28 server answers GET /v1/usage, and ships the power route with it.
    let usage = Loadable<UsageSnapshot>.loaded(usageSnapshot([]), at: now)
    #expect(MenuRows.displayPower(usage: usage, clockHealth: health, matrixPower: nil).map(\.on) == [false, true])
    #expect(MenuRows.displayPower(usage: usage, clockHealth: health, matrixPower: true).map(\.on) == [false])
    #expect(MenuRows.displayPower(usage: usage, clockHealth: health, matrixPower: false).map(\.title.text) == ["Turn Display On"])
}

@Test func showOnClockListsAppsWithDisplayNames() {
    #expect(MenuRows.showOnClock(.failed(.featureOff, last: nil, lastAt: nil)).isEmpty)
    let apps = MenuRows.showOnClock(.loaded([AppToggle(name: "claude", enabled: true),
                                             AppToggle(name: "ember-weather", enabled: false)], at: now))
    #expect(apps.map(\.title.text) == ["Claude", "Weather"])
    #expect(apps.map(\.enabled) == [true, false])
}
