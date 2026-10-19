import Foundation
import SwiftUI
import Testing
@testable import EmberKit

private let w = KnobTheme.standard.weather
private let observed: Int64 = 1_782_043_200
private let now = observed + 300
private func at(_ t: Int64) -> Date { Date(timeIntervalSince1970: TimeInterval(t)) }

private func faces(wind: Int? = nil, aqi: Int? = nil, nowRain: Int? = nil,
                   spells: [(Int64, Int64)]? = nil) -> WeatherState.Faces {
    .init(utcOffsetS: 3600, night: false, windKmh: wind, windDirDeg: nil, rainIntensity: nowRain, todayPopPct: nil,
          rain: spells.map { .init(until: nil, spells: $0.map { .init(start: at($0.0), end: at($0.1), intensity: 2, snow: false) }) },
          days: [], twilight: nil, moon: nil, aqi: aqi)
}

private func state(_ cond: String = "clouds", _ code: String = "3", fetched: Int64 = observed, generated: Int64 = observed,
                   cloud: Double? = nil, faces f: WeatherState.Faces? = faces(), stale: Bool = false,
                   enabled: Bool = true) -> WeatherState {
    WeatherState(generatedAt: at(generated), enabled: enabled, provider: "open-meteo", units: "metric", locationName: nil,
                 current: .init(fetchedAt: at(fetched), stale: stale, condition: cond, conditionCode: code, severe: false, tempC: 12,
                                todayMinC: nil, todayMaxC: nil, cloudPct: cloud, uv: nil, hourly: []),
                 air: nil, sun: nil, faces: f)
}

private func auto(_ s: WeatherState?, shown: KnobWeatherFaceID? = nil) -> KnobWeatherFaceID {
    KnobWeatherAuto.face(state: s, now: at(now), shown: shown)
}

@Test func autoPicksRainWhileWetOrStormy() {
    #expect(auto(state("rain", "61", faces: faces(nowRain: 2, spells: [(observed - 900, observed + 7200)]))) == .rain)
    #expect(auto(state("storm", "95", faces: faces())) == .rain, "a storm is the Rain now face even when dry")
    #expect(auto(state(faces: faces(nowRain: 0, spells: [(observed - 900, now)]))) == .temp, "the spell ended at now")
}

@Test func autoPicksCastWhenRainIsDueWithinTheHour() {
    let soon = Int64(w.cast.soonS), keep = Int64(w.cast.keepS)
    #expect(auto(state(faces: faces(spells: [(now + soon, now + soon + 1800)]))) == .cast)
    #expect(auto(state(faces: faces(spells: [(now + soon + 1, now + soon + 1800)]))) == .temp)
    #expect(auto(state(faces: faces(spells: [(now + keep, now + keep + 1800)])), shown: .cast) == .cast, "kept while on screen")
    #expect(auto(state(faces: faces(spells: [(now + keep + 1, now + keep + 1800)])), shown: .cast) == .temp)
    #expect(auto(state(faces: faces())) == .temp, "no rain block, no forecast")
}

@Test func autoPicksWindFrom39KmhAndKeepsItDownTo34() {
    #expect(w.wind.autoKmh == 39 && w.wind.keepKmh == 34)
    #expect(auto(state(faces: faces(wind: 39))) == .wind)
    #expect(auto(state(faces: faces(wind: 38))) == .temp)
    #expect(auto(state(faces: faces(wind: 34)), shown: .wind) == .wind)
    #expect(auto(state(faces: faces(wind: 33)), shown: .wind) == .temp)
    #expect(auto(state(faces: faces(wind: 38)), shown: .air) == .temp, "only its own face keeps it")
    #expect(auto(state(faces: faces(wind: 1000))) == .temp, "out of range is no reading")
}

@Test func autoPicksAirAbovePoorAndKeepsItWithinTheHysteresis() {
    #expect(auto(state(faces: faces(aqi: w.air.poor + 1))) == .air)
    #expect(auto(state(faces: faces(aqi: w.air.poor))) == .temp)
    #expect(auto(state(faces: faces(aqi: w.air.poor - w.air.hyst + 1)), shown: .air) == .air)
    #expect(auto(state(faces: faces(aqi: w.air.poor - w.air.hyst)), shown: .air) == .temp)
}

