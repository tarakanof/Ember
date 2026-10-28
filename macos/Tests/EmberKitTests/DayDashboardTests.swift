import Testing
import Foundation
@testable import EmberKit

private func calendar() -> Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "Europe/Amsterdam")!
    c.locale = Locale(identifier: "en_US")
    c.firstWeekday = 2
    return c
}

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

private func decode<T: Decodable>(_ json: String) async throws -> T {
    let client = stubbedClient { req in (okResponse(req.url!), Data(json.utf8)) }
    return try await client.get("/")
}

private let stateJSON = #"""
{"enabled":true,"state":"break","started":"10:05","stop_at":"00:00","on_min":150,"break_min":15,"replies_off_last_night":7}
"""#

private let historyJSON = #"""
{"days":[
{"date":"2026-10-27","start":"2026-10-27T10:05:00+01:00","stop":null,"stop_target":"00:00","on_sec":9000,"break_sec":900,
 "breaks":[{"start":"2026-10-27T12:00:00+01:00","end":"2026-10-27T12:15:00+01:00"}],"auto":false,"in_range":null,"replies_on":30,"replies_off":0,
 "first_reply":"2026-10-27T10:06:00+01:00","last_reply":"2026-10-27T13:00:00+01:00"},
{"date":"2026-10-26","start":"2026-10-26T11:00:00+01:00","stop":"2026-10-27T00:20:00+01:00","stop_target":"2026-10-27T00:00:00+01:00","on_sec":45000,"break_sec":3600,
 "breaks":[{"start":"2026-10-26T15:00:00+01:00","end":"2026-10-26T16:00:00+01:00"}],"auto":true,"in_range":false,"replies_on":210,"replies_off":12,
 "first_reply":"2026-10-26T11:02:00+01:00","last_reply":"2026-10-27T00:50:00+01:00",
 "replies_off_at":["2026-10-27T00:30:00+01:00","2026-10-27T00:50:00+01:00"]},
{"date":"2026-10-25","start":null,"stop":null,"stop_target":null,"on_sec":0,"break_sec":0,"auto":false,"in_range":null,
 "replies_on":0,"replies_off":3,"first_reply":"2026-10-25T21:00:00+01:00","last_reply":"2026-10-25T22:10:00+01:00"}
]}
"""#

@Test func dayStateDecodes() async throws {
    let s: DayState = try await decode(stateJSON)
    #expect(s.phase == .onBreak)
    #expect(s.started == "10:05")
    #expect(s.stopAt == "00:00")
    #expect(s.onMin == 150 && s.breakMin == 15)
    #expect(s.repliesOffLastNight == 7)
}

@Test func dayStateToleratesOffAndUnknownPhases() async throws {
    let off: DayState = try await decode(#"{"state":"off","started":""}"#)
    #expect(off.phase == .off && off.started == nil && off.enabled && off.repliesOffLastNight == 0)
    let odd: DayState = try await decode(#"{"state":"paused"}"#)
    #expect(odd.phase == .off)
}

@Test func dayHistoryDecodes() async throws {
    let h: DayHistory = try await decode(historyJSON)
    #expect(h.days.count == 3)
    let night = h.days[1]
    #expect(night.auto && night.inRange == false && night.repliesOff == 12)
    #expect(night.breaks.count == 1 && night.repliesOffAt.count == 2)
    #expect(h.days[0].stop == nil && h.days[0].inRange == nil)
    #expect(h.days[2].start == nil)
}

@Test func dayWeeksDecodeByKeyOrStart() async throws {
    let w: DayWeeks = try await decode(#"""
    {"weeks":[{"week":"2026-W44","days_worked":4,"days_in_range":2,"on_sec":144000,"break_sec":7200,"replies_off":19},
              {"start":"2026-10-19","days_worked":5}]}
    """#)
    #expect(w.weeks[0].key == "2026-W44" && w.weeks[0].daysInRange == 2 && w.weeks[0].repliesOff == 19)
    #expect(w.weeks[1].key == "2026-10-19" && w.weeks[1].onSec == 0)
    #expect(w.weeks[0].breakSec == 7200 && w.weeks[1].breakSec == nil)
}

@Test func dayConfigRoundTrips() throws {
    let data = try JSONEncoder().encode(DayConfig(defaultStop: "23:30", breakHintMinutes: 0))
    let object = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    #expect(object["default_stop"] as? String == "23:30")
    #expect(object["break_hint_minutes"] as? Int == 0)
    #expect(Set(object.keys) == ["enabled", "default_stop", "break_hint_minutes", "auto_stop_minutes", "grace_minutes"])
    #expect(try JSONDecoder().decode(DayConfig.self, from: data).defaultStop == "23:30")
}

private func strip(_ now: String = "2026-10-27T13:30:00+01:00") async throws -> DayStrip {
    let h: DayHistory = try await decode(historyJSON)
    return DayStrip(days: h.days, now: iso(now), calendar: calendar())
}

@Test func stripPlacesBarsOnTheSixToFourAxis() async throws {
    let s = try await strip()
    #expect(s.rows.map(\.key) == ["2026-10-25", "2026-10-26", "2026-10-27"])
    let night = s.rows[1]
    #expect(night.start == 11 && night.stop == 24 + 1.0 / 3)
    #expect(night.target == 24)
    #expect(night.spans == [.init(from: 11, to: 15), .init(from: 16, to: 24 + 1.0 / 3)])
    #expect(night.dots == [24.5, 24 + 50.0 / 60])
    #expect(night.auto && night.inRange == false)
}

@Test func stripKeepsOffClockRepliesOnADayWithoutAClockIn() async throws {
    let day = try await strip().rows[0]
    #expect(!day.hasClock && day.spans.isEmpty)
    #expect(day.repliesOff == 3)
    #expect(day.dots == [21, 22 + 10.0 / 60])
}

@Test func stripDoesNotGuessOffClockDotsInsideTheClockedSpan() {
    let json = #"{"date":"2026-10-27","start":"2026-10-27T09:00:00+01:00","stop":"2026-10-27T18:00:00+01:00","replies_off":2,"first_reply":"2026-10-27T08:00:00+01:00","last_reply":"2026-10-27T17:00:00+01:00"}"#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let day = try! d.decode(DayHistory.Day.self, from: Data(json.utf8))
    let row = DayStrip(days: [day], now: iso("2026-10-27T20:00:00+01:00"), calendar: calendar()).rows[0]
    #expect(row.dots == [8])
    #expect(row.repliesOff == 2)
}

@Test func stripExtendsAnOpenTodayToNowAndSplitsAtBreaks() async throws {
    let today = try await strip().rows[2]
    #expect(today.isToday && today.stop == nil)
    #expect(today.target == 24)
    #expect(today.spans == [.init(from: 10 + 5.0 / 60, to: 12), .init(from: 12.25, to: 13.5)])
}

@Test func stripResolvesClockTargetsPastMidnightAndEmphasisesWeekends() async throws {
    let cal = calendar()
    let midnight = DayKey.date("2026-10-26", in: cal)!
    #expect(DayStrip.resolveTarget("00:00", midnight: midnight, calendar: cal) == 24)
    #expect(DayStrip.resolveTarget("01:30", midnight: midnight, calendar: cal) == 25.5)
    #expect(DayStrip.resolveTarget("04:00", midnight: midnight, calendar: cal) == 28)
    #expect(DayStrip.resolveTarget("22:45", midnight: midnight, calendar: cal) == 22.75)
    #expect(DayStrip.resolveTarget("soon", midnight: midnight, calendar: cal) == nil)
    let s = try await strip()
    #expect(s.rows.map(\.isWeekend) == [true, false, false])
}

@Test func stripClampsToTheAxisAndKeepsTheLastWindow() {
    let cal = calendar()
    let early = DayHistory.Day.sample("2026-10-20", start: "2026-10-20T05:00:00+02:00", stop: "2026-10-21T05:30:00+02:00")
    let rows = DayStrip(days: [early], now: iso("2026-10-21T08:00:00+02:00"), calendar: cal).rows
    #expect(rows[0].spans == [.init(from: 6, to: 28)])
    let many = (1...40).map { DayHistory.Day.sample(String(format: "2026-09-%02d", min($0, 30)) + ($0 > 30 ? "" : "")) }
    #expect(DayStrip(days: Array(many.prefix(30)), count: 28, now: .now, calendar: cal).rows.count == 28)
}

@Test func stopTrendComparesStopsWithTargets() async throws {
    let trend = StopTrend(strip: try await strip())
    #expect(trend.points.map(\.key) == ["2026-10-26"])
    #expect(trend.averageMinutesPastTarget == 20)
    #expect(trend.hourDomain == 23...27)
    #expect(StopTrend(strip: DayStrip(days: [], now: .now, calendar: calendar())).isEmpty)
}

@Test func weekTableOrdersNewestFirstAndFindsThisWeek() async throws {
    let w: DayWeeks = try await decode(#"""
    {"weeks":[{"week":"2026-W42","days_worked":5,"days_in_range":1,"on_sec":1,"replies_off":9},
              {"week":"2026-W44","days_worked":2,"days_in_range":2,"on_sec":7200,"replies_off":0},
              {"week":"2026-W43","days_worked":6,"days_in_range":3,"on_sec":2,"replies_off":4}]}
    """#)
    let table = DayWeekTable(weeks: w.weeks, count: 2, now: iso("2026-10-28T09:00:00+01:00"), calendar: calendar())
    #expect(table.rows.map(\.key) == ["2026-W44", "2026-W43"])
    #expect(table.thisWeek?.daysInRange == 2 && table.thisWeek?.daysWorked == 2)
    #expect(!table.hasBreaks)
    let afterMidnight = DayWeekTable(weeks: w.weeks, now: iso("2026-11-02T01:30:00+01:00"), calendar: calendar())
    #expect(afterMidnight.thisWeek?.key == "2026-W44")
    let later = DayWeekTable(weeks: w.weeks, now: iso("2026-11-09T09:00:00+01:00"), calendar: calendar())
    #expect(later.thisWeek == nil)
}

extension DayHistory.Day {
    static func sample(_ date: String, start: String? = nil, stop: String? = nil) -> DayHistory.Day {
        let s = start.map { "\"\($0)\"" } ?? "null", e = stop.map { "\"\($0)\"" } ?? "null"
        let json = #"{"date":"\#(date)","start":\#(s),"stop":\#(e)}"#
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return try! d.decode(DayHistory.Day.self, from: Data(json.utf8))
    }
}
