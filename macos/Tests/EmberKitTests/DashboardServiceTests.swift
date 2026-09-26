import Testing
import Foundation
@testable import EmberKit

// These decode the server's golden files (cmd/ember/testdata/dashboard), which
// TestDashboardGolden generates from the real Go builders. A renamed or
// retyped field on either side fails one of the two suites.

private func golden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()      // EmberKitTests
        .deletingLastPathComponent()      // Tests
        .deletingLastPathComponent()      // macos
        .deletingLastPathComponent()      // repo root
        .appendingPathComponent("cmd/ember/testdata/dashboard/\(name).json")
    return try Data(contentsOf: url)
}

/// An APIClient whose server answers `body` for `path` (404 otherwise),
/// recording each query string it was asked for.
private func client(path: String, body: Data, query: LockedBox? = nil) -> APIClient {
    stubbedClient { req in
        guard req.url?.path == path else { return (okResponse(req.url!, status: 404), Data()) }
        if let q = req.url?.query { query?.add(q) }
        return (okResponse(req.url!), body)
    }
}

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

@Test func usageSnapshotDecodesGolden() async throws {
    let svc = UsageService(client: client(path: "/v1/usage", body: try golden("usage")))
    let u = try await svc.snapshot()
    #expect(u.staleAfterSec == 600)
    #expect(u.tools.map(\.id) == ["claude", "codex"])
    let claude = u.tools[0]
    #expect(claude.source == "m4")
    #expect(!claude.stale)
    #expect(claude.fiveHour?.usedPercent == 14)
    #expect(claude.fiveHour?.resetsAt == iso("2026-09-26T12:30:00+02:00"))
    #expect(claude.fiveHour?.resetLabel == "12:30")
    #expect(claude.sevenDay?.resetsAt == nil)
    #expect(claude.sevenDay?.resetLabel == nil)
    #expect(claude.models["sonnet"]?.usedPercent == 20.5)
    #expect(claude.models.keys.sorted() == ["opus", "sonnet"])
    let codex = u.tools[1]
    #expect(codex.source == nil)
    #expect(codex.stale)
    #expect(codex.sevenDay == nil)
    #expect(codex.models.isEmpty)
}

@Test func activitySummaryDecodesGoldenAndSendsDays() async throws {
    let q = LockedBox()
    let svc = ActivityService(client: client(path: "/v1/activity/summary", body: try golden("activity_summary"), query: q))
    let a = try await svc.summary(days: 2)
    #expect(q.paths == ["days=2"])
    #expect(a.recording)
    #expect(a.days == 2)
    #expect(a.today.total.key == nil)
    #expect(a.today.total.activeSec == 840)
    #expect(a.today.total.attention == 1)
    #expect(a.today.byTool.map(\.id) == ["codex", "claude"])
    #expect(a.today.bySource.first?.sourceColor == "#FF8800")
    #expect(a.today.byTool.first?.sourceColor == nil)
    #expect(a.daily.map(\.id) == ["2026-09-25|claude", "2026-09-25|codex", "2026-09-26|claude", "2026-09-26|codex"])
    #expect(a.daily[1].activeSec == 0)
    #expect(a.daily.last?.date == iso("2026-09-26T00:00:00+02:00"))
    #expect(a.dailyBySource.map(\.id) == ["2026-09-25|m4", "2026-09-25|m5", "2026-09-26|m4", "2026-09-26|m5"])
    #expect(a.dailyBySource[2].sourceColor == "#00C8C8")
    #expect(a.dailyBySource[2].attention == 1)
}

@Test func weatherStateDecodesGolden() async throws {
    let svc = WeatherService(client: client(path: "/v1/weather/state", body: try golden("weather_state")))
    let w = try await svc.state()
    #expect(w.locationName == "Amsterdam")
    let cur = try #require(w.current)
    #expect(cur.condition == "rain")
    #expect(cur.conditionCode == "61")
    #expect(cur.tempC == 11.5)
    #expect(cur.hourly.count == 3)
    #expect(cur.hourly[0].time == iso("2026-09-26T10:00:00+02:00"))
    #expect(cur.hourly[1].time.timeIntervalSince(cur.hourly[0].time) == 3600)
    #expect(w.air?.europeanAqi == 42)
    #expect(w.air?.pm25Ugm3 == 8)
    #expect(w.sun?.sunrise == iso("2026-09-26T07:30:00+02:00"))
}

