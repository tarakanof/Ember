import Testing
import Foundation
@testable import EmberKit

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(T.self, from: Data(json.utf8))
}

// Shape captured from the live server's GET /v1/pomodoro/stats.
private let fullStats = #"""
{"today":{"date":"2026-09-25","completed_focus":3,"focus_min":75},
 "history":[{"date":"2026-09-25","completed_focus":3,"focus_min":75}],
 "streak":2,"longest_streak":9,
 "completion":{"completed_focus":10,"abandoned_focus":2,"total_focus":12,"completion_rate":0.8333,"focus_sec":15000},
 "goal":{"daily_sessions":8,"today_completed":3,"daily_met":false,"weekly_days":5,"week_active_days":4,"weekly_met":false},
 "weekly":[{"key":"2026-W38","focus_min":310,"sessions":12},{"key":"2026-W39","focus_min":75,"sessions":3}]}
"""#

@Test func pomoStatsDecodesTheRichPayload() throws {
    let s = try decode(PomoStats.self, fullStats)
    #expect(s.longestStreak == 9)
    #expect(s.completion.abandonedFocus == 2)
    #expect(s.completion.completionRate == 0.8333)
    #expect(s.goal.dailySessions == 8)
    #expect(s.goal.weekActiveDays == 4)
    #expect(!s.goal.dailyMet)
    #expect(s.weekly.map(\.key) == ["2026-W38", "2026-W39"])
    #expect(s.weekly.last?.focusMin == 75)
}

@Test func pomoStatsFromAnOlderServerGetsDefaults() throws {
    let s = try decode(PomoStats.self, #"{"today":{"date":"d","completed_focus":1,"focus_min":25},"history":[],"streak":4}"#)
    #expect(s.longestStreak == 4)
    #expect(s.completion == CompletionStat())
    #expect(s.goal.dailyMet)
    #expect(s.weekly.isEmpty)
}

private let pomoConfigJSON = #"""
{"enabled":true,"focus_minutes":25,"short_break_minutes":5,"long_break_minutes":15,
 "rounds_before_long_break":4,"auto_start_next":false,"sound":true,"sound_melody":"m",
 "focus_color":"#FF0000","break_color":"#00FF00","max_session_minutes":480}
