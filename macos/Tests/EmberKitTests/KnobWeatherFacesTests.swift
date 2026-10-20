import Foundation
import Testing
@testable import EmberKit

private let w = KnobTheme.standard.weather
private func at(_ t: Int64) -> Date { Date(timeIntervalSince1970: TimeInterval(t)) }

private func faces(offset: Int = 3600) -> WeatherState.Faces {
    .init(utcOffsetS: offset, night: false, windKmh: nil, windDirDeg: nil, rainIntensity: nil, todayPopPct: nil,
          rain: nil, days: [], twilight: nil, moon: .init(phasePct: 40, illumPct: 80, up: nil), aqi: nil)
}

private func spells(_ s: [(Int64, Int64, Int, Bool)], until: Int64? = nil) -> WeatherState.Faces.Rain {
    .init(until: until.map(at), spells: s.map { .init(start: at($0.0), end: at($0.1), intensity: $0.2, snow: $0.3) })
}

private func state(_ cond: String = "clouds", _ code: String = "3", fetched: Int64, generated: Int64? = nil,
                   faces f: WeatherState.Faces? = faces(), stale: Bool = false, enabled: Bool = true,
                   sun: (Int64, Int64)? = nil) -> WeatherState {
    WeatherState(generatedAt: at(generated ?? fetched), enabled: enabled, provider: "open-meteo", units: "metric", locationName: nil,
                 current: .init(fetchedAt: at(fetched), stale: stale, condition: cond, conditionCode: code, severe: false, tempC: 12,
                                todayMinC: nil, todayMaxC: nil, cloudPct: nil, uv: nil, hourly: []),
                 air: nil, sun: sun.map { .init(sunrise: at($0.0), sunset: at($0.1), night: nil) }, faces: f)
}

private let observed: Int64 = 1_782_043_200
private let knobNow = observed + 300

private func wet(_ cond: String, _ code: String, nowRain: Int, until: Int64, kind snow: Bool = false,
                 pop: Int? = nil) -> WeatherState {
    var f = faces()
    f.rainIntensity = nowRain
    f.todayPopPct = pop
    f.rain = spells([(observed - 900, until != 0 ? until : observed + 7200, 2, snow)], until: until != 0 ? until : nil)
    return state(cond, code, fetched: observed, faces: f)
}

private func rain(_ s: WeatherState, shown: KnobRainFace? = nil) -> KnobRainFace { KnobRainFace(state: s, now: at(knobNow), shown: shown) }

@Test func rainNowPicksTheSpellAndUntilLikeCinder() {
    let r = rain(wet("rain", "61", nowRain: 1, until: 1_782_049_500, pop: 83))
    #expect(!r.dry && r.kind == .rain && r.intensity == 2 && r.pop == 83)
    #expect(r.untilMin == 14 * 60 + 45 && r.sub == "until 14:45")

    var s = wet("rain", "61", nowRain: 1, until: 1_782_049_500)
    s.faces?.rain?.spells[0].intensity = 3
    #expect(rain(s).intensity == 3, "a heavy spell: heavy")
    s.faces?.rain?.spells = []
    #expect(rain(s).intensity == 1, "no spell covers now: now.rain")
    s.faces?.rainIntensity = 3
    #expect(rain(s).intensity == 3)

    s = wet("rain", "61", nowRain: 0, until: 0)
    s.faces?.rain?.until = nil
    let pause = rain(s)
    #expect(!pause.dry && pause.intensity == 2 && pause.untilMin == nil && pause.sub == "Rain", "a pause inside a spell")
    s.faces?.rain?.spells = []
    #expect(rain(s).dry, "cond rain, now.rain 0 and no spell: not wet")

    s = wet("clouds", "3", nowRain: 2, until: 1_782_049_500, kind: true)
    #expect(rain(s).kind == .snow, "the spell's kind when the condition is dry")
    s = wet("snow", "73", nowRain: 2, until: 1_782_049_500)
    #expect(rain(s).kind == .snow, "the condition wins over the spell's kind")

    s = wet("rain", "61", nowRain: 2, until: knobNow)
    s.faces?.rain?.spells = []
    #expect(rain(s).dry, "until == now: dry")
    s.faces?.rain?.until = at(knobNow + 1)
    #expect(!rain(s).dry && rain(s).untilMin != nil)

    var storm = faces()
    storm.rainIntensity = 0
    storm.rain = spells([])
    let st = rain(state("storm", "95", fetched: observed, faces: storm))
    #expect(!st.dry && st.kind == .storm && st.intensity == 2 && st.sub == "Storm")
    #expect(rain(state("storm", "99", fetched: observed, faces: storm)).intensity == 3)
}

