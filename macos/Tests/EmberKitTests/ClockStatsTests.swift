import Testing
import Foundation
@testable import EmberKit

private func golden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/dashboard/\(name).json")
    return try Data(contentsOf: url)
}

@Test func clockStatsDecodesGolden() async throws {
    let body = try golden("clock_stats")
    let client = stubbedClient { req in (okResponse(req.url!), body) }
    let s: ClockStats = try await client.get("/")
    #expect(s.range == "15m")
    #expect(s.configured)
    #expect(s.online)
    #expect(s.ipAddress == "192.0.2.66")
    #expect(s.sampleIntervalSec == 30)
    #expect(s.points.count == 2)
    let p = s.points[0]
    #expect(p.rssiDBm == -71)
    #expect(p.freeHeapBytes == 103_032)
    #expect(p.minFreeHeapBytes == 76_544)
    #expect(p.temperatureC == 33.4)
    #expect(p.humidityPercent == 21.4)
    #expect(p.lightLux == 42.5)
    #expect(p.batteryPercent == 97)
    #expect(p.publishOK == 1 && p.publishFail == 0)
    #expect(s.latest?.publishFail == 1)
}

@Test func clockStatsDecodesEmptyGolden() async throws {
    let body = try golden("clock_stats_empty")
    let client = stubbedClient { req in (okResponse(req.url!), body) }
    let s: ClockStats = try await client.get("/")
    #expect(s.reachable == nil)
    #expect(!s.online)
    #expect(s.latest == nil)
    #expect(s.points.isEmpty)
    #expect(!s.has(\.lightLux))
}

@Test func clockStatsClientAsksForTheRange() async throws {
    let seen = LockedBox()
    let client = stubbedClient { req in
        seen.add("\(req.httpMethod!) \(req.url!.path)?\(req.url!.query ?? "")")
        return (okResponse(req.url!), try golden("clock_stats_empty"))
    }
    _ = try await ClockStatsClient(client: client).clockStats(range: .day)
    #expect(seen.paths == ["GET /v1/clock/stats?range=24h"])
}

@Test func clockStatsFakeCoversEachRangeAndBreaksAtGaps() {
    let now = Date(timeIntervalSinceReferenceDate: 812_000_000)
    for range in HardwareRange.allCases {
        let s = ClockStatsFake.make(range: range, now: now)
        #expect(!s.points.isEmpty)
        #expect(zip(s.points, s.points.dropFirst()).allSatisfy { $0.t < $1.t })
        #expect(s.online)
        #expect(s.latest == s.points.last)
    }
    let gappy = ClockStatsFake.make(range: .hour, now: now, gap: 1200...1800)
    let rssi = gappy.series([("Signal", { $0.rssiDBm.map(Double.init) })], range: .hour)
    #expect(Set(rssi.map(\.segment)) == [0, 1])
    let offline = ClockStatsFake.make(range: .hour, now: now, online: false)
    #expect(!offline.online)
    #expect(offline.latest!.t < now.addingTimeInterval(-25 * 60))
}

private actor FakeClockStatsService: ClockStatsService {
    var fetches: [HardwareRange] = []
    func clockStats(range: HardwareRange) async throws -> ClockStats {
        fetches.append(range)
        return ClockStatsFake.make(range: range)
    }
}

@MainActor @Test func clockStatsModelPollsAtTheRangeInterval() async {
    let svc = FakeClockStatsService()
    let slept = LockedBox()
    let model = ClockStatsModel(service: svc, sleep: { d in
        slept.add("\(d.components.seconds)")
        if slept.paths.count >= 3 { throw CancellationError() }
    })
    model.range = .fifteenMinutes
    await model.run()
    #expect(await svc.fetches == [.fifteenMinutes, .fifteenMinutes, .fifteenMinutes])
    #expect(slept.paths == ["15", "15", "15"])
    #expect(model.stats.value?.range == "15m")
}

@Test func summedSeriesAddsCountsPerUnit() {
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC")!
    let t0 = Date(timeIntervalSinceReferenceDate: 812_000_040) // 2026-09-25 03:34:00 UTC
    let points = [
        HardwareSeriesPoint(t: t0, series: "OK", segment: 0, value: 2),
        HardwareSeriesPoint(t: t0.addingTimeInterval(30), series: "OK", segment: 0, value: 3),
        HardwareSeriesPoint(t: t0.addingTimeInterval(30), series: "Failed", segment: 0, value: 1),
        HardwareSeriesPoint(t: t0.addingTimeInterval(60), series: "OK", segment: 1, value: 4),
    ]
    let minute = HardwareSeries.summed(points, per: .minute, calendar: cal)
    #expect(minute.map(\.value) == [5, 1, 4])
    #expect(minute.map(\.series) == ["OK", "Failed", "OK"])
    #expect(minute[0].t == cal.dateInterval(of: .minute, for: t0)!.start)
    #expect(HardwareSeries.summed(points, per: .hour, calendar: cal).map(\.value) == [9, 1])
}

@Test func deliveredNeverRoundsFailuresUpTo100() {
    let en = Locale(identifier: "en_US")
    #expect(ClockReadout.delivered(ok: 0, fail: 0, locale: en) == nil)
    #expect(ClockReadout.delivered(ok: 50, fail: 0, locale: en) == "100%")
    #expect(ClockReadout.delivered(ok: 2871, fail: 12, locale: en) == "99.5%")
    #expect(ClockReadout.delivered(ok: 9999, fail: 1, locale: en) == "99.9%")
    #expect(ClockReadout.delivered(ok: 96, fail: 4, locale: en) == "96%")
}