"""#

@Test func pomoConfigGoalsDefaultWhenTheServerOmitsThem() throws {
    let c = try decode(PomoConfig.self, pomoConfigJSON)
    #expect(c.dailyGoalSessions == 8)
    #expect(c.weeklyGoalDays == 5)
    #expect(c.focusMinutes == 25)
}

@Test func pomoConfigGoalsRoundTrip() throws {
    var c = try decode(PomoConfig.self, pomoConfigJSON)
    c.dailyGoalSessions = 6
    c.weeklyGoalDays = 0
    let data = try JSONEncoder().encode(c)
    let obj = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    #expect(obj["daily_goal_sessions"] as? Int == 6)
    #expect(obj["weekly_goal_days"] as? Int == 0)
    #expect(try JSONDecoder().decode(PomoConfig.self, from: data) == c)
}

@Test func pomoConfigToleratesAnOmittedMelody() throws {
    let json = pomoConfigJSON.replacingOccurrences(of: #""sound_melody":"m","#, with: "")
    #expect(try decode(PomoConfig.self, json).soundMelody == "")
}

@Test func heatmapDecodesAndSendsDays() async throws {
    let q = LockedBox()
    let grid = Array(repeating: Array(repeating: 0, count: 24), count: 7)
    var g = grid
    g[2][16] = 25
    let body = try JSONSerialization.data(withJSONObject: [
        "grid": g, "calendar": [["key": "2026-08-04", "focus_min": 50, "sessions": 2]], "days": 84,
    ] as [String: Any])
    let client = stubbedClient { req in
        guard req.url?.path == "/v1/pomodoro/heatmap" else { return (okResponse(req.url!, status: 404), Data()) }
        if let s = req.url?.query { q.add(s) }
        return (okResponse(req.url!), body)
    }
    let h = try await StatsService(client: client).heatmap(days: 84)
    #expect(q.paths == ["days=84"])
    #expect(h.minutes(weekday: 2, hour: 16) == 25)
    #expect(h.minutes(weekday: 9, hour: 0) == 0)
    #expect(h.calendar.first?.sessions == 2)
    #expect(h.days == 84)
}

@Test func statsServiceReadsStats() async throws {
    let client = stubbedClient { req in
        guard req.url?.path == "/v1/pomodoro/stats" else { return (okResponse(req.url!, status: 404), Data()) }
        return (okResponse(req.url!), Data(fullStats.utf8))
    }
    #expect(try await StatsService(client: client).stats().longestStreak == 9)
}

@Test(arguments: [
    ("focus", "Focus"), ("short_break", "Short Break"), ("long_break", "Long Break"),
    ("idle", "Idle"), ("", "Idle"), ("deep_work", "Deep Work"),
])
func phaseDisplayNames(wire: String, name: String) {
    #expect(PomoPhase(wire: wire).displayName.text == name)
}

@Test func shortBreakNeverRendersWithAnUnderscore() {
    // Regression for audit #10: `"short_break".capitalized` is "Short_Break".
    #expect(!PomoPhase(wire: "short_break").displayName.text.contains("_"))
    #expect(PomoPhase(wire: "short_break").isBreak)
    #expect(!PomoPhase.focus.isBreak)
}

private func pomo(_ phase: String, running: Bool, paused: Bool) -> PomoState {
    PomoState(phase: phase, running: running, paused: paused, remainingSec: 60, plannedSec: 300, round: 1)
}

@Test func pomoModeCoversEveryCombination() {
    #expect(pomo("idle", running: false, paused: false).mode == .idle)
    #expect(pomo("focus", running: true, paused: false).mode == .running)
    #expect(pomo("focus", running: true, paused: true).mode == .paused)
    #expect(pomo("focus", running: false, paused: true).mode == .paused)
    #expect(pomo("short_break", running: false, paused: false).mode == .parked)
}

@Test func sessionStateMapsWireStrings() {
    #expect(Session.State(wire: "running") == .running)
    #expect(Session.State(wire: "") == .idle)
    #expect(Session.State(wire: "thinking") == .unknown("thinking"))
    #expect(Session.State(wire: "thinking").displayName.text == "Thinking")
    #expect(Session.State.waiting.displayName.text == "Waiting")
    #expect(Session.State.running.wire == "running")
    #expect(Session.State.error.color == stateColorRGB("error"))
}

@Test func sessionStateSortRankMatchesPickWinning() {
    let ranked: [Session.State] = [.done, .idle, .running, .error, .waiting]
    #expect(ranked.sorted { $0.sortRank < $1.sortRank } == [.waiting, .error, .running, .done, .idle])
}

@Test func snapshotEquatesBySessions() throws {
    let a = try decode(Snapshot.self, #"{"sessions":[{"tool":"claude","state":"running"}]}"#)
    let b = try decode(Snapshot.self, #"{"sessions":[{"tool":"claude","state":"running"}]}"#)
    #expect(a == b)
    #expect(a.sessions.first?.stateEnum == .running)
    #expect(Snapshot(sessions: []) != a)
}

@Test func weatherSymbolFollowsConditionAndNight() throws {
    let json = #"""
    {"generated_at":"2026-09-26T12:00:00Z","enabled":true,"provider":"open-meteo","units":"metric",
     "current":{"fetched_at":"2026-09-26T12:00:00Z","stale":false,"condition":"clear","severe":false,"temp_c":14,"hourly":[]},
     "sun":{"sunrise":"2026-09-26T05:30:00Z","sunset":"2026-09-26T17:10:00Z"}}
    """#
    var w = try decode(WeatherState.self, json)
    let noon = try Date("2026-09-26T12:00:00Z", strategy: .iso8601)
    let late = try Date("2026-09-26T22:00:00Z", strategy: .iso8601)
    #expect(w.sfSymbol(at: noon) == "sun.max.fill")
    #expect(w.sfSymbol(at: late) == "moon.stars.fill")
    w.current?.condition = "storm"
    #expect(w.sfSymbol(at: noon) == "cloud.bolt.rain.fill")
    w.current = nil
    #expect(w.sfSymbol(at: noon) == "questionmark.circle")
}
