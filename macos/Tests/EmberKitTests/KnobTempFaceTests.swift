import Foundation
import Testing
@testable import EmberKit

private let w = KnobTheme.standard.weather

@Test func tempColourFollowsTheFirmwareScale() {
    #expect(KnobTempFace.color(-20, w.scale) == RGB(hex: "#B070E0"))
    #expect(KnobTempFace.color(-35, w.scale) == RGB(hex: "#B070E0"))
    #expect(KnobTempFace.color(.nan, w.scale) == RGB(hex: "#B070E0"))
    #expect(KnobTempFace.color(0, w.scale) == RGB(hex: "#66C2EC"))
    #expect(KnobTempFace.color(15, w.scale) == RGB(hex: "#E8D44A"))
    #expect(KnobTempFace.color(20, w.scale) == RGB(hex: "#ECAF42"))
    #expect(KnobTempFace.color(40, w.scale) == RGB(hex: "#E0306A"))
    #expect(KnobTempFace.color(55, w.scale) == RGB(hex: "#E0306A"))
}

@Test func ringFractionUsesTodaysRangeWidenedToTheValue() {
    let day = KnobTempFace(value: 12.4, todayMinC: 8.4, todayMaxC: 16.6)
    #expect(day.value == 12 && day.hasRange && day.lo == 8 && day.hi == 17)
    #expect(abs(day.fraction - 4.0 / 9) < 1e-12)

    let wide = KnobTempFace(value: 21, todayMinC: 9, todayMaxC: 18)
    #expect(wide.lo == 9 && wide.hi == 21 && wide.fraction == 1)

    let flat = KnobTempFace(value: 10, todayMinC: 10, todayMaxC: 10)
    #expect(flat.lo == 9 && flat.hi == 11 && flat.fraction == 0.5)

    let open = KnobTempFace(value: 12, todayMinC: 8)
    #expect(!open.hasRange && open.lo == -20 && open.hi == 40)
    #expect(abs(open.fraction - 32.0 / 60) < 1e-12)

    let none = KnobTempFace(value: nil, todayMinC: 1, todayMaxC: 5)
    #expect(none.value == nil && !none.hasRange && none.fraction == 0 && none.valueText() == "--°")
    #expect(KnobTempFace(value: 140).value == 99 && KnobTempFace(value: -2.5).value == -3)
}

@Test func tempFaceSurvivesNonFiniteAndHugeTemperatures() {
    for bad in [Double.nan, .infinity, -.infinity, 1e300, -1e300] {
        let f = KnobTempFace(value: bad, todayMinC: bad, todayMaxC: bad)
        #expect(f.fraction.isFinite)
        _ = KnobTempFace.color(bad, w.scale)
        _ = KnobTempFace.mercuryTop(bad, w.thermo)
        let g = KnobTempFace(value: 12, todayMinC: bad, todayMaxC: bad)
        #expect(g.value == 12 && g.lo <= 12 && g.hi >= 12 && g.lo >= -99 && g.hi <= 99)
    }
    let huge = KnobTempFace(value: 1e300, todayMinC: -1e300, todayMaxC: 1e300)
    #expect(huge.value == 99 && huge.lo == -99 && huge.hi == 99)
}

@Test func gaugeSweepsFromSevenThirtyClockwiseToFourThirty() {
    let start = KnobTempFace.gaugePoint(0, w, center: 233), top = KnobTempFace.gaugePoint(0.5, w, center: 233)
    let end = KnobTempFace.gaugePoint(1, w, center: 233), r = 222 / 2.0.squareRoot()
    #expect(abs(start.x - (233 - r)) < 1e-9 && abs(start.y - (233 + r)) < 1e-9)
    #expect(abs(top.x - 233) < 1e-9 && abs(top.y - 11) < 1e-9)
    #expect(abs(end.x - (233 + r)) < 1e-9 && abs(end.y - (233 + r)) < 1e-9)
}