@Test func weatherStateDecodesEmptyGolden() async throws {
    let svc = WeatherService(client: client(path: "/v1/weather/state", body: try golden("weather_state_empty")))
    let w = try await svc.state()
    #expect(!w.enabled)
    #expect(w.locationName == nil)
    #expect(w.current == nil)
    #expect(w.air == nil)
    #expect(w.sun == nil)
}

@Test func clockHealthDecodesGolden() async throws {
    let svc = HealthService(client: client(path: "/v1/clock/health", body: try golden("clock_health")))
    let h = try await svc.clockHealth()
    #expect(h.publish.ok24h == 3)
    #expect(h.publish.fail24h == 1)
    #expect(h.publish.successRatio24h == 0.75)
    #expect(h.publish.okTotal == 3)
    #expect(h.publish.retriesTotal == 1)
    #expect(h.publish.lastAt == iso("2026-09-26T10:29:00+02:00"))
    #expect(h.latestFirmware == "1.1.2")
    #expect(h.updateAvailable == true)
    let dev = try #require(h.device)
    #expect(dev.reachable)
    #expect(dev.firmware == "1.1.1")
    #expect(dev.currentApp == "Time")
    #expect(dev.wifiRssiDbm == -71)
    #expect(dev.freeHeapBytes == 103032)
    #expect(dev.uptimeSec == 268719)
    #expect(dev.wifiConnects == 3)
    #expect(dev.matrixPower == true)
    #expect(dev.lowBattery == false)
    #expect(dev.temperatureC == 33.4)
}

@Test func clockHealthDecodesUnreachableGolden() async throws {
    let svc = HealthService(client: client(path: "/v1/clock/health", body: try golden("clock_health_unreachable")))
    let h = try await svc.clockHealth()
    #expect(h.publish.successRatio24h == nil)
    #expect(h.publish.lastAt == nil)
    #expect(h.latestFirmware == nil)
    #expect(h.updateAvailable == nil)
    let dev = try #require(h.device)
    #expect(!dev.reachable)
    #expect(dev.firmware == nil)
    #expect(dev.currentApp == nil)
    #expect(dev.wifiRssiDbm == nil)
}

private let workHoursJSON = #"""
{"days":[{"date":"2026-09-26","work_start":"2026-09-26T04:00:00+02:00","work_end":"2026-09-26T08:34:12+02:00","span_sec":16452,"active_sec":2100,"break_sec":14352,"sessions":2,"longest_sec":1500},{"date":"2026-09-25","work_start":null,"work_end":null,"span_sec":0,"active_sec":0,"break_sec":0,"sessions":0,"longest_sec":0}],"gap_min":15,"include_activity":true}
"""#

@Test func workHoursDecodesNullSpanAndSendsDays() async throws {
    let q = LockedBox()
    let svc = StatsService(client: client(path: "/v1/pomodoro/workhours", body: Data(workHoursJSON.utf8), query: q))
    let wh = try await svc.workHours(days: 2)
    #expect(q.paths == ["days=2"])
    #expect(wh.includeActivity)
    #expect(wh.days.first?.workStart == iso("2026-09-26T04:00:00+02:00"))
    let empty = try #require(wh.days.last)
    #expect(empty.workStart == nil)
    #expect(empty.workEnd == nil)
}

// Servers before 0.28 sent Go's zero time for an empty day.
@Test func workHoursMapsLegacyZeroTimeToNil() async throws {
    let legacy = workHoursJSON
        .replacingOccurrences(of: #""work_start":null"#, with: #""work_start":"0001-01-01T00:00:00Z""#)
        .replacingOccurrences(of: #""work_end":null"#, with: #""work_end":"0001-01-01T00:00:00Z""#)
    let wh = try await StatsService(client: client(path: "/v1/pomodoro/workhours", body: Data(legacy.utf8))).workHours()
    let empty = try #require(wh.days.last)
    #expect(empty.workStart == nil)
    #expect(empty.workEnd == nil)
    #expect(wh.days.first?.workStart != nil)
}