@Test func rainNowFallsBackToTheDryFaceAndKeepsTheShownIntensity() {
    var f = faces()
    f.todayPopPct = 40
    f.rainIntensity = 0
    f.rain = spells([])
    let dry = rain(state(fetched: observed, faces: f))
    #expect(dry == KnobRainFace(dry: true, intensity: 1, pop: 40, sub: "Dry now"))
    #expect(rain(state(fetched: observed, faces: f, stale: true)) == KnobRainFace(dry: true, intensity: 1))
    #expect(KnobRainFace(state: nil, now: .now) == KnobRainFace(dry: true, intensity: 1))

    let heavy = KnobRainFace(dry: false, intensity: 3, sub: "Heavy rain")
    var s = wet("rain", "61", nowRain: 1, until: 0)
    s.faces?.rain?.until = nil
    s.faces?.rain?.spells[0].intensity = 1
    let kept = rain(s, shown: heavy)
    #expect(kept.intensity == 3 && kept.sub == "Heavy rain", "a lower intensity keeps the shown one and its word")
    #expect(rain(s, shown: KnobRainFace(dry: false, kind: .snow, intensity: 3)).intensity == 1, "another kind keeps nothing")
}

@Test func rainColoursAndRingMatchCinder() {
    #expect(KnobRainFace.color(1, w.rain) == RGB(hex: "#4F81B7") && KnobRainFace.color(2, w.rain) == RGB(hex: "#5C9CE0"))
    #expect(KnobRainFace.color(3, w.rain) == RGB(hex: "#3D7BF5") && KnobRainFace.color(9, w.rain) == RGB(hex: "#3D7BF5"))
    let r = KnobRainFace(dry: false, intensity: 3, pop: 80)
    #expect(r.ringColor(0, w) == RGB(hex: "#2E3A48") && r.ringColor(1, w) == RGB(hex: "#3D7BF5"))
    #expect(r.ringColor(0.5, w) == RGB(r: 0x36, g: 0x5B, b: 0x9F))
    #expect(KnobRainFace(dry: true, intensity: 1).ringColor(0.5, w) == w.ring.trackColor && r.fraction == 0.8)
}

private let day: Int64 = 1_782_000_000
private func local(_ h: Int64, _ m: Int64) -> Int64 { day + h * 3600 + m * 60 - 3600 }
private let castNow = local(14, 30)

private func castState(_ s: [(Int64, Int64, Int, Bool)], until: Int64? = nil, nowRain: Int = 0, cond: String = "clouds",
                       code: String = "3") -> WeatherState {
    var f = faces()
    f.rainIntensity = nowRain
    f.rain = spells(s, until: until)
    return state(cond, code, fetched: castNow - 300, faces: f)
}

private func cast(_ s: WeatherState, now: Int64 = castNow, shown: KnobCastFace? = nil) -> KnobCastFace {
    KnobCastFace(state: s, now: at(now), shown: shown)
}

private func wetUntil(_ until: Int64) -> WeatherState {
    castState([(local(14, 0), until, 2, false), (local(20, 0), local(21, 0), 3, false)], until: until, nowRain: 2,
              cond: "rain", code: "63")
}

@Test func castRoundsAndCountsLikeCinder() {
    let cases: [(Int64, Bool, Int, String)] = [
        (45, false, 9, "45"), (150, true, 5, "2½"), (90, true, 3, "1½"), (57, false, 11, "55"), (58, true, 2, "1"), (0, false, 1, "5"),
        (2, false, 1, "5"), (3, false, 1, "5"), (8, false, 2, "10"), (75, true, 3, "1½"), (74, true, 2, "1"), (719, true, 24, "12"),
        (105, true, 4, "2"), (30, false, 6, "30"),
    ]
    for (min, hours, count, text) in cases {
        let r = KnobCastFace.round(min * 60)
        #expect(r.hours == hours && r.count == count && KnobCount.text(hours: r.hours, count: r.count) == text, "\(min) min")
    }
    #expect(KnobCastFace.round(-50) == (false, 1))
    #expect(KnobCount.text(hours: true, count: 1) == "½")
}