@Test func mercuryTopIsClampedToTheTube() {
    #expect(KnobTempFace.mercuryTop(12, w.thermo) == 144)
    #expect(KnobTempFace.mercuryTop(31, w.thermo) == 122)
    #expect(KnobTempFace.mercuryTop(40, w.thermo) == 122)
    #expect(KnobTempFace.mercuryTop(-20, w.thermo) == 178)
}

@Test func faceDotsSitAtTheBottomWithTheTemperatureDotFirst() {
    let first = KnobTempFace.dotPoint(0, w.dots, center: 233), mid = KnobTempFace.dotPoint(3, w.dots, center: 233)
    #expect(abs(mid.x - 233) < 1e-9 && abs(mid.y - (233 + 206)) < 1e-9)
    #expect(first.x < mid.x && first.y < mid.y)
}

@Test func tempFaceTextMatchesTheFirmware() {
    #expect(KnobTempFace.degrees(-4) == "\u{2212}4°" && KnobTempFace.degrees(7) == "7°")
    #expect(KnobTempFace.sky(.init(face: .clearDay)) == "Sunny")
    #expect(KnobTempFace.sky(.init(face: .clearNight, night: true)) == "Clear")
    #expect(KnobTempFace.sky(.init(face: .rain, intensity: .heavy)) == "Heavy rain")
    #expect(KnobTempFace.sky(.init(face: .snow, intensity: .light)) == "Light snow")
    #expect(KnobTempFace.sky(.init(face: .rain, intensity: .moderate)) == "Rain")
    #expect(KnobTempFace.sky(.init(face: .overcast)) == "Overcast")
}