@Test func autoKeepsTheKnobsOrder() {
    let all = faces(wind: 60, aqi: 90, nowRain: 2, spells: [(observed - 900, observed + 7200), (now + 600, now + 1200)])
    #expect(auto(state("rain", "61", faces: all)) == .rain)
    #expect(auto(state(faces: faces(wind: 60, aqi: 90, spells: [(now + 600, now + 1200)]))) == .cast)
    #expect(auto(state(faces: faces(wind: 60, aqi: 90))) == .wind)
    #expect(auto(state(faces: faces(wind: 10, aqi: 90))) == .air)
}

@Test func autoFallsBackToTempWithoutFreshData() {
    let windy = faces(wind: 60, aqi: 90)
    #expect(auto(nil) == .temp)
    #expect(auto(state(generated: now - 1801, faces: windy)) == .temp)
    #expect(auto(state(generated: now - 1800, faces: windy)) == .wind)
    #expect(auto(state(faces: windy, stale: true)) == .temp)
    #expect(auto(state(faces: windy, enabled: false)) == .temp)
    #expect(auto(state(faces: nil)) == .temp, "a server without faces")
}

private func knob(_ id: String, kind: String = KnobDevice.knobKind, _ shown: KnobShown?) -> KnobDevice {
    KnobDevice(id: id, kind: kind, hwID: id, name: id, createdAt: at(0), shown: shown)
}

private func seen(_ page: String, _ face: String? = nil, _ t: Int64 = now) -> KnobShown { .init(page: page, face: face, at: at(t)) }

@Test func followPicksTheKnobThatChangedLast() {
    var f = KnobShownFollow()
    f.follow([knob("a", seen("weather", "wind", now - 10)), knob("b", seen("weather", "air", now - 2)),
              knob("c", kind: KnobDevice.clockKind, seen("weather", "rain", now)), knob("d", nil)])
    #expect(f.face == .air && f.current == "b", "the clock is no knob")
    f.follow([knob("a", seen("weather", "wind", now + 20)), knob("b", seen("weather", "air", now + 3))])
    #expect(f.face == .air, "a knob re-polling the same face is no change")
    f.follow([knob("a", seen("weather", "sun", now + 22)), knob("b", seen("weather", "air", now + 25))])
    #expect(f.face == .sun && f.current == "a", "a turn on the other knob takes over")
    f.follow([knob("a", seen("weather", "sun", now + 40)), knob("b", seen("weather", "air", now + 45))])
    #expect(f.face == .sun)
    f.follow([knob("b", seen("weather", "air", now + 47))])
    #expect(f.face == .air && f.current == "b", "the followed knob's report went stale")
}

@Test func followKeepsTheCurrentKnobOnATie() {
    var f = KnobShownFollow()
    f.follow([knob("b", seen("weather", "air", now))])
    f.follow([knob("a", seen("weather", "wind", now)), knob("b", seen("weather", "air", now))])
    #expect(f.face == .air && f.current == "b")
    var g = KnobShownFollow()
    g.follow([knob("a", seen("weather", "wind", now))])
    g.follow([knob("a", seen("weather", "wind", now)), knob("b", seen("weather", "air", now))])
    #expect(g.face == .wind && g.current == "a")
}

@Test func followKeepsTheLastWeatherFaceOffThePage() {
    var f = KnobShownFollow()
    f.follow([knob("a", seen("pomodoro"))])
    #expect(f.face == nil, "no weather report yet: AUTO")
    for (i, id) in KnobWeatherFaceID.allCases.enumerated() {
        f.follow([knob("a", seen("weather", id.rawValue, now + Int64(i)))])
        #expect(f.face == id)
    }
    f.follow([knob("a", seen("weather", "wind", now + 10))])
    f.follow([knob("a", seen("nowplaying", nil, now + 11))])
    #expect(f.face == .wind, "a non-weather page keeps the last weather face")
    f.follow([knob("a", seen("weather", "fog", now + 12))])
    #expect(f.face == .wind, "an unknown face keeps it too")
    f.follow([knob("a", seen("weather", nil, now + 13))])
    #expect(f.face == .wind)
    f.follow([knob("a", nil)])
    #expect(f.face == nil, "nothing fresh reported: AUTO")
}

