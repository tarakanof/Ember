import Testing
import Foundation
@testable import EmberKit

// Chart transforms for the Dashboard's Pomodoro and agent cards. Every test
// pins the calendar (Amsterdam, which has DST) so none depends on the Mac
// running them.

private func calendar(_ tz: String = "Europe/Amsterdam", firstWeekday: Int = 2) -> Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: tz)!
    c.locale = Locale(identifier: "en_US")
    c.firstWeekday = firstWeekday
    return c
}

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

private func day(_ key: String, _ sessions: Int, _ minutes: Int) -> PomoDayStat {
    try! JSONDecoder().decode(PomoDayStat.self, from: Data(
        #"{"date":"\#(key)","completed_focus":\#(sessions),"focus_min":\#(minutes)}"#.utf8))
}

private func stats(history: [PomoDayStat], goal: Int = 4, weekly: [FocusBucket] = []) -> PomoStats {
    PomoStats(today: history[0], history: history, streak: 2, longestStreak: 5,
              completion: CompletionStat(completedFocus: 9, abandonedFocus: 1, totalFocus: 10,
                                         completionRate: 0.9, focusSec: 13_500),
              goal: GoalStatus(dailySessions: goal, todayCompleted: history[0].completedFocus),
              weekly: weekly)
}

private let week: [PomoDayStat] = [   // newest first, as the server sends it
    day("2026-09-26", 3, 75), day("2026-09-25", 0, 0), day("2026-09-24", 5, 125),
    day("2026-09-23", 2, 50), day("2026-09-22", 0, 0), day("2026-09-21", 1, 25), day("2026-09-20", 0, 0),
]

// MARK: Last 7 days

@Test func weekBarsAreOldestFirstWithTodayLast() {
    let bars = WeekBars(stats: stats(history: week), focusMinutes: 25, calendar: calendar())
    #expect(bars.bars.map(\.key) == ["2026-09-20", "2026-09-21", "2026-09-22", "2026-09-23",
                                     "2026-09-24", "2026-09-25", "2026-09-26"])
    #expect(bars.bars.map(\.isToday) == [false, false, false, false, false, false, true])
    #expect(bars.bars.last?.date == iso("2026-09-26T00:00:00+02:00"))
    #expect(bars.totalMinutes == 275)
    #expect(!bars.isEmpty)
}

@Test func weekBarsGoalLineIsSessionsTimesFocusLength() {
    let bars = WeekBars(stats: stats(history: week, goal: 4), focusMinutes: 25, calendar: calendar())
    #expect(bars.goalMinutes == 100)
    #expect(bars.daysAtGoal == 1)
    // The tallest bar (125) with 15 % headroom, rounded up to 30.
    #expect(bars.yMax == 150)
    #expect(WeekBars(stats: stats(history: week, goal: 0), focusMinutes: 25, calendar: calendar()).goalMinutes == nil)
    #expect(WeekBars(stats: stats(history: week, goal: 4), focusMinutes: nil, calendar: calendar()).goalMinutes == nil)
}

@Test func weekBarsAxisNeverCollapsesOnSparseData() {
    let sparse = [day("2026-09-26", 1, 25)] + (1..<7).map { day("2026-09-\(26 - $0)", 0, 0) }
    let bars = WeekBars(stats: stats(history: sparse, goal: 0), focusMinutes: 25, calendar: calendar())
    #expect(bars.yMax == 60)
    let none = (0..<7).map { day("2026-09-\(26 - $0)", 0, 0) }
    #expect(WeekBars(stats: stats(history: none), focusMinutes: 25, calendar: calendar()).isEmpty)
    #expect(WeekBars.axisTop(0) == 60)
    #expect(WeekBars.axisTop(200) == 240)
}

// MARK: 12 weeks

@Test func weeklyTrendZeroFillsAndEndsThisWeek() {
    let weekly = [FocusBucket(key: "2026-W30", focusMin: 100, sessions: 4),
                  FocusBucket(key: "2026-W37", focusMin: 300, sessions: 12),
                  FocusBucket(key: "2026-W39", focusMin: 50, sessions: 2)]
    let t = WeeklyTrend(weekly: weekly, now: iso("2026-09-26T10:00:00+02:00"), calendar: calendar())
    #expect(t.points.first?.key == "2026-W30")
    #expect(t.points.last?.key == "2026-W39")
    #expect(t.points.count == 10)
    #expect(t.points.map(\.focusMin).reduce(0, +) == 450)
    #expect(t.points.last?.weekStart == iso("2026-09-21T00:00:00+02:00"))
    #expect(t.averageMinutes == 150)
}