@Test func tempFaceReadsTheWeatherState() throws {
    let json = #"""
    {"generated_at":"2026-09-26T10:30:00+02:00","enabled":true,"provider":"open-meteo","units":"metric",
     "current":{"fetched_at":"2026-09-26T10:26:00+02:00","stale":false,"condition":"rain","condition_code":"61",
                "severe":false,"temp_c":11.5,"today_min_c":8.4,"today_max_c":16.6,"hourly":[]}}
    """#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let state = try d.decode(WeatherState.self, from: Data(json.utf8))
    let now = try #require(ISO8601DateFormatter().date(from: "2026-09-26T08:30:00Z"))
    let f = KnobTempFace(state: state, now: now)
    #expect(f == KnobTempFace(value: 11.5, todayMinC: 8.4, todayMaxC: 16.6, sub: "Rain", cloudBand: 4))
    #expect(f.valueColor(w) == KnobTempFace.color(12, w.scale))

    let old = KnobTempFace(state: state, now: now.addingTimeInterval(3 * 3600))
    #expect(old.still && old.valueColor(w) == w.stillColor)
    #expect(KnobTempFace(state: nil, now: now) == KnobTempFace(value: nil, still: true))
}

@Test func captionPartsSurviveAHugeServerTemperature() throws {
    for (temp, text) in [("1e300", "99°"), ("-1e300", "\u{2212}99°"), ("-3.6", "\u{2212}4°")] {
        let json = #"{"generated_at":"2026-09-26T10:30:00+02:00","enabled":true,"provider":"open-meteo","units":"metric","#
            + #""current":{"fetched_at":"2026-09-26T10:26:00+02:00","stale":false,"condition":"clear","condition_code":"0","#
            + #""severe":false,"temp_c":"# + temp + #","today_min_c":-1e300,"today_max_c":1e300,"hourly":[]}}"#
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        let state = try d.decode(WeatherState.self, from: Data(json.utf8))
        let f = KnobTempFace(state: state, now: try #require(ISO8601DateFormatter().date(from: "2026-09-26T08:30:00Z")))
        #expect(f.valueText() == text && !f.still && (f.sub == "Sunny" || f.sub == "Clear"))
        #expect(f.lo == -99 && f.hi == 99)
        #expect(f.caption == .current(sky: f.sub, temp: text))
    }
}

@Test func weatherCaptionNamesTheSkyAndTemperatureAndFlagsAStaleReading() throws {
    let json = #"""
    {"generated_at":"2026-09-26T10:30:00+02:00","enabled":true,"provider":"open-meteo","units":"metric",
     "current":{"fetched_at":"2026-09-26T10:26:00+02:00","stale":false,"condition":"rain","condition_code":"61",
                "severe":false,"temp_c":13.6,"today_min_c":8.4,"today_max_c":16.6,"hourly":[]}}
    """#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let state = try d.decode(WeatherState.self, from: Data(json.utf8))
    let now = try #require(ISO8601DateFormatter().date(from: "2026-09-26T08:30:00Z"))
    #expect(KnobTempFace(state: state, now: now).caption == .current(sky: "Rain", temp: "14°"))
    #expect(KnobTempFace(state: state, now: now.addingTimeInterval(3 * 3600)).caption == .stale(sky: "Rain", temp: "14°"))
    #expect(KnobTempFace(state: nil, now: now).caption == .noData)
    #expect(KnobTempFace(value: .nan, sub: "Rain").caption == .noData)
    #expect(KnobTempFace(value: -6, still: true, sub: "Light snow").caption == .stale(sky: "Light snow", temp: "\u{2212}6°"))
}

private func skyState(condition: String, code: String, cloud: String = "null", uv: String = "null",
                      sun: Bool = true) throws -> WeatherState {
    let json = #"{"generated_at":"2026-09-26T10:30:00Z","enabled":true,"provider":"open-meteo","units":"metric","#
        + #""current":{"fetched_at":"2026-09-26T10:26:00Z","stale":false,"condition":""# + condition
        + #"","condition_code":""# + code + #"","severe":false,"temp_c":31.2,"today_min_c":24,"today_max_c":34,"#
        + #""cloud_pct":"# + cloud + #","uv":"# + uv + #","hourly":[]}"#
        + (sun ? #","sun":{"sunrise":"2026-09-26T06:00:00Z","sunset":"2026-09-26T18:00:00Z"}}"# : "}")
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(WeatherState.self, from: Data(json.utf8))
}

private let utc: Calendar = {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "UTC")!
    return c
}()
private let noon = ISO8601DateFormatter().date(from: "2026-09-26T10:30:00Z")!
private let midnight = ISO8601DateFormatter().date(from: "2026-09-26T22:30:00Z")!

private func face(_ s: WeatherState, _ now: Date = noon, shown: Int? = nil) -> KnobTempFace {
    KnobTempFace(state: s, now: now, calendar: utc, shownBand: shown)
}

@Test func uvAdviceAndColourMatchCinder() {
    #expect(KnobTempFace.uvAdvice(0, w.uv) == nil && KnobTempFace.uvAdvice(2, w.uv) == nil)
    #expect(KnobTempFace.uvAdvice(3, w.uv) == "Sunglasses on" && KnobTempFace.uvColor(5, w.uv) == RGB(hex: "#F0E641"))
    #expect(KnobTempFace.uvAdvice(6, w.uv) == "Use sunscreen" && KnobTempFace.uvColor(7, w.uv) == RGB(hex: "#F08A3A"))
    #expect(KnobTempFace.uvAdvice(8, w.uv) == "Sunscreen & shade" && KnobTempFace.uvColor(10, w.uv) == RGB(hex: "#FF5050"))
    #expect(KnobTempFace.uvAdvice(11, w.uv) == "Avoid the sun" && KnobTempFace.uvColor(14, w.uv) == RGB(hex: "#B060E0"))
    #expect(KnobTempFace.uvAdvice(5, w.uv) == "Sunglasses on" && KnobTempFace.uvAdvice(7, w.uv) == "Use sunscreen")
    #expect(KnobTempFace.uvAdvice(10, w.uv) == "Sunscreen & shade")
}

@Test func cloudBandKeepMatchesCinder() {
    let keep: [(Int, Int?, Int)] = [
        (72, nil, 3), (72, 2, 2), (74, 2, 2), (75, 2, 3), (36, 2, 2), (35, 2, 2), (34, 2, 1), (14, 0, 0),
        (15, 0, 1), (85, 4, 4), (84, 4, 3), (5, 1, 1), (4, 1, 0), (94, 3, 3), (95, 3, 4), (44, 1, 1), (45, 1, 2),
    ]
    for (cloud, shown, want) in keep {
        #expect(KnobTempFace.cloudBandKeep(cloud, shown: shown, w.cloud) == want, "cloud \(cloud)% shown \(String(describing: shown))")
    }
    #expect([0, 9, 10, 39, 40, 69, 70, 89, 90, 100].map { KnobTempFace.cloudBand($0, w.cloud) } == [0, 0, 1, 1, 2, 2, 3, 3, 4, 4])
    #expect(KnobTempFace.cloudBandKeep(50, shown: 7, w.cloud) == 2 && KnobTempFace.cloudBandKeep(50, shown: -1, w.cloud) == 2)
}

@Test func subLineFollowsCloudCoverAndUVLikeTheKnob() throws {
    let advice = face(try skyState(condition: "clear", code: "0", cloud: "5", uv: "8"))
    #expect(advice.sub == "Sunscreen & shade" && advice.subColor == RGB(hex: "#FF5050") && advice.cloudBand == 0)
    let low = face(try skyState(condition: "clear", code: "0", cloud: "5", uv: "2"))
    #expect(low.sub == "Sunny" && low.subColor == nil)
    #expect(face(try skyState(condition: "clear", code: "0", cloud: "0", uv: "5"), midnight).sub == "Clear")
    let cloudyNight = face(try skyState(condition: "clear", code: "0", cloud: "80", uv: "5"), midnight)
    #expect(cloudyNight.sub == "Cloudy" && cloudyNight.cloudBand == 3 && cloudyNight.subColor == nil)
    #expect(face(try skyState(condition: "clouds", code: "2", cloud: "25")).sub == "Mostly sunny")
    #expect(face(try skyState(condition: "clouds", code: "2", cloud: "25"), midnight).sub == "Mostly clear")
    #expect(face(try skyState(condition: "clouds", code: "3", cloud: "62.4")).sub == "Partly cloudy")
    #expect(face(try skyState(condition: "clouds", code: "2", cloud: "95")).sub == "Overcast")
    let rain = face(try skyState(condition: "rain", code: "65", cloud: "38"))
    #expect(rain.sub == "Heavy rain" && rain.cloudBand == 1)
    #expect(face(try skyState(condition: "storm", code: "95", uv: "9")).sub == "Sunscreen & shade")
    #expect(face(try skyState(condition: "clear", code: "0", uv: "8", sun: false)).sub == "Sunscreen & shade")
    #expect(face(try skyState(condition: "clear", code: "0", uv: "2.6")).sub == "Sunglasses on")
}

@Test func subLineWithoutCloudOrUVKeepsTheConditionWord() throws {
    let partly = face(try skyState(condition: "clouds", code: "2"))
    #expect(partly.sub == "Partly cloudy" && partly.cloudBand == 2 && partly.subColor == nil)
    #expect(face(try skyState(condition: "clouds", code: "3")).sub == "Overcast")
    let clear = face(try skyState(condition: "clear", code: "0"), midnight)
    #expect(clear.sub == "Clear" && clear.cloudBand == 0)
    #expect(face(try skyState(condition: "rain", code: "65")).cloudBand == 4)
    for (cloud, uv) in [("101", "41"), ("-1", "-1")] {
        let bad = face(try skyState(condition: "clouds", code: "2", cloud: cloud, uv: uv))
        #expect(bad.sub == "Partly cloudy" && bad.subColor == nil, "cloud \(cloud) uv \(uv) are out of range")
    }
}

@Test func shownCloudBandHoldsNearAnEdge() throws {
    let first = face(try skyState(condition: "clouds", code: "2", cloud: "38"))
    #expect(first.cloudBand == 1 && first.sub == "Mostly sunny")
    for c in 38...44 {
        let f = face(try skyState(condition: "clouds", code: "2", cloud: "\(c)"), shown: first.cloudBand)
        #expect(f.cloudBand == 1 && f.sub == "Mostly sunny", "cloud \(c)% next to the 40% edge keeps the shown band")
    }
    let moved = face(try skyState(condition: "clouds", code: "2", cloud: "45"), shown: first.cloudBand)
    #expect(moved.cloudBand == 2 && moved.sub == "Partly cloudy")
    let fresh = face(try skyState(condition: "clouds", code: "2", cloud: "42"))
    #expect(fresh.cloudBand == 2 && fresh.sub == "Partly cloudy")
    let fromClear = face(try skyState(condition: "clear", code: "0", cloud: "12"), shown: 0)
    #expect(fromClear.sub == "Sunny" && fromClear.cloudBand == 0)
}

@Test func olderServersWithoutCloudAndUVStillDecode() throws {
    let json = #"{"generated_at":"2026-09-26T10:30:00Z","enabled":true,"provider":"open-meteo","units":"metric","#
        + #""current":{"fetched_at":"2026-09-26T10:26:00Z","stale":false,"condition":"clear","condition_code":"0","#
        + #""severe":false,"temp_c":20,"today_min_c":null,"today_max_c":null,"hourly":[]}}"#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let s = try d.decode(WeatherState.self, from: Data(json.utf8))
    #expect(s.current?.cloudPct == nil && s.current?.uv == nil)
    #expect(face(s).sub == "Sunny")
}

private func duskState(generated: String, night: String, cloud: String = "null") throws -> WeatherState {
    let json = #"{"generated_at":""# + generated + #"","enabled":true,"provider":"open-meteo","units":"metric","#
        + #""current":{"fetched_at":""# + generated + #"","stale":false,"condition":"clear","condition_code":"0","#
        + #""severe":false,"temp_c":24,"today_min_c":15,"today_max_c":26,"cloud_pct":"# + cloud + #","uv":5,"hourly":[]},"#
        + #""sun":{"sunrise":"2026-09-26T06:00:00Z","sunset":"2026-09-26T18:00:00Z","night":"# + night + "}}"
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(WeatherState.self, from: Data(json.utf8))
}

@Test func uvAdviceFollowsTheServersNightNotTheRoundedSunset() throws {
    let at = try #require(ISO8601DateFormatter().date(from: "2026-09-26T18:01:00Z"))
    let day = try duskState(generated: "2026-09-26T18:01:00Z", night: "false")
    #expect(face(day, at).sub == "Sunglasses on", "sunset 18:02 sent as 18:00: still day on the knob")
    let dark = try duskState(generated: "2026-09-26T17:58:00Z", night: "true")
    #expect(face(dark, try #require(ISO8601DateFormatter().date(from: "2026-09-26T17:59:00Z"))).sub == "Clear",
            "sunset 17:58 sent as 18:00: already night on the knob")
    let older = try duskState(generated: "2026-09-26T18:01:00Z", night: "null")
    #expect(face(older, at).sub == "Clear", "a server without sun.night falls back to the rounded times")
    let iso = ISO8601DateFormatter()
    let late = try duskState(generated: "2026-09-26T18:01:00Z", night: "false")
    #expect(face(late, try #require(iso.date(from: "2026-09-26T18:03:00Z"))).sub == "Clear",
            "fetched between the sent 18:00 and an exact sunset up to 18:02:30: night once that bound passes")
    #expect(face(late, try #require(iso.date(from: "2026-09-26T18:02:29Z"))).sub == "Sunglasses on",
            "30 s before the latest exact sunset the fetched day holds")
    #expect(face(late, try #require(iso.date(from: "2026-09-26T18:02:31Z"))).sub == "Clear",
            "30 s after the latest exact sunset it is night")
    let dawn = try duskState(generated: "2026-09-26T06:01:00Z", night: "true", cloud: "0")
    #expect(face(dawn, try #require(iso.date(from: "2026-09-26T06:02:00Z"))).sub == "Clear")
    #expect(face(dawn, try #require(iso.date(from: "2026-09-26T06:03:00Z"))).sub == "Sunglasses on",
            "the same bound at sunrise")
    let overnight = try duskState(generated: "2026-09-26T23:00:00Z", night: "true", cloud: "0")
    #expect(face(overnight, try #require(iso.date(from: "2026-09-27T06:05:00Z"))).sub == "Sunglasses on",
            "the bound wraps midnight: sunrise sent as 06:00 ends the fetched night next morning")
    let dayOld = try duskState(generated: "2026-09-25T17:00:00Z", night: "false")
    #expect(face(dayOld, try #require(iso.date(from: "2026-09-26T18:10:00Z"))).sub == "Clear", "a fetch a day old uses the rounded times")
    let future = try duskState(generated: "2026-09-26T19:00:00Z", night: "false")
    #expect(face(future, try #require(iso.date(from: "2026-09-26T18:30:00Z"))).sub == "Clear", "a fetch from the future uses the rounded times")
    let crossed = try duskState(generated: "2026-09-26T17:30:00Z", night: "false")
    #expect(face(crossed, try #require(ISO8601DateFormatter().date(from: "2026-09-26T18:30:00Z"))).sub == "Clear",
            "a sun event since the fetch overrides its night")
}

@MainActor @Test func weatherMemoHoldsTheShownBandOnlyFromAFaceWithData() throws {
    var memo = KnobWeatherMemo()
    let none = memo.face(state: nil, now: noon, calendar: utc)
    #expect(none.value == nil && memo.band == nil)
    let real = memo.face(state: try skyState(condition: "clouds", code: "3", cloud: "87"), now: noon, calendar: utc)
    #expect(real.cloudBand == 3 && real.sub == "Cloudy", "no band held over from the empty face")

    var fresh = KnobWeatherMemo()
    _ = fresh.face(state: try skyState(condition: "clouds", code: "2", cloud: "38"), now: noon, calendar: utc)
    let held = fresh.face(state: try skyState(condition: "clouds", code: "2", cloud: "42"), now: noon, calendar: utc)
    #expect(held.sub == "Mostly sunny" && fresh.band == 1)
    let caption = fresh.face(state: try skyState(condition: "clouds", code: "2", cloud: "42"), now: noon, calendar: utc).caption
    #expect(caption == .current(sky: "Mostly sunny", temp: "31°"), "the caption read from the same memo matches the drawn sub-line")
    _ = fresh.face(state: try skyState(condition: "clouds", code: "2", cloud: "45"), now: noon, calendar: utc)
    #expect(fresh.band == 2)
    #expect(KnobTempFace(value: nil, cloudBand: 4).cloudBand == nil && KnobTempFace(value: .nan, cloudBand: 4).cloudBand == nil)
}

private func polarState(generated: String, night: String, rise: String, set: String) throws -> WeatherState {
    let json = #"{"generated_at":""# + generated + #"","enabled":true,"provider":"open-meteo","units":"metric","#
        + #""current":{"fetched_at":""# + generated + #"","stale":false,"condition":"clear","condition_code":"0","#
        + #""severe":false,"temp_c":14,"today_min_c":8,"today_max_c":16,"cloud_pct":0,"uv":5,"hourly":[]},"#
        + #""sun":{"sunrise":""# + rise + #"","sunset":""# + set + #"","night":"# + night + "}}"
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(WeatherState.self, from: Data(json.utf8))
}

@Test func sunTimeSentAsMidnightStillEndsTheFetchedNight() throws {
    let iso = ISO8601DateFormatter()
    let set = try polarState(generated: "2026-06-10T23:59:00Z", night: "true",
                             rise: "2026-06-10T03:00:00Z", set: "2026-06-11T00:00:00Z")
    #expect(face(set, try #require(iso.date(from: "2026-06-11T00:30:00Z"))).sub == "Clear")
    #expect(face(set, try #require(iso.date(from: "2026-06-11T03:10:00Z"))).sub == "Sunglasses on")
    #expect(face(set, try #require(iso.date(from: "2026-06-11T12:00:00Z"))).sub == "Sunglasses on")
}

@Test func sunriseSentAsMidnightStillEndsTheFetchedDay() throws {
    let iso = ISO8601DateFormatter()
    let rise = try polarState(generated: "2026-06-10T23:59:00Z", night: "false",
                              rise: "2026-06-11T00:00:00Z", set: "2026-06-10T21:00:00Z")
    #expect(face(rise, try #require(iso.date(from: "2026-06-11T12:00:00Z"))).sub == "Sunglasses on")
    #expect(face(rise, try #require(iso.date(from: "2026-06-11T21:30:00Z"))).sub == "Clear")
}

private let ageNow: Int64 = 1_782_043_500
private func ageAt(_ t: Int64) -> Date { Date(timeIntervalSince1970: TimeInterval(t)) }

private func ageFaces(wind: Int, aqi: Int) -> WeatherState.Faces {
    .init(utcOffsetS: 3600, night: false, windKmh: wind, windDirDeg: nil, rainIntensity: nil, todayPopPct: nil, rain: nil,
          days: [], twilight: nil, moon: nil, aqi: aqi)
}

private func ageState(fetched: Int64 = ageNow, generated: Int64, faces: WeatherState.Faces? = nil, stale: Bool = false,
                      enabled: Bool = true) -> WeatherState {
    WeatherState(generatedAt: ageAt(generated), enabled: enabled, provider: "open-meteo", units: "metric", locationName: nil,
                 current: .init(fetchedAt: ageAt(fetched), stale: stale, condition: "clouds", conditionCode: "3", severe: false,
                                tempC: 12, todayMinC: nil, todayMaxC: nil, cloudPct: nil, uv: nil, hourly: []),
                 air: nil, sun: nil, faces: faces)
}

@Test func tempFaceExpiresByTheServersGenerationLikeTheKnob() {
    let fresh = KnobTempFace(state: ageState(fetched: ageNow - 7200, generated: ageNow - 1800), now: ageAt(ageNow))
    #expect(!fresh.still, "an old provider fetch the server still vouches for is fresh")
    #expect(KnobTempFace(state: ageState(fetched: ageNow - 60, generated: ageNow - 1801), now: ageAt(ageNow)).still, "strictly over 1800 s")
    #expect(KnobTempFace(state: ageState(generated: ageNow - 1800), now: ageAt(ageNow)).caption == .current(sky: "Overcast", temp: "12°"))
    #expect(KnobTempFace(state: ageState(generated: ageNow - 1801), now: ageAt(ageNow)).caption == .stale(sky: "Overcast", temp: "12°"))
    #expect(KnobTempFace(state: ageState(generated: ageNow, stale: true), now: ageAt(ageNow)).still)
    #expect(KnobTempFace(state: ageState(generated: ageNow, enabled: false), now: ageAt(ageNow)).still)
}

@Test func sixFacesExpireAtTheSame1800sBoundary() {
    let windy = ageFaces(wind: 20, aqi: 42)
    #expect(KnobWindFace(state: ageState(generated: ageNow - 1800, faces: windy), now: ageAt(ageNow)).have)
    #expect(!KnobWindFace(state: ageState(generated: ageNow - 1801, faces: windy), now: ageAt(ageNow)).have)
    #expect(KnobAirFace(state: ageState(generated: ageNow - 1800, faces: windy), now: ageAt(ageNow)).have)
    #expect(!KnobAirFace(state: ageState(generated: ageNow - 1801, faces: windy), now: ageAt(ageNow)).have)
}