@Test func deviceListDecodesShownAndToleratesOldOrBadEntries() throws {
    let json = #"""
    {"devices":[{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"a0b1c261fc8c","name":"Knob",
    "created_at":"2026-10-01T00:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,"last_checkin":null,
    "shown":{"page":"weather","face":"wind","at":"2026-06-21T12:00:00Z"}},
    {"id":"knob-000002","kind":"cinder-knob","hw_id":"000000000002","name":"Pomo","created_at":"2026-10-01T00:00:00Z",
    "config_version":1,"rotation_pending":false,"rotated_at":null,"last_checkin":null,
    "shown":{"page":"pomodoro","face":null,"at":"2026-06-21T11:59:00Z"}},
    {"id":"knob-000001","kind":"cinder-knob","hw_id":"000000000001","name":"Old","created_at":"2026-10-01T00:00:00Z",
    "config_version":1,"rotation_pending":false,"rotated_at":null,"last_checkin":null},
    {"id":"knob-000003","kind":"cinder-knob","hw_id":"000000000003","name":"Bad","created_at":"2026-10-01T00:00:00Z",
    "config_version":1,"rotation_pending":false,"rotated_at":null,"last_checkin":null,"shown":"weather:wind"}]}
    """#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let list = try d.decode(KnobDeviceList.self, from: Data(json.utf8)).devices
    #expect(list[0].shown == KnobShown(page: "weather", face: "wind", at: Date(timeIntervalSince1970: 1_782_043_200)))
    #expect(list[1].shown == KnobShown(page: "pomodoro", face: nil, at: Date(timeIntervalSince1970: 1_782_043_140)))
    #expect(list[2].shown == nil && list[3].shown == nil)
    #expect(KnobShownFollow.seeded(list) == .wind)
    #expect(KnobShownFollow.seeded([list[2], list[3]]) == nil, "old firmware: AUTO")
}

@Test func memoDrawsTheReportedFaceOrTheAutoPick() {
    let windy = state(faces: faces(wind: 45))
    for id in KnobWeatherFaceID.allCases {
        var m = KnobWeatherMemo()
        #expect(m.face(id, state: windy, now: at(now)).weatherFace == id)
    }
    var memo = KnobWeatherMemo()
    #expect(memo.face(nil, state: windy, now: at(now)).weatherFace == .wind)
    #expect(memo.face(nil, state: state(faces: faces(wind: 36)), now: at(now)).weatherFace == .wind, "AUTO keeps what it drew")
    #expect(memo.face(.temp, state: windy, now: at(now)) == .weather(KnobTempFace(state: windy, now: at(now))))
    #expect(memo.face(nil, state: state(faces: faces(wind: 36)), now: at(now)).weatherFace == .temp,
            "a reported face resets AUTO's keep")
}

@Test func memoKeepsTheShownFacesOwnHysteresis() {
    var memo = KnobWeatherMemo()
    guard case .air(let a) = memo.face(.air, state: state(faces: faces(aqi: 61)), now: at(now)) else { Issue.record("not air"); return }
    #expect(a.wilted)
    guard case .air(let b) = memo.face(.air, state: state(faces: faces(aqi: 58)), now: at(now)) else { return }
    #expect(b.wilted, "kept within the hysteresis while on screen")
    _ = memo.face(.temp, state: state(faces: faces(aqi: 58)), now: at(now))
    guard case .air(let c) = memo.face(.air, state: state(faces: faces(aqi: 58)), now: at(now)) else { return }
    #expect(!c.wilted, "a face off screen keeps nothing")
}

@Test func temperatureCloudBandFollowsEveryObservationWhateverIsDrawn() {
    var memo = KnobWeatherMemo()
    _ = memo.face(.temp, state: state(cloud: 39), now: at(now))
    #expect(memo.band == 1)
    _ = memo.face(.wind, state: state(cloud: 80, faces: faces(wind: 45)), now: at(now))
    #expect(memo.band == 3, "the band moves while Wind is drawn, as wx_temp_from_shown does")
    _ = memo.face(.wind, state: state(cloud: 42, faces: faces(wind: 45)), now: at(now))
    guard case .weather(let t) = memo.face(.temp, state: state(cloud: 42), now: at(now)) else { Issue.record("not temp"); return }
    #expect(t.cloudBand == 2 && t.sub == "Partly cloudy", "back on Temperature: band 2 like the knob, not the stale band 1")
}

@Test func peekDrawsWithoutTouchingTheMemoAndSettles() {
    let s = state(cloud: 42, faces: faces(wind: 45, aqi: 70, nowRain: 2, spells: [(observed - 900, observed + 7200), (now + 600, now + 1200)]))
    for pick in [nil] + KnobWeatherFaceID.allCases.map(Optional.some) {
        let memo = KnobWeatherMemo()
        let first = memo.peek(pick, state: s, now: at(now))
        #expect(memo == KnobWeatherMemo(), "peek leaves the memo alone")
        #expect(first.memo.drawn == first.face.weatherFace && first.memo.band != nil, "the peek's memo is what the commit stores")
        let again = first.memo.peek(pick, state: s, now: at(now))
        #expect(again.memo == first.memo && again.face == first.face, "committing a peek is a fixed point (\(String(describing: pick)))")
    }
}

@Test func aFreshMemoForgetsTheKeepLikeTheKnobOnPageOpen() {
    var memo = KnobWeatherMemo()
    #expect(memo.face(nil, state: state(faces: faces(wind: 40)), now: at(now)).weatherFace == .wind)
    #expect(memo.peek(nil, state: state(faces: faces(wind: 36)), now: at(now + 600)).face.weatherFace == .wind,
            "while the page stays open, Wind is kept down to 34")
    memo = KnobWeatherMemo()
    #expect(memo.peek(nil, state: state(faces: faces(wind: 36)), now: at(now + 600)).face.weatherFace == .temp,
            "reopened, the knob starts without a shown face")
}

@MainActor
private func litDot(_ f: KnobFace) throws -> Int? {
    let r = ImageRenderer(content: KnobFaceView(f).frame(width: 466, height: 466).environment(\.colorScheme, .dark))
    r.scale = 1
    let img = try #require(r.cgImage)
    var px = [UInt8](repeating: 0, count: 4)
    func rgb(_ p: CGPoint) -> RGB {
        let ctx = CGContext(data: &px, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                            space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(img, in: CGRect(x: -p.x.rounded(), y: -(466 - 1 - p.y.rounded()), width: 466, height: 466))
        return RGB(r: px[0], g: px[1], b: px[2])
    }
    func near(_ a: RGB, _ b: RGB) -> Bool { abs(Int(a.r) - Int(b.r)) + abs(Int(a.g) - Int(b.g)) + abs(Int(a.b) - Int(b.b)) <= 40 }
    return (0..<w.dots.faces).first { near(rgb(KnobTempFace.dotPoint($0, w.dots, center: 233)), w.dots.onColor) }
}

@MainActor @Test func theLitDotIsTheDrawnFace() throws {
    #expect(KnobWeatherFaceID.allCases.map(\.dot) == Array(0..<w.dots.faces))
    let s = state(faces: faces(wind: 45, aqi: 70, spells: [(now + 600, now + 1200)]))
    for id in KnobWeatherFaceID.allCases {
        #expect(try litDot(KnobWeatherMemo().peek(id, state: s, now: at(now)).face) == id.dot, "\(id)")
    }
    #expect(try litDot(KnobWeatherMemo().peek(nil, state: state(faces: faces(wind: 45)), now: at(now)).face) == KnobWeatherFaceID.wind.dot)
}

private final class Devices: @unchecked Sendable {
    private let lock = NSLock()
    private var _list: [KnobDevice] = []
    private var _fail = false
    private var _calls = 0
    private var _now = at(now)
    private var _sleeps: [Duration] = []
    var list: [KnobDevice] { get { lock.withLock { _list } } set { lock.withLock { _list = newValue } } }
    var fail: Bool { get { lock.withLock { _fail } } set { lock.withLock { _fail = newValue } } }
    var calls: Int { lock.withLock { _calls } }
    var sleeps: [Duration] { lock.withLock { _sleeps } }
    var clock: Date { get { lock.withLock { _now } } set { lock.withLock { _now = newValue } } }
    func read() throws -> [KnobDevice] {
        try lock.withLock {
            _calls += 1
            if _fail { throw URLError(.cannotConnectToHost) }
            return _list
        }
    }
    func slept(_ d: Duration) { lock.withLock { _sleeps.append(d) } }
}

@MainActor @Test func shownFeedStartsFromTheSeedList() {
    let feed = KnobShownFeed(seed: [knob("a", seen("weather", "sun"))], fetch: { [] })
    #expect(feed.face == .sun, "no AUTO flash before the first poll")
    #expect(KnobShownFeed(fetch: { [] }).face == nil)
}

@MainActor @Test func shownFeedKeepsTheAnswerOnlyAcrossFailures() async {
    let d = Devices()
    var handed: [[KnobDevice]] = []
    let feed = KnobShownFeed(fetch: { try d.read() }, onDevices: { handed.append($0) }, now: { d.clock })
    await feed.refresh()
    #expect(feed.face == nil)
    d.list = [knob("a", seen("weather", "rain"))]
    await feed.refresh()
    #expect(feed.face == .rain && handed.last == d.list, "the list is handed on to the knob model")
    d.fail = true
    d.clock = at(now + 50)
    await feed.refresh()
    #expect(feed.face == .rain, "a failed poll within 50 s keeps the last answer")
    d.clock = at(now + 51)
    await feed.refresh()
    #expect(feed.face == nil, "AUTO once the last good list is over 50 s old")
    d.fail = false
    d.list = [knob("a", seen("weather", "rain", now + 52))]
    await feed.refresh()
    #expect(feed.face == .rain)
    d.list = [knob("a", nil)]
    await feed.refresh()
    #expect(feed.face == nil, "a good list without shown is AUTO at once")
    #expect(handed.count == 4)
}

@MainActor @Test func shownFeedPublishesOnlyAChange() async {
    let d = Devices()
    d.list = [knob("a", seen("weather", "wind"))]
    let feed = KnobShownFeed(fetch: { try d.read() })
    await feed.refresh()
    final class Flag: @unchecked Sendable { var fired = false }
    let flag = Flag()
    withObservationTracking { _ = feed.face } onChange: { flag.fired = true }
    d.list = [knob("a", seen("weather", "wind", now + 2))]
    await feed.refresh()
    #expect(!flag.fired, "an unchanged poll doesn't redraw the preview")
    d.list = [knob("a", seen("weather", "sun", now + 4))]
    await feed.refresh()
    #expect(flag.fired && feed.face == .sun)
}

@MainActor private func until(_ cond: @autoclosure () -> Bool) async throws {
    for _ in 0..<400 where !cond() { try await Task.sleep(for: .milliseconds(5)) }
}

@MainActor @Test func shownFeedPollsOnlyWhileVisibleAndAtOnceWhenShownAgain() async throws {
    #expect(KnobShownFeed.interval == .seconds(2))
    let d = Devices()
    let feed = KnobShownFeed(fetch: { try d.read() }, sleep: { s in d.slept(s); try await Task.sleep(for: .seconds(3600)) })
    #expect(!feed.isPolling && d.calls == 0, "nothing polls before the preview is visible")
    feed.setVisible(true)
    try await until(d.calls == 1)
    feed.setVisible(true)
    try await Task.sleep(for: .milliseconds(30))
    #expect(d.calls == 1 && feed.isPolling, "visible twice is one poller")
    feed.setVisible(false)
    #expect(!feed.isPolling)
    try await Task.sleep(for: .milliseconds(30))
    #expect(d.calls == 1, "hidden: no poll")
    feed.setVisible(true)
    try await until(d.calls == 2)
    #expect(d.calls == 2, "shown again: it polls at once, not after the wait")
    #expect(d.sleeps.allSatisfy { $0 == .seconds(2) })
    feed.setVisible(false)
}

@MainActor @Test func shownFeedStopsPollingWhileHidden() async throws {
    let d = Devices()
    let feed = KnobShownFeed(fetch: { try d.read() }, sleep: { _ in try await Task.sleep(for: .milliseconds(2)) })
    feed.setVisible(true)
    try await until(d.calls >= 3)
    feed.setVisible(false)
    try await Task.sleep(for: .milliseconds(20))
    let stopped = d.calls
    try await Task.sleep(for: .milliseconds(60))
    #expect(d.calls == stopped, "minimised, covered or inactive: no poll")
    feed.setVisible(true)
    try await until(d.calls >= stopped + 3)
    #expect(d.calls >= stopped + 3, "visible again: polling resumes")
    feed.setVisible(false)
}

@MainActor @Test func shownFeedBacksOffOnFailuresAndResetsOnSuccess() async throws {
    let d = Devices()
    d.fail = true
    let feed = KnobShownFeed(fetch: { try d.read() }, sleep: { s in d.slept(s); try await Task.sleep(for: .milliseconds(1)) })
    feed.setVisible(true)
    try await until(d.sleeps.count >= 6)
    d.fail = false
    let failed = d.sleeps.count
    try await until(d.sleeps.count >= failed + 2)
    feed.setVisible(false)
    let s = d.sleeps
    #expect(Array(s.prefix(6)) == [.seconds(4), .seconds(8), .seconds(16), .seconds(30), .seconds(30), .seconds(30)])
    #expect(s.last == .seconds(2), "a good poll goes back to 2 s")
}

@MainActor @Test func shownFeedDropsAnAnswerThatLandsAfterItWasHidden() async throws {
    let d = Devices()
    d.list = [knob("a", seen("weather", "sun"))]
    final class Box: @unchecked Sendable { var feed: KnobShownFeed? }
    let box = Box()
    var handed = 0
    let feed = KnobShownFeed(fetch: {
        let list = try d.read()
        await MainActor.run { box.feed?.setVisible(false) }
        return list
    }, onDevices: { _ in handed += 1 }, sleep: { _ in try await Task.sleep(for: .seconds(3600)) })
    box.feed = feed
    feed.setVisible(true)
    try await until(d.calls == 1)
    try await Task.sleep(for: .milliseconds(30))
    #expect(feed.face == nil && handed == 0, "a poll cancelled in flight changes nothing")
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func autoRuleMatchesCinderSource() throws {
    let chain = try cinderMatch("components/weather/wx_temp.c", #"t->face = (t->rain\.on.*?);"#)
    let rx = try NSRegularExpression(pattern: #"\?\s*WXF_([A-Z]+)\s*:"#)
    let picked = rx.matches(in: chain, range: NSRange(chain.startIndex..., in: chain)).map {
        String(chain[Range($0.range(at: 1), in: chain)!]).lowercased()
    }
    #expect(picked == KnobWeatherAuto.order.map(\.rawValue), "AUTO order")
    #expect(chain.hasSuffix(": WXF_TEMP"), "AUTO falls back to Temperature")
    for (fn, face) in [("wx_cast_soon", "CAST"), ("wx_wind_strong", "WIND"), ("wx_air_poor", "AIR")] {
        #expect(chain.contains("\(fn)(o, shown && shown->face == WXF_\(face))"), "\(fn) keeps only its own face")
    }
    #expect(chain.hasPrefix("t->rain.on "))
    _ = try cinderMatch("components/weather/wx_temp.c", #"(wx_rain_from\(o, &t->rain\);\s*wx_rain_keep\(&t->rain, shown \? &shown->rain : NULL\);)"#)
    _ = try cinderMatch("components/weather/wx_cast.c",
                        #"(bool wx_cast_soon\(const wx_obs_t \*o, bool shown\)\s*\{\s*scan_t s;\s*"#
                            + #"if \(!scan\(o, &s\) \|\| !o->has_rain \|\| !o->has_utc_offset \|\| s\.wet \|\| !s\.next\) return false;\s*"#
                            + #"return s\.next->start - s\.now <= \(shown \? WX_CAST_KEEP_S : WX_CAST_SOON_S\);)"#)
    _ = try cinderMatch("components/weather/wx_wind.c",
                        #"(static bool fresh\(const wx_obs_t \*o\) \{ return o && o->valid && o->has_wind && !wx_look_from_obs\(o\)\.still; \})"#)
    _ = try cinderMatch("components/weather/wx_wind.c",
                        #"(return fresh\(o\) && clamp_kmh\(o->wind_kmh\) >= \(shown \? WX_WIND_KEEP_KMH : WX_WIND_AUTO_KMH\);)"#)
    _ = try cinderMatch("components/weather/wx_air.c",
                        #"(static bool fresh\(const wx_obs_t \*o\) \{ return o && o->has_aqi && !wx_look_from_obs\(o\)\.still; \})"#)
    _ = try cinderMatch("components/weather/wx_air.c",
                        #"(return fresh\(o\) && o->aqi > \(shown \? WX_AIR_POOR - WX_AIR_HYST : WX_AIR_POOR\);)"#)
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func faceIdsAndFreshnessMatchCinderSource() throws {
    let ids = try cinderMatch("components/weather/include/wx_faces.h", #"typedef enum \{ ([A-Z_, ]+), WXF_N \} wx_face_id_t;"#)
    #expect(ids.components(separatedBy: ", ") == KnobWeatherFaceID.allCases.map { "WXF_" + $0.rawValue.uppercased() })
    let names = try cinderMatch("components/demo/demo.c", #"static const char \*const FACES\[\] = \{([^}]*)\};"#)
    #expect(names == KnobWeatherFaceID.allCases.map { "\"\($0.rawValue)\"" }.joined(separator: ", "), "the shown/demo face words")
    _ = try cinderMatch("components/weather/weather_face.c",
                        #"(l\.still = !o->enabled \|\| o->stale \|\| o->age_s > WX_MAX_AGE_S;)"#)
}
