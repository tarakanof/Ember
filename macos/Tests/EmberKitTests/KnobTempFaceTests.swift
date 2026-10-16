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
    #expect(f == KnobTempFace(value: 11.5, todayMinC: 8.4, todayMaxC: 16.6, sub: "Rain"))
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
    }
}
