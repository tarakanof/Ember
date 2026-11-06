import Testing
import Foundation
@testable import EmberKit

private func golden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/dashboard/\(name).json")
    return try Data(contentsOf: url)
}

private func decode<T: Decodable>(_ body: Data) async throws -> T {
    let client = stubbedClient { req in (okResponse(req.url!), body) }
    return try await client.get("/")
}

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

@Test func usageSnapshotDecodesGolden() async throws {
    let u: UsageSnapshot = try await decode(try golden("usage"))
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

@Test func activitySummaryDecodesGolden() async throws {
    let a: ActivitySummary = try await decode(try golden("activity_summary"))
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
    let w: WeatherState = try await decode(try golden("weather_state"))
    #expect(w.locationName == "Amsterdam")
    let cur = try #require(w.current)
    #expect(cur.condition == "rain")
    #expect(cur.conditionCode == "61")
    #expect(cur.tempC == 11.5)
    #expect(cur.todayMinC == 8.4 && cur.todayMaxC == 16.6)
    #expect(cur.cloudPct == 62 && cur.uv == 3)
    #expect(cur.hourly.count == 3)
    #expect(cur.hourly[0].time == iso("2026-09-26T10:00:00+02:00"))
    #expect(cur.hourly[1].time.timeIntervalSince(cur.hourly[0].time) == 3600)
    #expect(w.air?.europeanAqi == 42)
    #expect(w.air?.pm25Ugm3 == 8)
    #expect(w.sun?.sunrise == iso("2026-09-26T07:35:00+02:00"))
    let f = try #require(w.faces)
    #expect(f.utcOffsetS == 7200 && !f.night && f.windKmh == 18 && f.windDirDeg == 243)
    #expect(f.rainIntensity == 1 && f.todayPopPct == 80 && f.aqi == 42)
    #expect(f.rain?.until == iso("2026-09-26T10:45:00+02:00") && f.rain?.spells.count == 2)
    #expect(f.rain?.spells.last == .init(start: iso("2026-09-26T13:00:00+02:00"), end: iso("2026-09-26T13:15:00+02:00"),
                                         intensity: 3, snow: false))
    #expect(f.days.count == 2 && f.days[0].conditionCode == "63" && f.days[0].minC == 10 && f.days[1].popPct == 5)
    #expect(f.twilight?.goldenStart == iso("2026-09-26T18:45:00+02:00") && f.moon?.up == false)
}

@Test func weatherStateDecodesEmptyGolden() async throws {
    let w: WeatherState = try await decode(try golden("weather_state_empty"))
    #expect(!w.enabled)
    #expect(w.locationName == nil)
    #expect(w.current == nil)
    #expect(w.air == nil)
    #expect(w.sun == nil)
    #expect(w.faces == nil)
}

@Test func clockHealthDecodesGolden() async throws {
    let h: ClockHealth = try await decode(try golden("clock_health"))
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
    let h: ClockHealth = try await decode(try golden("clock_health_unreachable"))
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

@Test func clockHealthDecodesDisabledGolden() async throws {
    let h: ClockHealth = try await decode(try golden("clock_health_disabled"))
    #expect(h.isDisabled)
    #expect(h.device == nil)
}

@Test func weatherStateGoldenFeedsTheKnobFaces() async throws {
    let s: WeatherState = try await decode(try golden("weather_state"))
    let now = iso("2026-09-26T10:30:00+02:00")
    let rain = KnobRainFace(state: s, now: now)
    #expect(!rain.dry && rain.kind == .rain && rain.intensity == 2 && rain.pop == 80 && rain.sub == "until 10:45")
    let cast = KnobCastFace(state: s, now: now)
    #expect(cast.wet && cast.hasCount && !cast.hours && cast.valueText == "15" && cast.sub == "#5C9CE0 rain# until 10:45")
    #expect(cast.arcs.map(\.intensity) == [2, 3] && cast.label == "10:45")
    let wind = KnobWindFace(state: s, now: now)
    #expect(wind.have && wind.kmh == 18 && wind.sub == "#8ACF7A Gentle breeze# \u{2022} SW")
    let days = KnobDaysFace(state: s, now: now)
    #expect(days.rows.map(\.icon) == [.rain, .sun] && days.rows.map(\.pop) == [70, nil] && days.rows.map(\.wday) == [0, 1])
    #expect(days.rows.map(\.lo) == [10, 7] && days.rows.map(\.hi) == [14, 19])
    let sun = KnobSunFace(state: s, now: now)
    #expect(sun.have && sun.phase == .day && sun.rise == "07:35" && sun.set == "19:30" && sun.sub == "golden hour 18:45")
    let air = KnobAirFace(state: s, now: now)
    #expect(air.have && air.aqi == 42 && air.sub == "Moderate")
}
