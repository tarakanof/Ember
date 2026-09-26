import Testing
import Foundation
@testable import EmberKit

// View-models behind the Dashboard's non-chart cards.

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

private func decode<T: Decodable>(_ json: String) -> T {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try! d.decode(T.self, from: Data(json.utf8))
}

private func state(_ phase: String, running: Bool = true, paused: Bool = false,
                   remaining: Int = 600, planned: Int = 1500) -> PomoState {
    PomoState(phase: phase, running: running, paused: paused, remainingSec: remaining, plannedSec: planned, round: 2)
}

private func stats(done: Int, goal: Int, total: Int = 10, streak: Int = 3) -> PomoStats {
    let today: PomoDayStat = decode(#"{"date":"2026-09-26","completed_focus":\#(done),"focus_min":\#(done * 25)}"#)
    return PomoStats(today: today, history: [today], streak: streak, longestStreak: 9,
                     completion: CompletionStat(completedFocus: 8, abandonedFocus: 2, totalFocus: total,
                                                completionRate: total == 0 ? 0 : 0.8, focusSec: 0),
                     goal: GoalStatus(dailySessions: goal, todayCompleted: done))
}

private let now = iso("2026-09-26T10:00:00+02:00")

// MARK: Focus

@Test func focusRingShowsTheGoalWhileIdle() {
    let f = FocusSummary(stats: stats(done: 3, goal: 8), state: state("idle", running: false), now: now)
    #expect(f.ring == .goal(completed: 3, goal: 8))
    #expect(f.gauge == (3, 8))
    #expect(f.completionRate == 0.8)
    #expect(f.longestStreak == 9)
    #expect(!f.dailyGoalMet)
    #expect(!f.isEmpty)
}

@Test func focusRingWithoutAGoalStillReadsAsDone() {
    let f = FocusSummary(stats: stats(done: 0, goal: 0, total: 0, streak: 0), state: nil, now: now)
    #expect(f.ring == .goal(completed: 0, goal: nil))
    #expect(f.gauge == (0, 1))
    #expect(f.completionRate == nil)
    // New user: longest streak is 9 in the fixture, so not empty; a truly
    // fresh stats payload is.
    let fresh = PomoStats(today: decode(#"{"date":"2026-09-26","completed_focus":0,"focus_min":0}"#),
                          history: [], streak: 0)
    #expect(FocusSummary(stats: fresh, state: nil, now: now).isEmpty)
    #expect(FocusSummary(stats: stats(done: 12, goal: 8), state: nil, now: now).gauge == (8, 8))
}

@Test func focusRingFollowsTheRunningPhase() {
    let f = FocusSummary(stats: stats(done: 3, goal: 8), state: state("focus"), now: now)
    #expect(f.ring == .phase(.focus, remaining: 600, planned: 1500, endsAt: now.addingTimeInterval(600), round: 2))
    #expect(f.gauge == (900, 1500))
    let paused = FocusSummary(stats: nil, state: state("short_break", paused: true), now: now)
    #expect(paused.ring == .phase(.shortBreak, remaining: 600, planned: 1500, endsAt: nil, round: 2))
    #expect(!paused.isEmpty)
}

// MARK: Usage

private let usageJSON = #"""
{"generated_at":"2026-09-26T10:30:00+02:00","stale_after_sec":600,"tools":[
 {"tool":"claude","source":"m4","updated_at":"2026-09-26T10:28:00+02:00","stale":false,
  "five_hour":{"used_percent":14,"resets_at":"2026-09-26T12:30:00+02:00","reset_label":"12:30"},
  "seven_day":null,"models":{"opus":{"used_percent":5},"sonnet":{"used_percent":20.5}}}]}
"""#

@Test func usageRowsComeFromTheSnapshot() {
    let rows = UsageRow.rows(from: decode(usageJSON) as UsageSnapshot)
    #expect(rows.map(\.tool) == ["claude"])
    #expect(rows[0].fiveHour?.percent == 14)
    #expect(rows[0].fiveHour?.resetsAt == iso("2026-09-26T12:30:00+02:00"))
    #expect(rows[0].sevenDay == nil)
    #expect(rows[0].models.map(\.name) == ["sonnet", "opus"])
}

private func session(_ tool: String, source: String = "m4", pct: Int? = nil, resetAt: String? = nil,
                     updated: String = "2026-09-26T10:00:00+02:00") -> Session {
    var json = #"{"source":"\#(source)","tool":"\#(tool)","state":"running","updated_at":"\#(updated)""#
    if let pct { json += #","rate_window_pct":\#(pct)"# }
    if let resetAt { json += #","rate_reset_at":\#(Int64(iso(resetAt).timeIntervalSince1970))"# }
    return decode(json + "}")
}

private let emptyUsage: UsageSnapshot = decode(#"{"generated_at":"2026-09-26T10:00:00+02:00","stale_after_sec":600,"tools":[]}"#)

@Test func usageRowsFallBackToSessionsWhenTheSnapshotIsEmpty() {
    // 0.28 with no producer posting /v1/usage: the sessions' 5h window still shows.
    let sessions = [
        session("claude", source: "m4", pct: 16, resetAt: "2026-09-26T12:40:00+02:00", updated: "2026-09-26T09:59:00+02:00"),
        session("claude", source: "m5", pct: 18, resetAt: "2026-09-26T12:40:00+02:00", updated: "2026-09-26T09:59:30+02:00"),
        session("codex", pct: 5),  // no known reset: left out, as in the menu
    ]
    let rows = UsageRow.rows(from: emptyUsage, sessions: sessions, now: now)
    #expect(rows.map(\.tool) == ["claude"])
    #expect(rows[0].fiveHour == UsageRow.Window(percent: 18, resetsAt: iso("2026-09-26T12:40:00+02:00"), resetLabel: nil))
    #expect(rows[0].source == "m5")
    #expect(!rows[0].stale)
    // Old server (no /v1/usage): same rows.
    #expect(UsageRow.rows(from: nil, sessions: sessions, now: now) == rows)
}

@Test func usageSessionRowsDropOnceTheWindowHasReset() {
    let sessions = [session("claude", pct: 97, resetAt: "2026-09-26T09:30:00+02:00")]
    #expect(UsageRow.rows(from: emptyUsage, sessions: sessions, now: now).isEmpty)
    #expect(MenuRows.usage(.loaded(emptyUsage, at: now), sessions: sessions, now: now).isEmpty)
}

@Test func usageFreshSnapshotWinsOverSessions() {
    let snap: UsageSnapshot = decode(usageJSON)
    let sessions = [session("claude", source: "m5", pct: 60, resetAt: "2026-09-26T12:40:00+02:00"),
                    session("codex", source: "m5", pct: 30, resetAt: "2026-09-26T11:00:00+02:00")]
    let rows = UsageRow.rows(from: snap, sessions: sessions, now: now)
    #expect(rows.map(\.tool) == ["claude", "codex"])
    #expect(rows[0].fiveHour?.percent == 14)
    #expect(rows[0].models.count == 2)
    #expect(rows[1].fiveHour?.percent == 30)
}

@Test func usageStaleSnapshotYieldsToSessionsElseStaysFlagged() {
    let stale: UsageSnapshot = decode(usageJSON.replacingOccurrences(of: #""stale":false"#, with: #""stale":true"#))
    let live = [session("claude", source: "m5", pct: 60, resetAt: "2026-09-26T12:40:00+02:00")]
    let rows = UsageRow.rows(from: stale, sessions: live, now: now)
    #expect(rows.count == 1)
    #expect(rows[0].fiveHour?.percent == 60)
    #expect(!rows[0].stale)
    let kept = UsageRow.rows(from: stale, sessions: [], now: now)
    #expect(kept.first?.stale == true)
    #expect(kept.first?.fiveHour?.percent == 14)
}

@Test func usageFreshSnapshotWithoutFiveHourBorrowsTheSessionWindow() {
    let weekly: UsageSnapshot = decode(#"""
    {"generated_at":"2026-09-26T10:00:00+02:00","stale_after_sec":600,"tools":[
     {"tool":"claude","source":"statusline","updated_at":"2026-09-26T09:59:00+02:00","stale":false,
      "five_hour":null,"seven_day":{"used_percent":33,"resets_at":null,"reset_label":"FRI"},"models":{}}]}
    """#)
    let rows = UsageRow.rows(from: weekly, sessions: [session("claude", pct: 22, resetAt: "2026-09-26T12:40:00+02:00")], now: now)
    #expect(rows.count == 1)
    #expect(rows[0].fiveHour?.percent == 22)
    #expect(rows[0].sevenDay?.percent == 33)
    #expect(rows[0].source == "statusline")
}

// MARK: Upcoming

@Test func upcomingMergesSortsAndCaps() {
    let meetings = [
        MeetingsState.Item(uid: "a", title: "Standup", start: now.addingTimeInterval(12 * 60)),
        MeetingsState.Item(uid: "b", title: "Late joiner", start: now.addingTimeInterval(-3 * 60)),
        MeetingsState.Item(uid: "c", title: "Long gone", start: now.addingTimeInterval(-30 * 60)),
        MeetingsState.Item(uid: "d", title: "Next week", start: now.addingTimeInterval(40 * 3600)),
        MeetingsState.Item(uid: "e", title: "1:1", start: now.addingTimeInterval(6 * 3600)),
    ]
    let reminders = [
        UpcomingItem(id: "r1", kind: .reminder, title: "Call the bank", date: now.addingTimeInterval(4 * 3600)),
        UpcomingItem(id: "r2", kind: .reminder, title: "Pay rent", date: now.addingTimeInterval(20 * 3600)),
        UpcomingItem(id: "r3", kind: .reminder, title: "Water plants", date: now.addingTimeInterval(30 * 3600)),
    ]
    let items = UpcomingItem.merge(meetings: meetings, reminders: reminders, now: now)
    #expect(items.map(\.title) == ["Late joiner", "Standup", "Call the bank", "1:1", "Pay rent"])
    #expect(items[1].kind == .meeting)
    #expect(items[2].kind == .reminder)
    #expect(UpcomingItem.merge(meetings: [], reminders: [], now: now).isEmpty)
}

// MARK: Agents

@Test func agentsTableSortsByAttentionThenRecency() {
    let s: [Session] = [
        decode(#"{"source":"m4","tool":"claude","session":"1","state":"running","updated_at":"2026-09-26T10:00:00Z"}"#),
        decode(#"{"source":"m5","tool":"codex","session":"2","state":"waiting","updated_at":"2026-09-26T09:00:00Z"}"#),
        decode(#"{"source":"m4","tool":"codex","session":"3","state":"running","updated_at":"2026-09-26T10:05:00Z"}"#),
        decode(#"{"source":"m4","tool":"claude","session":"4","state":"done","updated_at":"2026-09-26T10:09:00Z"}"#),
    ]
    let t = AgentsTable(sessions: s)
    #expect(t.rows.map(\.session.session) == ["2", "3", "1", "4"])
    #expect(t.running == 2)
    #expect(t.waiting == 1)
    #expect(t.rows[0].id == "m5|codex|2")
    #expect(AgentsTable(sessions: []).isEmpty)
}

// MARK: Clock health

@Test func clockHealthThresholds() {
    #expect(ClockHealthReadout.wifi(rssi: -74).weak == false)
    #expect(ClockHealthReadout.wifi(rssi: -81).weak)
    #expect(ClockHealthReadout.wifi(rssi: -50).strength == 1)
    #expect(ClockHealthReadout.wifi(rssi: -95).strength == 0)
    #expect(ClockHealthReadout.wifi(rssi: -70).strength == 0.5)
    #expect(ClockHealthReadout.batterySymbol(percent: 87) == "battery.75")
    #expect(ClockHealthReadout.batterySymbol(percent: 95) == "battery.100")
    #expect(ClockHealthReadout.batterySymbol(percent: 5) == "battery.0")
    #expect(ClockHealthReadout.publishIsPoor(0.94))
    #expect(!ClockHealthReadout.publishIsPoor(0.994))
}

@Test func clockHealthPublishRatio() {
    let p: ClockHealth.Publish = decode(#"{"counting_since":"2026-09-26T00:00:00Z","ok_24h":0,"fail_24h":0,"success_ratio_24h":null,"ok_total":0,"fail_total":0,"retries_total":0,"last_at":null,"last_ok":false}"#)
    #expect(ClockHealthReadout.publishRatio(p) == nil)
    var q = p
    q.ok24h = 3; q.fail24h = 1
    #expect(ClockHealthReadout.publishRatio(q) == 0.75)
    q.successRatio24h = 0.7
    #expect(ClockHealthReadout.publishRatio(q) == 0.7)
}

// MARK: Weather

@Test func airLevelsFollowTheEuropeanBands() {
    #expect(WeatherReadout.AirLevel(europeanAQI: 0) == .good)
    #expect(WeatherReadout.AirLevel(europeanAQI: 32) == .fair)
    #expect(WeatherReadout.AirLevel(europeanAQI: 42) == .moderate)
    #expect(WeatherReadout.AirLevel(europeanAQI: 79.9) == .poor)
    #expect(WeatherReadout.AirLevel(europeanAQI: 99) == .veryPoor)
    #expect(WeatherReadout.AirLevel(europeanAQI: 140) == .extremelyPoor)
}

@Test func temperatureFollowsTheServersUnits() {
    #expect(WeatherReadout.temperature(celsius: 20, units: "metric").value == 20)
    #expect(WeatherReadout.temperature(celsius: 20, units: "metric").unit == .celsius)
    let f = WeatherReadout.temperature(celsius: 20, units: "imperial")
    #expect(f.unit == .fahrenheit)
    #expect(abs(f.value - 68) < 0.001)
}

@Test func upcomingHoursStartAtTheCurrentHour() {
    let fmt = ISO8601DateFormatter()
    let points: [WeatherState.TempPoint] = (0..<30).map {
        decode(#"{"time":"\#(fmt.string(from: now.addingTimeInterval(Double($0 - 3) * 3600)))","temp_c":\#($0)}"#)
    }
    let next = WeatherReadout.upcomingHours(points, from: now.addingTimeInterval(600))
    #expect(next.count == 12)
    #expect(next.first?.time == now)
    #expect(WeatherReadout.upcomingHours(Array(points.prefix(4)), from: now).isEmpty)
}
