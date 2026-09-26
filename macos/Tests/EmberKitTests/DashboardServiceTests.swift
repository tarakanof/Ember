import Testing
import Foundation
@testable import EmberKit

// Fixtures are real server output (cmd/ember/dashboard_http.go), captured from
// the Go handlers, so these tests pin the wire contract rather than a guess.

private let usageJSON = #"""
{"generated_at":"2026-09-26T08:44:12+02:00","stale_after_sec":600,"tools":[{"tool":"claude","source":"m4","updated_at":"2026-09-26T08:44:12+02:00","stale":false,"five_hour":{"used_percent":14,"resets_at":"2026-09-26T04:20:00+02:00","reset_label":"04:20"},"seven_day":{"used_percent":37.5,"resets_at":null},"models":[{"model":"opus","used_percent":5,"resets_at":null}]}]}
"""#

private let activityJSON = #"""
{"generated_at":"2026-09-26T08:44:12+02:00","recording":true,"days":2,"span_gap_sec":300,"today":{"from":"2026-09-26T04:00:00+02:00","to":"2026-09-26T08:44:12+02:00","total":{"active_sec":600,"sessions":1,"attention":1},"by_tool":[{"key":"claude","active_sec":600,"sessions":1,"attention":1}],"by_source":[{"key":"m4","active_sec":600,"sessions":1,"attention":1}]},"period":{"from":"2026-09-25T04:00:00+02:00","to":"2026-09-26T08:44:12+02:00","total":{"active_sec":600,"sessions":1,"attention":1},"by_tool":[{"key":"claude","active_sec":600,"sessions":1,"attention":1}],"by_source":[{"key":"m4","active_sec":600,"sessions":1,"attention":1}]},"daily":[{"day":"2026-09-25","date":"2026-09-25T00:00:00+02:00","tool":"claude","active_sec":0},{"day":"2026-09-26","date":"2026-09-26T00:00:00+02:00","tool":"claude","active_sec":600}]}
"""#

private let weatherJSON = #"""
{"generated_at":"2026-09-26T08:44:12+02:00","enabled":true,"provider":"open-meteo","units":"metric","current":{"fetched_at":"2026-09-26T08:39:12+02:00","stale":false,"condition":"rain","severe":false,"temp_c":11.5,"hourly":[{"time":"2026-09-26T08:00:00+02:00","temp_c":11.5},{"time":"2026-09-26T09:00:00+02:00","temp_c":12}]},"air":{"fetched_at":"2026-09-26T08:39:12+02:00","stale":false,"european_aqi":42,"pm2_5_ugm3":8,"pm10_ugm3":15,"hourly":[{"time":"2026-09-26T08:00:00+02:00","european_aqi":42},{"time":"2026-09-26T09:00:00+02:00","european_aqi":40}]},"sun":{"sunrise":"2026-09-26T05:32:27Z","sunset":"2026-09-26T17:31:12Z"}}
"""#

private let weatherEmptyJSON = #"""
{"generated_at":"2026-09-26T08:44:12+02:00","enabled":false,"provider":"open-meteo","units":"metric","current":null,"air":null,"sun":null}
"""#

private let clockJSON = #"""
{"generated_at":"2026-09-26T08:44:12+02:00","publish":{"counting_since":"2026-09-26T08:44:12+02:00","ok_total":2,"fail_total":1,"retries_total":0,"success_ratio":0.6666666666666666,"last_at":"2026-09-26T06:44:12Z","last_ok":true},"device":{"reachable":true,"checked_at":"2026-09-26T08:44:12+02:00","firmware":"1.1.2","uptime_sec":268719,"free_heap_bytes":103032,"min_free_heap_bytes":76544,"wifi_rssi_dbm":-71,"wifi_connects":3,"reset_reason":"software","fps":42,"battery_percent":97,"temperature_c":33.4,"humidity_percent":21.4},"last_button_at":"2026-09-26T08:44:12+02:00"}
"""#

private let clockUnreachableJSON = #"""
{"generated_at":"2026-09-26T08:44:12+02:00","publish":{"counting_since":"2026-09-26T08:44:12+02:00","ok_total":0,"fail_total":0,"retries_total":0,"success_ratio":null,"last_at":null,"last_ok":false},"device":{"reachable":false,"checked_at":"2026-09-26T08:44:12+02:00","uptime_sec":null,"free_heap_bytes":null,"min_free_heap_bytes":null,"wifi_rssi_dbm":null,"wifi_connects":null,"fps":null,"battery_percent":null,"temperature_c":null,"humidity_percent":null},"last_button_at":null}
"""#

private let workHoursJSON = #"""
{"days":[{"date":"2026-09-26","work_start":"2026-09-26T04:00:00+02:00","work_end":"2026-09-26T08:34:12+02:00","span_sec":16452,"active_sec":2100,"break_sec":14352,"sessions":2,"longest_sec":1500},{"date":"2026-09-25","work_start":null,"work_end":null,"span_sec":0,"active_sec":0,"break_sec":0,"sessions":0,"longest_sec":0}],"gap_min":15,"include_activity":true}
"""#

/// A DashboardService whose server answers `json` for `path` (and 404 otherwise),
/// recording the query string it was asked for.
private func service(path: String, json: String, query: LockedBox? = nil) -> DashboardService {
    let client = stubbedClient { req in
        guard req.url?.path == path else { return (okResponse(req.url!, status: 404), Data()) }
        if let q = req.url?.query { query?.add(q) }
        return (okResponse(req.url!), Data(json.utf8))
    }
    return DashboardService(client: client)
}

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

@Test func dashboardDecodesUsageSnapshot() async throws {
    let u = try await service(path: "/v1/usage", json: usageJSON).usage()
    #expect(u.staleAfterSec == 600)
    let claude = try #require(u.tools.first)
    #expect(claude.id == "claude")
    #expect(claude.source == "m4")
    #expect(claude.fiveHour?.usedPercent == 14)
    #expect(claude.fiveHour?.resetsAt == iso("2026-09-26T04:20:00+02:00"))
    #expect(claude.sevenDay?.resetsAt == nil)
    #expect(claude.models.map(\.model) == ["opus"])
}

@Test func dashboardDecodesActivitySummaryAndSendsDays() async throws {
    let q = LockedBox()
    let a = try await service(path: "/v1/activity/summary", json: activityJSON, query: q).activitySummary(days: 2)
    #expect(q.paths == ["days=2"])
    #expect(a.recording)
    #expect(a.today.total.key == nil)
    #expect(a.today.total.activeSec == 600)
    #expect(a.today.total.attention == 1)
    #expect(a.period.byTool.first?.key == "claude")
    #expect(a.period.bySource.first?.key == "m4")
    #expect(a.daily.map(\.id) == ["2026-09-25|claude", "2026-09-26|claude"])
    #expect(a.daily.last?.date == iso("2026-09-26T00:00:00+02:00"))
}

@Test func dashboardDecodesWeatherState() async throws {
    let w = try await service(path: "/v1/weather/state", json: weatherJSON).weatherState()
    let cur = try #require(w.current)
    #expect(cur.condition == "rain")
    #expect(cur.tempC == 11.5)
    #expect(cur.hourly.count == 2)
    #expect(cur.hourly[1].time.timeIntervalSince(cur.hourly[0].time) == 3600)
    #expect(w.air?.europeanAqi == 42)
    #expect(w.air?.pm25Ugm3 == 8)
    #expect(w.sun?.sunrise == iso("2026-09-26T05:32:27Z"))
}

@Test func dashboardDecodesWeatherStateBeforeFirstFetch() async throws {
    let w = try await service(path: "/v1/weather/state", json: weatherEmptyJSON).weatherState()
    #expect(!w.enabled)
    #expect(w.current == nil)
    #expect(w.air == nil)
    #expect(w.sun == nil)
}

@Test func dashboardDecodesClockHealth() async throws {
    let h = try await service(path: "/v1/clock/health", json: clockJSON).clockHealth()
    #expect(h.publish.okTotal == 2)
    #expect(h.publish.failTotal == 1)
    #expect(h.publish.successRatio != nil)
    #expect(h.publish.lastAt == iso("2026-09-26T06:44:12Z"))
    let dev = try #require(h.device)
    #expect(dev.reachable)
    #expect(dev.wifiRssiDbm == -71)
    #expect(dev.freeHeapBytes == 103032)
    #expect(dev.uptimeSec == 268719)
    #expect(dev.wifiConnects == 3)
    #expect(dev.temperatureC == 33.4)
}

@Test func dashboardDecodesUnreachableClock() async throws {
    let h = try await service(path: "/v1/clock/health", json: clockUnreachableJSON).clockHealth()
    #expect(h.publish.successRatio == nil)
    #expect(h.publish.lastAt == nil)
    #expect(h.lastButtonAt == nil)
    let dev = try #require(h.device)
    #expect(!dev.reachable)
    #expect(dev.firmware == nil)
    #expect(dev.wifiRssiDbm == nil)
}

@Test func dashboardDecodesWorkHoursWithEmptyDay() async throws {
    let q = LockedBox()
    let wh = try await service(path: "/v1/pomodoro/workhours", json: workHoursJSON, query: q).workHours(days: 2)
    #expect(q.paths == ["days=2"])
    #expect(wh.includeActivity)
    #expect(wh.days.first?.workStart == iso("2026-09-26T04:00:00+02:00"))
    let empty = try #require(wh.days.last)
    #expect(empty.workStart == nil)
    #expect(empty.workEnd == nil)
    #expect(empty.activeSec == 0)
}