@Test func castStatesMatchCinder() {
    var c = cast(wetUntil(local(15, 40)))
    #expect(c.wet && c.hasCount && c.hours && c.valueText == "1" && c.variant == 2)
    #expect(c.sub == "#5C9CE0 rain# until 15:40" && c.label == "15:40")
    #expect(abs(c.toDeg - (c.nowDeg + 35 - 1.5)) < 0.01)
    #expect(c.arcs == [.init(a: 150, n: 70, intensity: 2), .init(a: 480, n: 60, intensity: 3)])

    var s = wetUntil(local(15, 40))
    s.faces?.rain?.until = nil
    c = cast(s)
    #expect(c.wet && !c.hasCount && c.sub == "Rain" && c.variant == 2)
    s = castState([(local(14, 0), local(14, 25), 2, false), (local(20, 0), local(21, 0), 3, false)], until: local(14, 25),
                  nowRain: 2, cond: "rain", code: "63")
    c = cast(s)
    #expect(!c.wet && c.hasCount && c.variant == 3 && c.label == "20", "until passed: dry, the next spell")

    c = cast(castState([]))
    #expect(c.have && c.dry && !c.wet && !c.hasCount && c.variant == 1 && c.arcs.isEmpty && c.sub == "No rain for 12 h")
    #expect(c.label.isEmpty && c.valueText == "12+" && c.unit == "h" && c.valueColor(w) == w.cast.dryColor)
    s = castState([])
    s.faces?.rain = nil
    c = cast(s)
    #expect(!c.have && !c.dry && c.variant == 0 && c.sub == "No forecast" && c.valueText == "--" && c.unit == nil)
    c = cast(castState([]).with { $0.faces = nil })
    #expect(!c.have && c.sub == "No forecast", "an older server: no offset, no forecast")
    c = cast(castState([]).with { $0.current?.stale = true })
    #expect(!c.have && c.variant == 0 && c.sub.isEmpty && c.valueColor(w) == w.stillColor)

    c = cast(castState([(local(18, 0), local(19, 0), 3, true)]))
    #expect(c.snow && c.variant == 3 && c.sub == "#5C9CE0 heavy snow# at 18:00")
    c = cast(castState([(local(22, 0), local(23, 0), 3, false), (local(16, 0), local(17, 0), 1, false)]))
    #expect(c.variant == 1 && c.label == "16", "the earliest spell counts")

    s = wetUntil(local(15, 40))
    s.faces?.rain?.spells[0].intensity = 3
    let heavy = cast(s)
    s.faces?.rain?.spells[0].intensity = 2
    c = cast(s, shown: heavy)
    #expect(c.variant == 3 && c.sub.contains("heavy rain"))
    s.faces?.rain?.spells[0].intensity = 1
    s.faces?.rain?.spells[0].snow = true
    c = cast(s, shown: heavy)
    #expect(c.variant == 1 && c.snow)
}

@Test func castArcsMatchCinder() {
    var c = cast(castState([(local(23, 0), local(4, 0) + 86_400, 2, false)]))
    #expect(c.arcs == [.init(a: 660, n: 180, intensity: 2)], "cut at now + 12 h")
    c = cast(wetUntil(local(15, 40)), now: castNow + 360)
    #expect(c.arcs.first?.a == 155, "past the 5-min step the wet arc starts later")
    c = cast(castState([(local(14, 0), local(14, 20), 1, false)]))
    #expect(c.arcs.isEmpty && !c.hasCount)
    c = cast(castState([(local(14, 0), local(14, 33), 1, false)]), now: castNow + 60)
    #expect(c.wet && c.arcs == [.init(a: 150, n: 3, intensity: 1)])
    c = cast(castState([(local(15, 0), local(15, 0) + 60, 1, false)]))
    #expect(c.arcs.count == 1 && c.arcs[0].n == 1)
    c = cast(castState([(local(15, 0), local(15, 0) + 59, 1, false)]))
    #expect(c.arcs.isEmpty)
    let segs = cast(castState([(local(14, 50), local(16, 10), 2, false)])).segments
    #expect(segs.map(\.from) == [85, 91.5, 121.5] && segs.map(\.to) == [88.5, 118.5, 125], "only the hour cells light, not their gaps")
}