@Test func weeklyTrendKeepsFourWeeksForANewUser() {
    let t = WeeklyTrend(weekly: [FocusBucket(key: "2026-W39", focusMin: 50, sessions: 2)],
                        now: iso("2026-09-26T10:00:00+02:00"), calendar: calendar())
    #expect(t.points.map(\.key) == ["2026-W36", "2026-W37", "2026-W38", "2026-W39"])
    #expect(t.averageMinutes == nil)
    #expect(WeeklyTrend(weekly: [], now: .now, calendar: calendar()).isEmpty)
}

@Test func weeklyTrendCapsAtTwelveWeeksAcrossTheYearBoundary() {
    let weekly = [FocusBucket(key: "2025-W40", focusMin: 10, sessions: 1),   // outside the window
                  FocusBucket(key: "2025-W42", focusMin: 10, sessions: 1),
                  FocusBucket(key: "2025-W52", focusMin: 20, sessions: 1),
                  FocusBucket(key: "2026-W01", focusMin: 30, sessions: 1)]
    let t = WeeklyTrend(weekly: weekly, now: iso("2026-01-02T12:00:00+01:00"), calendar: calendar())
    #expect(t.points.count == 12)
    #expect(t.points.last?.key == "2026-W01")
    #expect(t.points.first?.key == "2025-W42")
    #expect(t.points.map(\.focusMin).reduce(0, +) == 60)
}

// MARK: Day keys and DST

@Test func dayKeysSurviveDSTChanges() {
    let cal = calendar()
    // 2026-10-25 is 25 hours long in Amsterdam, 2026-03-29 is 23.
    let days = DayKey.days(endingAt: iso("2026-10-26T12:00:00+01:00"), count: 4, in: cal)
    #expect(days.map { DayKey.key($0, in: cal) } == ["2026-10-23", "2026-10-24", "2026-10-25", "2026-10-26"])
    #expect(days[2] == iso("2026-10-25T00:00:00+02:00"))
    #expect(days[3] == iso("2026-10-26T00:00:00+01:00"))
    let spring = DayKey.days(endingAt: iso("2026-03-30T00:30:00+02:00"), count: 3, in: cal)
    #expect(spring.map { DayKey.key($0, in: cal) } == ["2026-03-28", "2026-03-29", "2026-03-30"])
    #expect(DayKey.date("2026-03-29", in: cal) == iso("2026-03-29T00:00:00+01:00"))
    #expect(DayKey.date("2026-02-30", in: cal) == nil)
    #expect(DayKey.date("junk", in: cal) == nil)
    #expect(DayKey.weekStart("2026-W53", in: cal) == iso("2026-12-28T00:00:00+01:00"))
    #expect(DayKey.weekStart("2026-W39", in: cal) == iso("2026-09-21T00:00:00+02:00"))
    #expect(DayKey.weekStart("2026-39", in: cal) == nil)
    #expect(DayKey.weekKey(iso("2027-01-01T12:00:00+01:00"), in: cal) == "2026-W53")
}

// MARK: Heatmap

private func grid(_ fill: (Int, Int) -> Int) -> Heatmap {
    Heatmap(grid: (0..<7).map { wd in (0..<24).map { h in fill(wd, h) } }, calendar: [], days: 84)
}

@Test func heatmapRowsFollowTheLocaleWeek() {
    let map = grid { wd, h in wd == 1 && h == 9 ? 50 : (wd == 0 && h == 20 ? 25 : 0) }
    let monday = HeatmapGrid(heatmap: map, calendar: calendar(firstWeekday: 2))
    #expect(monday.rowLabels == ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"])
    #expect(monday.cells.count == 168)
    #expect(monday.cells.first { $0.weekday == 1 && $0.hour == 9 }?.row == 0)
    #expect(monday.cells.first { $0.weekday == 0 && $0.hour == 20 }?.row == 6)
    #expect(monday.maxMinutes == 50)
    #expect(monday.peak?.weekday == 1 && monday.peak?.hour == 9)

    let sunday = HeatmapGrid(heatmap: map, calendar: calendar(firstWeekday: 1))
    #expect(sunday.rowLabels.first == "Sun")
    #expect(sunday.cells.first { $0.weekday == 0 && $0.hour == 20 }?.row == 0)
    #expect(HeatmapGrid.weekdayOrder(firstWeekday: 7) == [6, 0, 1, 2, 3, 4, 5])
}

