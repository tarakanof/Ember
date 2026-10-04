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

private func iso(_ s: String) -> Date { try! Date(s, strategy: .iso8601) }

// MARK: Wire

@Test func knobStatsDecodesGolden() async throws {
    let body = try golden("knob_stats")
    let client = stubbedClient { req in (okResponse(req.url!), body) }
    let s: KnobStats = try await client.get("/")
    #expect(s.deviceID == "knob-61fc8c")
    #expect(s.diagnostics == .full)
    #expect(s.online)
    #expect(s.liveUntil == iso("2026-10-04T12:05:00Z"))
    #expect(s.resetReason == "poweron")
    #expect(s.points.count == 2)
    let p = s.points[0]
    #expect(p.cpuPercent == [12, 40])
    #expect(p.heapInternalMinBytes == 30000)
    #expect(p.psramFreeBytes == 7_000_000)
    #expect(p.requestsPerMin == 30)
    #expect(p.requestLatencyMaxMS == 120)
    #expect(p.frameAvgMS == 12.25)
    #expect(s.latest?.psramFreeBytes == nil)
    #expect(s.latest?.cpuAverage == 37.75)
    #expect(s.hasPSRAM)
    #expect(s.hasFullStats)
}

@Test func knobStatsDecodesEmptyGolden() async throws {
    let body = try golden("knob_stats_empty")
    let client = stubbedClient { req in (okResponse(req.url!), body) }
    let s: KnobStats = try await client.get("/")
    #expect(s.diagnostics == .off)
    #expect(s.lastSeen == nil)
    #expect(s.latest == nil)
    #expect(s.points.isEmpty)
    #expect(!s.hasFullStats)
}

@Test func knobSettingsWithoutDiagnosticsReadsOff() throws {
    let json = #"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true}],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}}"#
    let s = try JSONDecoder().decode(KnobSettings.self, from: Data(json.utf8))
    #expect(s.diagnostics == .off)
    var next = s
    next.diagnostics = .basic
    #expect(next.patch(from: s) == ["diagnostics": .string("basic")])
}

@Test func knobServiceStatsAndLiveRoutes() async throws {
    let seen = LockedBox()
    let client = stubbedClient { req in
        let body = req.httpBody ?? req.httpBodyStream.map { s in
            s.open(); defer { s.close() }
            var d = Data(); var buf = [UInt8](repeating: 0, count: 256)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: 256); if n <= 0 { break }; d.append(buf, count: n) }
            return d
        } ?? Data()
        seen.add("\(req.httpMethod!) \(req.url!.path)?\(req.url!.query ?? "") \(String(decoding: body, as: UTF8.self))")
        if req.url!.path.hasSuffix("/live") {
            return (okResponse(req.url!), Data(#"{"live_until":"2026-10-04T12:03:00Z"}"#.utf8))
        }
        return (okResponse(req.url!), try golden("knob_stats_empty"))
    }
    let svc = KnobService(client: client)
    _ = try await svc.stats(id: "knob-61fc8c", range: .day)
    let until = try await svc.setLive(id: "knob-61fc8c", seconds: 180)
    #expect(until == iso("2026-10-04T12:03:00Z"))
    #expect(seen.paths == [
        "GET /v1/devices/knob-61fc8c/stats?range=24h ",
        #"POST /v1/devices/knob-61fc8c/stats/live? {"seconds":180}"#,
    ])
}

// MARK: Model

private actor FakeStatsService: KnobStatsService {
    var diagnostics: KnobDiagnostics
    var fetches: [KnobStatsRange] = []
    var live: [Int] = []
    init(diagnostics: KnobDiagnostics) { self.diagnostics = diagnostics }

    func stats(id: String, range: KnobStatsRange) async throws -> KnobStats {
        fetches.append(range)
        return KnobStatsFake.make(range: range, diagnostics: diagnostics)
    }

    func setLive(id: String, seconds: Int) async throws -> Date? {
        live.append(seconds)
        return seconds > 0 ? Date() : nil
    }
}