@Test func castLabelPlacementMatchesCinder() {
    let (p, align) = KnobCastFace.labelAnchor(90)
    #expect(align == -1 && abs(p.x - (233 + 198)) < 1e-4 && abs(p.y - 233) < 1e-4)
    #expect(KnobCastFace.labelAnchor(0).align == 0 && KnobCastFace.labelAnchor(270).align == 1 && KnobCastFace.labelAnchor(-90).align == 1)
    let avoid = CGRect(x: 160, y: 302, width: 146, height: 33)
    #expect(KnobCastFace.labelPlace(0, w: 30, h: 27, avoid: avoid, gap: 2) == CGPoint(x: 218, y: 32))
    #expect(KnobCastFace.labelPlace(180, w: 30, h: 27, avoid: CGRect(x: 160, y: 400, width: 146, height: 33), gap: 2) == nil)
}

private func windState(_ kmh: Int?, _ dir: Int?) -> WeatherState {
    var f = faces()
    f.windKmh = kmh
    f.windDirDeg = dir
    return state(fetched: observed, faces: f)
}

@Test func windBandsCompassAndLevelsMatchCinder() {
    let bands: [(Int, String, String)] = [
        (0, "#9AA4B0", "Calm"), (1, "#9AA4B0", "Light air"), (5, "#9AA4B0", "Light air"), (6, "#4FD6CC", "Light breeze"),
        (11, "#4FD6CC", "Light breeze"), (12, "#8ACF7A", "Gentle breeze"), (19, "#8ACF7A", "Gentle breeze"),
        (20, "#C9D25A", "Moderate breeze"), (29, "#E8D44A", "Fresh breeze"), (39, "#F08A3A", "Strong breeze"),
        (50, "#FF5050", "Near gale"), (62, "#D03060", "Gale"), (75, "#D03060", "Strong gale"), (89, "#D03060", "Storm"),
        (103, "#D03060", "Violent storm"), (118, "#D03060", "Hurricane"), (999, "#D03060", "Hurricane"),
    ]
    for (kmh, hex, name) in bands {
        #expect(KnobWindFace.color(kmh, w.wind) == RGB(hex: hex) && KnobWindFace.band(kmh, w.wind) == name, "\(kmh) km/h")
    }
    #expect(KnobWindFace.dark(RGB(hex: "#8ACF7A")!, w.wind) == RGB(hex: "#4C7243"))
    #expect(KnobWindFace.dark(RGB(hex: "#F08A3A")!, w.wind) == RGB(hex: "#844C20"))
    #expect(KnobWindFace.dark(RGB(hex: "#D03060")!, w.wind) == RGB(hex: "#721A35"))
    let compass: [(Int, String)] = [(0, "N"), (22, "N"), (23, "NE"), (67, "NE"), (68, "E"), (202, "S"), (203, "SW"), (337, "NW"),
                                    (338, "N"), (360, "N"), (-45, "NW"), (-1, "N"), (810, "E")]
    for (d, p) in compass { #expect(KnobWindFace.compass(d) == p, "\(d)°") }
    let keep: [(Int, Int?, Int)] = [(14, 0, 0), (15, 0, 1), (9, 1, 1), (8, 1, 0), (31, 1, 1), (32, 1, 2), (26, 2, 2), (25, 2, 1),
                                    (40, 0, 2), (3, 2, 0), (14, nil, 1), (14, 3, 1), (12, 1, 1), (0, 0, 0)]
    for (kmh, shown, want) in keep { #expect(KnobWindFace.levelKeep(kmh, shown: shown, w.wind) == want, "\(kmh) shown \(String(describing: shown))") }
}

@Test func windFaceFollowsTheKnobView() {
    let cases: [(Int, Int?, Int, String)] = [
        (18, 225, 1, "#8ACF7A Gentle breeze# \u{2022} SW"), (4, 45, 0, "#9AA4B0 Light air# \u{2022} NE"),
        (45, 270, 2, "#F08A3A Strong breeze# \u{2022} W"), (0, 90, 0, "#9AA4B0 Calm#"), (21, nil, 1, "#C9D25A Moderate breeze#"),
        (130, 315, 2, "#D03060 Hurricane# \u{2022} NW"), (25, 359, 1, "#C9D25A Moderate breeze# \u{2022} N"),
        (110, 315, 2, "#D03060 Violent storm# \u{2022} NW"),
    ]
    for (kmh, dir, level, sub) in cases {
        let f = KnobWindFace(state: windState(kmh, dir), now: at(knobNow))
        #expect(f.have && f.kmh == kmh && f.level == level && f.sub == sub, "\(kmh) km/h: \(f.sub)")
    }
    var f = KnobWindFace(state: windState(18, 225), now: at(knobNow))
    #expect(abs(f.fraction - 0.36) < 1e-12 && f.dir == 225 && f.valueText == "18" && f.valueColor(w) == RGB(hex: "#8ACF7A"))
    #expect(KnobWindFace(state: windState(130, 0), now: at(knobNow)).fraction == 1)
    #expect(KnobWindFace(state: windState(10, 360), now: at(knobNow)).dir == 0)
    f = KnobWindFace(state: windState(10, 0), now: at(knobNow), shown: KnobWindFace(state: windState(14, 0), now: at(knobNow)))
    #expect(f.level == 1, "10 km/h with the gentle sock shown: kept")
    #expect(KnobWindFace(state: windState(10, 0), now: at(knobNow), shown: KnobWindFace()).level == 0, "a shown face without data")
    for s in [windState(nil, 90), windState(20, 0).with { $0.current?.stale = true }, windState(20, 0).with { $0.enabled = false },
              windState(1000, 0)] {
        let n = KnobWindFace(state: s, now: at(knobNow))
        #expect(!n.have && n.sub.isEmpty && n.valueText == "--" && n.valueColor(w) == w.stillColor)
    }
    #expect(KnobWindFace(state: windState(20, 0), now: at(knobNow + 3600)).have == false, "old data")
    #expect(f.ringColor(0, w) == RGB(hex: "#9AA4B0") && f.ringColor(1, w) == RGB(hex: "#FF5050"))
}

private let mid: Int64 = 1_781_996_400
private func h(_ hh: Int64, _ mm: Int64) -> Int64 { mid + hh * 3600 + mm * 60 }

private func sunState(_ now: Int64, twilight: Bool = true) -> WeatherState {
    var f = faces()
    f.moon?.up = true
    if twilight {
        f.twilight = .init(dawn: at(h(6, 55)), blueEnd: at(h(7, 13)), goldenEnd: at(h(8, 30)), goldenStart: at(h(18, 30)),
                           blueStart: at(h(19, 47)), dusk: at(h(20, 5)))
    }
    return state("clear", "0", fetched: now - 300, faces: f, sun: (h(7, 30), h(19, 30)))
}

private func sun(_ now: Int64) -> KnobSunFace { KnobSunFace(state: sunState(now), now: at(now)) }

@Test func sunFaceMatchesCinderAS2() {
    let s = sun(h(10, 30))
    #expect(s.have && s.hasNow && s.polar == .none && s.phase == .day && s.hours && s.count == 18 && s.unit == "h left")
    #expect(s.sub == "golden hour 18:30" && s.subColor == w.sun.goldColor && s.rise == "07:30" && s.set == "19:30")
    #expect(abs(s.fraction - 215.0 / 790) < 1e-4 && s.start == h(6, 55) && s.end == h(20, 5) && s.valueText == "9")
    let phases: [(Int64, Int64, Int64, KnobSunFace.Phase, Bool, Int, String, String, RGB)] = [
        (0, 0, 0, .before, true, 15, "h", "until sunrise 07:30", w.sun.horizonColor),
        (6, 30, 1, .before, false, 60, "min", "until sunrise 07:30", w.sun.horizonColor),
        (7, 29, 59, .before, false, 1, "min", "until sunrise 07:30", w.sun.horizonColor),
        (7, 30, 0, .morning, true, 24, "h left", "golden hour to 08:30", w.sun.goldColor),
        (8, 30, 0, .day, true, 22, "h left", "golden hour 18:30", w.sun.goldColor),
        (18, 30, 0, .evening, true, 2, "h left", "sunset 19:30", w.sun.horizonColor),
        (18, 30, 1, .evening, false, 60, "min left", "sunset 19:30", w.sun.horizonColor),
        (19, 30, 0, .after, true, 24, "h", "of daylight today", w.sun.subColor),
        (23, 59, 59, .after, true, 24, "h", "of daylight today", w.sun.subColor),
    ]
    for (hh, mm, ss, phase, hours, count, unit, sub, color) in phases {
        let s = sun(h(hh, mm) + ss)
        #expect(s.phase == phase && s.hours == hours && s.count == count && s.unit == unit && s.sub == sub && s.subColor == color,
                "\(hh):\(mm):\(ss): \(s.sub)")
    }
    #expect(sun(h(20, 5)).fraction == 1 && sun(h(6, 55)).fraction == 0 && sun(h(1, 0)).fraction == 0)
    let rounds: [(Int64, Bool, Int)] = [(-5, false, 1), (0, false, 1), (61, false, 2), (3541, false, 60), (3600, true, 2),
                                        (5400, true, 3), (86_399, true, 47), (172_800 + 1800, true, 96)]
    for (secs, hours, count) in rounds { #expect(KnobSunFace.round(secs, w.sun) == (hours, count), "\(secs) s") }
}

@Test func sunFaceWithoutTimesPolarAndRolledDays() {
    var s = KnobSunFace(state: sunState(h(10, 30), twilight: false), now: at(h(10, 30)))
    #expect(s.have && s.start == h(7, 30) && s.end == h(19, 30) && s.sub == "sunset 19:30", "no twilight: the gauge runs sunrise to sunset")
    var polar = faces()
    polar.night = true
    polar.moon?.up = false
    s = KnobSunFace(state: state(fetched: h(10, 0), faces: polar), now: at(h(10, 0)))
    #expect(!s.have && s.polar == .night && s.sub == "Polar night" && !s.hero && s.valueText == "--")
    polar.night = false
    s = KnobSunFace(state: state(fetched: h(10, 0), faces: polar), now: at(h(10, 0)))
    #expect(s.polar == .day && s.sub == "Midnight sun" && s.hero)
    polar.moon?.up = nil
    #expect(KnobSunFace(state: state(fetched: h(10, 0), faces: polar), now: at(h(10, 0))).polar == .none, "no location: no polar word")
    s = KnobSunFace(state: sunState(h(10, 30)), now: at(h(10, 30) + 86_400))
    #expect(s.have && s.rise == "07:30" && s.start == h(6, 55) + 86_400, "a day later the times move on by a day")
    s = KnobSunFace(state: sunState(h(10, 30)), now: at(h(10, 30) + 8 * 86_400))
    #expect(!s.have, "past a week: no gauge")
    #expect(sun(h(10, 30)).color(at: h(13, 30), w.sun) == w.sun.dayNoon && sun(h(10, 30)).color(at: h(6, 55), w.sun) == w.sun.blueOut)
    #expect(sun(h(10, 30)).color(at: h(7, 30) + 1020, w.sun) == w.sun.horizonColor)
}

@Test func airFaceMatchesCinder() {
    let colours: [(Int, String)] = [(13, "#4FD5C7"), (25, "#50CFB3"), (37, "#88D585"), (55, "#F4C145"), (60, "#F89B49"),
                                    (68, "#FE5F4F"), (75, "#F34854"), (85, "#DC385C"), (0, "#4FD6CC"), (999, "#D03060")]
    for (aqi, hex) in colours { #expect(KnobAirFace.color(aqi, w.air) == RGB(hex: hex), "\(aqi)") }
    let bands: [(Int, Int, String, String)] = [(20, 0, "Good", "#4FD6CC"), (21, 1, "Fair", "#50CCAA"), (60, 2, "Moderate", "#F0E641"),
                                               (61, 3, "Poor", "#FF5050"), (100, 4, "Very poor", "#D03060"),
                                               (101, 5, "Extremely poor", "#B060E0"), (999, 5, "Extremely poor", "#B060E0")]
    for (aqi, band, word, hex) in bands {
        let b = KnobAirFace.band(aqi, w.air)
        #expect(b == band && KnobAirFace.word(b) == word && KnobAirFace.bandColor(b, w.air) == RGB(hex: hex), "\(aqi)")
    }
    #expect(!KnobAirFace.wilted(60, shown: nil, w.air) && KnobAirFace.wilted(61, shown: nil, w.air))
    #expect(KnobAirFace.wilted(56, shown: true, w.air) && !KnobAirFace.wilted(55, shown: true, w.air))
    #expect(!KnobAirFace.wilted(65, shown: false, w.air) && KnobAirFace.wilted(66, shown: false, w.air))
    func air(_ aqi: Int?, shown: KnobAirFace? = nil, stale: Bool = false) -> KnobAirFace {
        var f = faces()
        f.aqi = aqi
        return KnobAirFace(state: state(fetched: observed, faces: f, stale: stale), now: at(knobNow), shown: shown)
    }
    var a = air(42)
    #expect(a.have && a.aqi == 42 && a.band == 2 && !a.wilted && abs(a.fraction - 0.42) < 1e-12 && a.sub == "Moderate" && a.unit == "AQI")
    a = air(68)
    #expect(a.band == 3 && a.wilted && a.subColor(w) == RGB(hex: "#FF5050"))
    #expect(air(250).fraction == 1 && air(250).band == 5)
    #expect(air(58, shown: air(68)).wilted && !air(58).wilted && !air(58, shown: KnobAirFace()).wilted)
    for n in [air(nil), air(42, stale: true), KnobAirFace(state: nil, now: .now)] {
        #expect(!n.have && n.valueText == "--" && n.unit == nil && n.sub.isEmpty && n.valueColor(w) == w.stillColor)
    }
}

private func dayEntry(_ start: Int64, _ cond: String, _ code: String, _ lo: Int?, _ hi: Int?, pop: Int? = nil,
                      end: Int64? = nil) -> WeatherState.Faces.Day {
    .init(start: at(start), end: at(end ?? start + 86_400), condition: cond, conditionCode: code, minC: lo, maxC: hi, popPct: pop)
}

@Test func daysFaceRowsMatchCinder() {
    let t0 = local(0, 0) + 86_400
    var f = faces()
    f.days = [dayEntry(t0, "rain", "63", 9, 14, pop: 70), dayEntry(t0 + 86_400, "clear", "0", 7, 19, pop: 5),
              dayEntry(t0 + 2 * 86_400, "snow", "73", -3, 1, pop: 40)]
    var d = KnobDaysFace(state: state(fetched: castNow - 300, faces: f), now: at(castNow))
    #expect(d.rows.map(\.wday) == [KnobDaysFace.weekday(t0, offset: 3600), (KnobDaysFace.weekday(t0, offset: 3600) + 1) % 7,
                                    (KnobDaysFace.weekday(t0, offset: 3600) + 2) % 7])
    #expect(d.rows.map(\.icon) == [.rain, .sun, .snow] && d.rows.map(\.pop) == [70, nil, 40], "a chance only under a wet icon")
    #expect(d.amin == -3 && d.amax == 19)
    d = KnobDaysFace(state: state(fetched: castNow - 300, faces: f), now: at(t0 + 86_400 + 10))
    #expect(d.rows.count == 2 && d.rows[0].icon == .sun, "a day over by the clock is dropped")
    d = KnobDaysFace(state: state(fetched: castNow - 300, faces: f, stale: true), now: at(castNow))
    #expect(d.rows.count == 3, "stale data still lists the days, as on the knob")
    f.days = [dayEntry(t0, "rain", "63", 14, 9), dayEntry(t0 + 86_400, "fog", "45", 5, 5), dayEntry(t0, "clear", "0", 7, 19)]
    d = KnobDaysFace(state: state(fetched: castNow - 300, faces: f), now: at(castNow))
    #expect(d.rows.count == 1 && d.rows[0].icon == .fog && d.amin == 4 && d.amax == 6, "bad and out-of-order rows are skipped")
    #expect(!KnobDaysFace(state: state(fetched: castNow - 300, faces: f, enabled: false), now: at(castNow)).have)
    #expect(KnobDaysFace.weekday(1_782_000_000, offset: 0) == 0 && KnobDaysFace.name(-1) == "Sat" && KnobDaysFace.name(1) == "Mon")
    #expect(KnobDaysFace.dayEnd(0, 50_000, next: nil) == 0 && KnobDaysFace.dayEnd(0, 86_400, next: nil) == 86_400)
    #expect(KnobDaysFace.dayEnd(0, 0, next: 82_800) == 82_800)
}

@Test func daysLayoutMatchesCinder() {
    let t = w.days
    #expect(KnobDaysFace.degWidth(5) == 36 && KnobDaysFace.degWidth(-5) == 57 && KnobDaysFace.degWidth(10) == 52)
    #expect([1, 2, 3].map { KnobDaysFace.rowY(3, $0 - 1, t) } == [143, 228, 313] && KnobDaysFace.rowY(1, 0, t) == 228)
    let d = KnobDaysFace(rows: [.init(wday: 3, icon: .rain, pop: 70, lo: 9, hi: 14), .init(wday: 4, icon: .sun, lo: -12, hi: 21)])
    let c = d.cols(t)
    #expect(c.dayW == 70 && c.loW == 70 && c.hiW == 54 && c.barW == 78)
    #expect(c.x1 - c.x0 == 352 && c.x0 == 57 && c.iconX == 137 && c.loX == 187 && c.barX == 267 && c.hiX == 355)
    let (a, b) = d.span(0, barW: 100, t)
    #expect(abs(a - 21 * 100.0 / 33) < 1e-9 && abs(b - 26 * 100.0 / 33) < 1e-9)
    let narrow = KnobDaysFace(rows: [.init(wday: 0, icon: .sun, lo: 5, hi: 5)])
    #expect(narrow.amin == 4 && narrow.amax == 6 && narrow.span(0, barW: 100, t) == (45, 55))
}

@Test func subLineMarkupSplitsLikeLVGLRecolour() {
    #expect(KnobSubMarkup.runs("#5C9CE0 rain# at 13:00") == [.init(text: "rain", color: RGB(hex: "#5C9CE0")), .init(text: " at 13:00", color: nil)])
    #expect(KnobSubMarkup.plain("#9AA4B0 Calm#") == "Calm" && KnobSubMarkup.runs("Dry now") == [.init(text: "Dry now", color: nil)])
    #expect(KnobSubMarkup.plain("#bad") == "#bad")
}

@Test func facesExpireByTheServersGenerationAndStaleFlagLikeTheKnob() {
    var f = faces()
    f.windKmh = 20
    f.aqi = 42
    f.rainIntensity = 2
    f.rain = spells([(observed - 900, observed + 7200, 2, false)])
    let oldFetch = state("rain", "61", fetched: observed - 3600, generated: knobNow - 60, faces: f)
    #expect(KnobWindFace(state: oldFetch, now: at(knobNow)).have, "an hour-old provider fetch the server still calls fresh")
    #expect(KnobAirFace(state: oldFetch, now: at(knobNow)).have)
    #expect(!rain(oldFetch).dry && cast(oldFetch, now: knobNow).have)
    let oldView = state("rain", "61", fetched: knobNow - 60, generated: knobNow - 1801, faces: f)
    #expect(!KnobWindFace(state: oldView, now: at(knobNow)).have, "no new state for over 30 min")
    let stale = state("rain", "61", fetched: knobNow - 60, generated: knobNow - 60, faces: f, stale: true)
    #expect(!KnobWindFace(state: stale, now: at(knobNow)).have && !KnobAirFace(state: stale, now: at(knobNow)).have)
}

@Test func disabledWeatherGivesNoFaceData() {
    let on = sunState(h(10, 30))
    #expect(KnobSunFace(state: on, now: at(h(10, 30))).have)
    let off = KnobSunFace(state: on.with { $0.enabled = false }, now: at(h(10, 30)))
    #expect(!off.have && !off.hasNow && off.polar == .none && off.sub.isEmpty && off.rise.isEmpty && off.valueText == "--")
    var polar = faces()
    polar.night = true
    polar.moon?.up = false
    let p = state(fetched: h(10, 0), faces: polar)
    #expect(KnobSunFace(state: p, now: at(h(10, 0))).polar == .night)
    let pOff = KnobSunFace(state: p.with { $0.enabled = false }, now: at(h(10, 0)))
    #expect(pOff.polar == .none && pOff.sub.isEmpty && !pOff.hero)
    var f = faces()
    f.todayPopPct = 40
    f.windKmh = 20
    f.aqi = 42
    let w = state(fetched: observed, faces: f, enabled: false)
    #expect(rain(w) == KnobRainFace(dry: true, intensity: 1) && !KnobWindFace(state: w, now: at(knobNow)).have)
    #expect(!KnobAirFace(state: w, now: at(knobNow)).have && !cast(w, now: knobNow).have && cast(w, now: knobNow).sub.isEmpty)
}

extension WeatherState {
    func with(_ f: (inout WeatherState) -> Void) -> WeatherState {
        var s = self
        f(&s)
        return s
    }
}

@Test func castRestMarkerAndLabelComputeInFloatLikeCinder() {
    func ring(_ d: Double) -> [Double] {
        let p = KnobCastFace.ringPoint(d)
        return [p.x.rounded(), p.y.rounded()]
    }
    func place(_ d: Double) -> [Double]? {
        KnobCastFace.labelPlace(d, w: 30, h: 27, avoid: .zero, gap: 2).map { [$0.x, $0.y] }
    }
    #expect(ring(258.7) == [15, 276], "C rounds 276.499939 down, Double says 276.500044")
    #expect(ring(75) == [447, 176] && ring(0) == [233, 11])
    #expect(place(60) == [374, 121], "C: 121.00 after float, Double lands on 120")
    #expect(place(258.7) == [39, 258])
    #expect(place(0) == [218, 32] && place(90) == [401, 220] && place(270) == [35, 220])
}