@Test func heatmapToleratesAShortGrid() {
    let short = HeatmapGrid(heatmap: Heatmap(grid: [[1, 2]], calendar: [], days: 7), calendar: calendar())
    #expect(short.cells.count == 168)
    #expect(short.maxMinutes == 2)
    #expect(HeatmapGrid(heatmap: grid { _, _ in 0 }, calendar: calendar()).isEmpty)
    #expect(HeatmapGrid(heatmap: grid { _, _ in 0 }, calendar: calendar()).peak == nil)
}

@Test func calendarStripEndsTodayInWeekColumns() {
    let buckets = [FocusBucket(key: "2026-09-26", focusMin: 75, sessions: 3),
                   FocusBucket(key: "2026-07-06", focusMin: 25, sessions: 1),
                   FocusBucket(key: "2026-01-01", focusMin: 999, sessions: 9)]   // outside the window
    let strip = CalendarStrip(calendar: buckets, now: iso("2026-09-26T10:00:00+02:00"), in: calendar())
    // Saturday is row 5 of a Monday week: 11 full weeks plus 6 days.
    #expect(strip.cells.count == 11 * 7 + 6)
    #expect(strip.cells.first?.key == "2026-07-06")
    #expect(strip.cells.first?.row == 0)
    #expect(strip.cells.last?.key == "2026-09-26")
    #expect(strip.cells.last?.column == 11)
    #expect(strip.cells.last?.row == 5)
    #expect(strip.activeDays == 2)
    #expect(strip.maxMinutes == 75)
}

@Test func calendarStripUsesTheServersLogicalToday() {
    // 02:00 on the 27th is still the 26th for a 04:00 day start.
    let strip = CalendarStrip(calendar: [], today: "2026-09-26", now: iso("2026-09-27T02:00:00+02:00"),
                              in: calendar(), weeks: 2)
    #expect(strip.cells.last?.key == "2026-09-26")
    #expect(strip.isEmpty)
}

@Test func calendarStripCountsDaysAcrossDST() {
    let strip = CalendarStrip(calendar: [], now: iso("2026-11-01T12:00:00+01:00"), in: calendar(firstWeekday: 1), weeks: 2)
    let keys = strip.cells.map(\.key)
    #expect(keys == ["2026-10-25", "2026-10-26", "2026-10-27", "2026-10-28", "2026-10-29",
                     "2026-10-30", "2026-10-31", "2026-11-01"])
    #expect(strip.cells.map(\.row) == [0, 1, 2, 3, 4, 5, 6, 0])
    #expect(Set(keys).count == keys.count)
}

// MARK: Work hours

private func workDay(_ date: String, _ start: String?, _ end: String?, active: Int = 3600, sessions: Int = 2) -> String {
    let s = start.map { "\"\($0)\"" } ?? "null", e = end.map { "\"\($0)\"" } ?? "null"
    return #"{"date":"\#(date)","work_start":\#(s),"work_end":\#(e),"span_sec":0,"active_sec":\#(active),"break_sec":0,"sessions":\#(sessions),"longest_sec":0}"#
}

private func workHours(_ days: [String]) -> WorkHours {
    let json = #"{"days":[\#(days.joined(separator: ","))],"gap_min":30,"include_activity":true}"#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try! d.decode(WorkHours.self, from: Data(json.utf8))
}