private final class StepNow: @unchecked Sendable {
    private let lock = NSLock()
    private var value = Date(timeIntervalSinceReferenceDate: 812_000_000)
    func now() -> Date { lock.lock(); defer { lock.unlock() }; return value }
    func advance(_ s: TimeInterval) { lock.lock(); value += s; lock.unlock() }
}

/// A sleep that advances the clock by the requested time and stops the
/// loop after `polls` sleeps.
private func steppingSleep(_ clock: StepNow, polls: Int) -> @Sendable (Duration) async throws -> Void {
    let count = LockedBox()
    return { d in
        count.add("")
        clock.advance(Double(d.components.seconds))
        if count.paths.count >= polls { throw CancellationError() }
    }
}

private func waitFor(_ cond: @escaping @Sendable () async -> Bool) async {
    for _ in 0..<200 where !(await cond()) { try? await Task.sleep(for: .milliseconds(5)) }
}

@MainActor @Test func knobStatsModelPollsAndKeepsLiveAlive() async {
    let svc = FakeStatsService(diagnostics: .basic)
    let clock = StepNow()
    let model = KnobStatsModel(service: svc, sleep: steppingSleep(clock, polls: 30), now: clock.now)
    await model.run(deviceID: "knob-61fc8c")
    #expect(await svc.fetches.count == 30)
    #expect(await svc.fetches.allSatisfy { $0 == .fifteenMinutes })
    // 30 polls 5 s apart: live at 0 s, renewed at 60 s and 120 s, then stopped.
    await waitFor { await svc.live.last == 0 }
    #expect(await svc.live == [KnobStatsModel.liveSeconds, KnobStatsModel.liveSeconds, KnobStatsModel.liveSeconds, 0])
    #expect(model.stats.value?.diagnostics == .basic)
}

@MainActor @Test func knobStatsModelLeavesLiveAloneWithDiagnosticsOff() async {
    let svc = FakeStatsService(diagnostics: .off)
    let clock = StepNow()
    let model = KnobStatsModel(service: svc, sleep: steppingSleep(clock, polls: 3), now: clock.now)
    await model.run(deviceID: "knob-61fc8c")
    try? await Task.sleep(for: .milliseconds(20))
    #expect(await svc.live.isEmpty)
}

@MainActor @Test func knobStatsModelPollsAtTheRangeInterval() async {
    let svc = FakeStatsService(diagnostics: .full)
    let clock = StepNow()
    let model = KnobStatsModel(service: svc, sleep: steppingSleep(clock, polls: 4), now: clock.now)
    model.range = .day
    let start = clock.now()
    await model.run(deviceID: "knob-61fc8c")
    #expect(clock.now().timeIntervalSince(start) == 4 * 60)
    #expect(await svc.fetches == [.day, .day, .day, .day])
    #expect(model.stats.value?.range == "24h")
}

// MARK: Fake and series

@Test func knobStatsFakeCoversEachRange() {
    let now = Date(timeIntervalSinceReferenceDate: 812_000_000)
    for range in KnobStatsRange.allCases {
        let s = KnobStatsFake.make(range: range, now: now)
        #expect(!s.points.isEmpty)
        #expect(zip(s.points, s.points.dropFirst()).allSatisfy { $0.t < $1.t })
        #expect(s.points.first!.t >= now.addingTimeInterval(-range.duration))
        #expect(s.points.allSatisfy { ($0.cpuPercent ?? []).allSatisfy { (0...100).contains($0) } })
        #expect(s.hasFullStats)
    }
    let basic = KnobStatsFake.make(range: .hour, diagnostics: .basic, psram: false)
    #expect(!basic.hasFullStats)
    #expect(!basic.hasPSRAM)
    #expect(KnobStatsFake.make(range: .hour, diagnostics: .off).points.isEmpty)
}

@Test func knobSeriesBreaksAtReportingGaps() {
    let s = KnobStatsFake.make(range: .hour, live: false, gap: 1200...1800)
    let points = s.series([("Temperature", { $0.tempC })], range: .hour)
    #expect(Set(points.map(\.segment)) == [0, 1])
    let cpu = s.cpuSeries(range: .hour) { "Core \($0 + 1)" }
    #expect(Set(cpu.map(\.series)) == ["Core 1", "Core 2"])
}