@Test func workHoursSpansAreWallClockHours() {
    let wh = workHours([
        workDay("2026-09-26", "2026-09-26T09:30:00+02:00", "2026-09-26T12:00:00+02:00"),
        workDay("2026-09-25", "2026-09-25T08:00:00+02:00", "2026-09-26T01:15:00+02:00"),
        workDay("2026-09-24", nil, nil, active: 0, sessions: 0),
    ])
    let chart = WorkHoursChart(days: wh.days, now: iso("2026-09-26T11:00:00+02:00"), calendar: calendar())
    #expect(chart.rows.map(\.key) == ["2026-09-24", "2026-09-25", "2026-09-26"])
    #expect(chart.rows[2].start == 9.5)
    #expect(chart.rows[2].end == 12)
    #expect(chart.rows[2].isToday)
    #expect(chart.rows[1].end == 25.25)
    #expect(!chart.rows[0].hasWork)
    #expect(chart.hourDomain == 6...26)
    #expect(chart.nowHour == 11)
}

@Test func workHoursAreWallClockOnDSTDays() {
    // 25-hour day: 09:00 is ten hours after midnight but reads as 9.
    let wh = workHours([workDay("2026-10-25", "2026-10-25T09:00:00+01:00", "2026-10-25T17:30:00+01:00")])
    let chart = WorkHoursChart(days: wh.days, now: iso("2026-10-25T20:00:00+01:00"), calendar: calendar())
    #expect(chart.rows.last?.start == 9)
    #expect(chart.rows.last?.end == 17.5)
    #expect(chart.nowHour == 20)
    #expect(chart.hourDomain == 6...24)
}

@Test func workHoursTrimOldEmptyDaysButKeepAWeek() {
    var days = [workDay("2026-09-26", "2026-09-26T05:10:00+02:00", "2026-09-26T07:00:00+02:00")]
    days += (1..<14).map { workDay(String(format: "2026-09-%02d", 26 - $0), nil, nil, active: 0, sessions: 0) }
    let chart = WorkHoursChart(days: workHours(days).days, now: iso("2026-09-26T08:00:00+02:00"), calendar: calendar())
    #expect(chart.rows.count == WorkHoursChart.minimumRows)
    #expect(chart.rows.last?.key == "2026-09-26")
    #expect(chart.hourDomain == 5...24)
    #expect(!chart.isEmpty)
    let empty = WorkHoursChart(days: workHours(Array(days.dropFirst())).days, now: .now, calendar: calendar())
    #expect(empty.isEmpty)
    #expect(empty.rows.count == 7)
    #expect(empty.nowHour == nil)
}

@Test func workHoursDropHalfOpenSpans() {
    let wh = workHours([workDay("2026-09-26", "2026-09-26T09:00:00+02:00", nil)])
    #expect(!WorkHoursChart(days: wh.days, now: .now, calendar: calendar()).rows[0].hasWork)
}

// MARK: Agent time

private func golden(_ name: String) throws -> Data {
    try Data(contentsOf: URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/dashboard/\(name).json"))
}

private func activityGolden() throws -> ActivitySummary {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(ActivitySummary.self, from: golden("activity_summary"))
}

@Test func agentTimeStacksBySourceOverAZeroFilledWeek() throws {
    let chart = AgentTimeChart(summary: try activityGolden(), calendar: calendar())
    #expect(chart.days.count == 7)
    #expect(chart.days.last == iso("2026-09-26T00:00:00+02:00"))
    #expect(chart.days.first == iso("2026-09-20T00:00:00+02:00"))
    // m5 did 2400 s, m4 240 s over the period: m5 stacks first.
    #expect(chart.sources.map(\.name) == ["m5", "m4"])
    #expect(chart.sources.map(\.colorHex) == ["#FF8800", "#00C8C8"])
    #expect(chart.segments.count == 14)
    #expect(chart.sources[0].totalMinutes == 40)
    #expect(chart.todayMinutes == 14)
    #expect(chart.dailyTotals.last?.minutes == 14)
    #expect(chart.dailyTotals.first?.minutes == 0)
    #expect(!chart.isEmpty)
    #expect(chart.isRecording)
}

@Test func agentTimeFallsBackToStableColours() throws {
    var summary = try activityGolden()
    summary.dailyBySource = summary.dailyBySource.map { var r = $0; r.sourceColor = nil; return r }
    let chart = AgentTimeChart(summary: summary, calendar: calendar())
    #expect(chart.sources.map(\.colorHex) == Array(AgentTimeChart.fallbackColors.prefix(2)))
    summary.dailyBySource = summary.dailyBySource.map { var r = $0; r.activeSec = 0; return r }
    #expect(AgentTimeChart(summary: summary, calendar: calendar()).isEmpty)
}
