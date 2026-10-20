import Foundation
import SwiftUI
import Testing
@testable import EmberKit

private typealias RF = KnobRainFace.Frame

private func rain(_ kind: KnobRainFace.Kind = .rain, _ intensity: Int = 3) -> KnobRainFace {
    KnobRainFace(dry: false, kind: kind, intensity: intensity, pop: 80)
}

@Test func arrivalEaseAndCountUpMatchCinder() {
    #expect(KnobArrive.ease(0) == 0 && KnobArrive.ease(-1) == 0 && KnobArrive.ease(.nan) == 0 && KnobArrive.ease(1.32) == 1 && KnobArrive.ease(9) == 1)
    #expect(abs(KnobArrive.ease(0.5) - 0.760271) < 1e-6 && abs(KnobArrive.ease(1) - 0.985753) < 1e-6)
    #expect(KnobArrive.spriteOpacity(nil) == 255, "the rest pose: fully in")
    #expect([0, 1.0 / 15, 0.2, 0.5, 1, 1.3].map { KnobArrive.spriteOpacity($0) } == [0, 37, 99, 186, 251, 255])
    #expect(KnobArrive.spriteOpacity(0.25) == 99, "held on the 15 fps step of 0.2 s")
    #expect(KnobArrive.quantised(0.249, fps: 30) == 7.0 / 30 && KnobArrive.quantised(1, fps: 15) == 1)
    #expect(KnobArrive.countN(9) == 9 && KnobArrive.countN(-3) == 3 && KnobArrive.countN(45) == 23)
    #expect(KnobArrive.countValue(45, 10) == 20 && KnobArrive.countValue(-3, 2) == -2 && KnobArrive.countValue(9, 0) == 0
        && KnobArrive.countValue(9, 99) == 9)
    #expect(KnobArrive.countValue(9, KnobArrive.countStep(9, 0.5)) == 6 && KnobArrive.countStep(9, 1.32) == 9 && KnobArrive.countStep(9, 0) == 0)
}

@Test func rainFrameMatchesCinder() {
    #expect(KnobRainFace.splashHit == [4, 31, 17])
    func off(_ snow: Bool, _ k: Int) -> [Int] { KnobRainFace.sheets.map { KnobRainFace.sheetOffset($0, snow: snow, k: k) } }
    #expect([0, 5, 6, 8, 19, 37, 102, 1000].map { off(false, $0) }
        == [[45, 94, 0], [83, 125, 44], [90, 131, 53], [105, 144, 71], [188, 213, 168], [23, 25, 26], [210, 131, 0], [45, 44, 124]])
    #expect(off(true, 37) == [161, 163, 163], "snow falls at half speed")
    let r = rain()
    #expect(r.frame(k: 0) == RF(drips: [nil, nil], splashes: [nil, nil, nil]))
    #expect(r.frame(k: -4) == r.frame(k: 0), "k < 0 is frame 0")
    #expect(r.frame(k: 4) == RF(drips: [nil, nil], splashes: [.init(dy: 1, opa: 179), nil, nil]))
    #expect(r.frame(k: 5) == RF(drips: [.init(pose: 0, dy: 0, opa: 153), nil], splashes: [.init(dy: 0, opa: 255), nil, nil]))
    #expect(r.frame(k: 6) == RF(drips: [.init(pose: 1, dy: 0, opa: 230), nil], splashes: [.init(dy: -2, opa: 153), nil, nil]))
    #expect(r.frame(k: 8) == RF(drips: [.init(pose: 2, dy: 6, opa: 255), nil], splashes: [nil, nil, nil]))
    #expect(r.frame(k: 11).drips == [.init(pose: 2, dy: 34, opa: 89), nil] && r.frame(k: 12).drips == [nil, nil])
    #expect(r.frame(k: 13).drips == [nil, .init(pose: 0, dy: 0, opa: 153)])
    #expect(r.frame(k: 19) == RF(drips: [nil, .init(pose: 2, dy: 34, opa: 89)], splashes: [nil, nil, .init(dy: -2, opa: 153)]))
    #expect(r.frame(k: 31).splashes == [nil, .init(dy: 1, opa: 179), nil] && r.frame(k: 39).splashes == [.init(dy: 0, opa: 255), nil, nil])
    #expect(!r.frame(k: 102).bolt, "a bolt only in a storm")
    let light = rain(.rain, 1)
    #expect(light.frame(k: 5).splashes[0] != nil && light.frame(k: 18).splashes[2] != nil && light.frame(k: 32).splashes[1] == nil,
            "a splash shows only while its drop falls: drop 1 is a moderate one")
    let snow = rain(.snow, 2)
    #expect(snow.frame(k: 37) == RF(drips: [nil, nil], splashes: [nil, nil, nil]))
    #expect(snow.frame(k: 5).drips == [nil, nil] && snow.frame(k: 5).splashes == [nil, nil, nil], "no drips or splashes in snow")
    let storm = rain(.storm, 2)
    #expect(storm.frame(k: 102) == RF(drips: [.init(pose: 1, dy: 0, opa: 230), nil], splashes: [nil, nil, nil], bolt: true))
    #expect(storm.frame(k: 103).bolt && !storm.frame(k: 104).bolt && storm.frame(k: 12).bolt && storm.frame(k: 25).bolt && !storm.frame(k: 26).bolt)
    #expect(r.frame(k: 8160) == r.frame(k: 0), "the loop's whole period")
}

@Test func castFrameMatchesCinder() {
    #expect([0, 0.5, 0.96, 1, 1.05, 1.1, 1.14, 1.2, 2].map(KnobCastFace.gateOpacity) == [0, 0, 0, 57, 128, 198, 255, 255, 255])
    #expect([0, 0.96, 1, 1.05, 1.14, 1.2, 1.32].map(KnobCastFace.labelOpacity) == [0, 0, 28, 64, 127, 170, 255])
    #expect(stride(from: 0, to: 26, by: 3).map { KnobCastFace.gloom(variant: 3, k: $0) } == [255, 255, 236, 236, 217, 217, 198, 198, 179])
    #expect((0..<48).allSatisfy { KnobCastFace.gloom(variant: 1, k: $0) == 255 && KnobCastFace.gloom(variant: 2, k: $0) == 255 })
    var c = KnobCastFace()
    c.have = true; c.variant = 3; c.wet = true
    let rest = c.frame(t: nil)
    #expect(rest == .init(k: 0, dx: [-4, 0], gloom: 255, gate: 255, label: 255, sprite: 255), "the rest pose")
    #expect(c.frame(t: 0) == .init(k: 0, dx: [-4, 0], gloom: 255, gate: 0, label: 0, sprite: 0))
    let one = c.frame(t: 1)
    #expect(one.gloom == 217 && one.gate == 57 && one.label == 28 && one.sprite == 251)
    #expect(one.dx == [KnobCastFace.breathe(variant: 3, side: 0, k: 15), KnobCastFace.breathe(variant: 3, side: 1, k: 15)])
    #expect(c.frame(t: 1.05) == c.frame(t: 1), "held on the 15 fps step")
    #expect(one.k == 15 && c.frame(t: 1.25).k == 18)
    var light = c
    light.variant = 1
    #expect(light.frame(t: 3).dx.count == 1 && light.frame(t: 3).gloom == 255 && light.frame(t: 3).gate == 255)
}

@Test func sunFrameMatchesCinder() {
    typealias F = KnobSunFace.Frame
    #expect(KnobSunFace.rest == F(dy: 0, rays: 255))
    #expect(KnobSunFace.frame(t: 0) == F(dy: 64, rays: 0) && KnobSunFace.frame(t: -1) == F(dy: 64, rays: 0) && KnobSunFace.frame(t: .nan) == F(dy: 64, rays: 0))
    #expect(KnobSunFace.frame(t: 0.1) == F(dy: 51, rays: 0) && KnobSunFace.frame(t: 0.5) == F(dy: 15, rays: 0))
    #expect(KnobSunFace.frame(t: 0.73).rays == 0 && KnobSunFace.frame(t: 11.0 / 15) == F(dy: 6, rays: 5))
    #expect(KnobSunFace.frame(t: 0.8).rays == 28 && KnobSunFace.frame(t: 0.86).rays == 28 && KnobSunFace.frame(t: 1.08).rays == 123)
    #expect(KnobSunFace.frame(t: 1) == F(dy: 1, rays: 99) && KnobSunFace.frame(t: 1.3) == F(dy: 0, rays: 194))
    #expect(KnobSunFace.frame(t: 1.44) == F(dy: 0, rays: 241) && KnobSunFace.frame(t: 22.0 / 15) == KnobSunFace.rest)
}

@Test func daysFrameMatchesCinder() {
    let arrive = [(0, 0.0), (0, -1), (0, 0.42), (0, 0.21), (1, 0.08), (1, 0.499), (2, 0.16), (2, 0.58)].map { KnobDaysFace.arrive($0.0, $0.1) }
    #expect(arrive.map(\.opa) == [0, 0, 255, 223, 0, 255, 0, 255] && arrive.map(\.dy) == [12, 12, 0, 2, 12, 0, 12, 0])
    let d = KnobDaysFace(rows: [.init(wday: 1, icon: .rain, pop: 70, lo: 9, hi: 14), .init(wday: 2, icon: .sun, lo: -3, hi: 1),
                                .init(wday: 3, icon: .snow, lo: 7, hi: 35)])
    typealias F = KnobDaysFace.Frame
    #expect(d.frame(t: 0) == F(opa: [0, 0, 0], dy: [12, 12, 12], lo: [0, 0, 0], hi: [0, 0, 0]))
    #expect(d.frame(t: 0.1) == F(opa: [142, 35, 0], dy: [5, 10, 12], lo: [1, 0, 1], hi: [2, 0, 6]))
    #expect(d.frame(t: 0.5) == F(opa: [255, 255, 253], dy: [0, 0, 0], lo: [6, -2, 5], hi: [10, 0, 26]))
    #expect(d.frame(t: 1.3) == F(opa: [255, 255, 255], dy: [0, 0, 0], lo: [8, -2, 6], hi: [13, 0, 33]))
    #expect(d.frame(t: 4.0 / 3) == d.rest && d.rest == F(opa: [255, 255, 255], dy: [0, 0, 0], lo: [9, -3, 7], hi: [14, 1, 35]))
}

@Test func airPetalMatchesCinder() {
    #expect(KnobAirFace.petal(k: -1).opa == 0)
    let fixtures = [(0, 248, 159, 2, 255), (10, 244, 174, 3, 255), (20, 242, 186, 4, 255), (75, 254, 134, 0, 0), (80, 254, 135, 0, 168),
                    (90, 252, 140, 1, 255), (100, 250, 152, 2, 255), (130, 241, 189, 5, 255), (141, 240, 192, 5, 255), (176, 240, 192, 5, 77),
                    (178, 240, 192, 5, 19)]
    for (k, x, y, rot, opa) in fixtures {
        #expect(KnobAirFace.petal(k: k) == .init(x: x, y: y, rot: rot, opa: opa), "frame \(k)")
    }
    #expect(KnobAirFace.petal(k: 0) == KnobAirFace.petal(k: 105), "a 7 s loop at 15 fps")
}

@Test func motionFollowsWhatTheFaceAnimates() {
    var wilted = KnobAirFace()
    wilted.have = true; wilted.wilted = true
    var wind = KnobWindFace()
    wind.have = true
    var cast = KnobCastFace()
    cast.have = true; cast.variant = 2
    var sun = KnobSunFace(state: nil, now: .now)
    #expect(KnobWeatherMotion(.sun(sun)) == .still, "no sun times: no sun to rise")
    sun.have = true
    let days = KnobDaysFace(rows: [.init(wday: 1, icon: .rain, lo: 9, hi: 14)])
    #expect(KnobWeatherMotion(.rain(rain())) == .loop && KnobWeatherMotion(.rain(KnobRainFace(dry: true, intensity: 1))) == .still)
    #expect(KnobWeatherMotion(.cast(cast)) == .loop && KnobWeatherMotion(.cast(KnobCastFace())) == .still)
    #expect(KnobWeatherMotion(.wind(wind)) == .loop)
    #expect(KnobWeatherMotion(.air(wilted)) == .loop && KnobWeatherMotion(.air(KnobAirFace())) == .still)
    #expect(KnobWeatherMotion(.sun(sun)) == .arrival(until: 22.0 / 15) && KnobSunFace.restS == 22.0 / 15 && KnobArrive.restS == 4.0 / 3)
    sun.sub = "golden hour 18:30"
    #expect(KnobWeatherMotion(.sun(sun)) == .arrival(until: 1.8), "the sub-line fades in until 1.8 s")
    #expect(KnobWeatherMotion(.days(days)) == .arrival(until: 4.0 / 3) && KnobWeatherMotion(.days(KnobDaysFace())) == .still)
    #expect(KnobWeatherMotion(.weather(KnobTempFace(value: 12))) == .arrival(until: 4.0 / 3), "the value counts up, the mercury rises")
    #expect(KnobWeatherMotion(.weather(KnobTempFace(value: 12, sub: "Clear"))) == .arrival(until: KnobArrive.subRestS) && KnobArrive.subRestS == 1.8)
    #expect(KnobWeatherMotion(.weather(KnobTempFace(value: nil))) == .still, "no data: nothing moves")
    #expect(KnobWeatherMotion(.wind(KnobWindFace())) == .arrival(until: 4.0 / 3), "no wind data: the sock still fades in")
    var calm = KnobAirFace()
    calm.have = true; calm.aqi = 20
    #expect(KnobWeatherMotion(.air(calm)) == .arrival(until: 1.8), "the AQI counts up, its band word fades in")
    #expect(KnobWeatherMotion(.rain(KnobRainFace(dry: true, intensity: 1, pop: 40, sub: "Dry now"))) == .arrival(until: 1.8))
    var unsure = KnobCastFace()
    unsure.sub = KnobCastFace.noForecast
    #expect(KnobWeatherMotion(.cast(unsure)) == .arrival(until: 1.8))
    for f in [KnobFace.rain(rain()), .cast(cast), .wind(wind), .air(wilted), .sun(sun), .days(days), .weather(KnobTempFace(value: 12)),
              .wind(KnobWindFace()), .air(calm)] {
        #expect(KnobWeatherMotion(f, reduceMotion: true) == .still, "Reduce Motion: \(f)")
    }
}

@Test func anArrivalEndsOnItsRestFrame() {
    let days = KnobDaysFace(rows: [.init(wday: 1, icon: .rain, lo: 9, hi: 14), .init(wday: 2, icon: .sun, lo: -3, hi: 1),
                                   .init(wday: 3, icon: .snow, lo: 7, hi: 35)])
    let sunEnd = 22.0 / 15, daysEnd = 4.0 / 3
    #expect(KnobSunFace.frame(t: KnobArrive.quantised(sunEnd + 0.5 / 15, fps: 30)) == KnobSunFace.rest)
    #expect(KnobSunFace.frame(t: KnobArrive.quantised(sunEnd - 0.5 / 15, fps: 30)) != KnobSunFace.rest)
    #expect(days.frame(t: KnobArrive.quantised(daysEnd + 0.5 / 15, fps: 30)) == days.rest)
    #expect(days.frame(t: KnobArrive.quantised(daysEnd - 0.5 / 15, fps: 30)) != days.rest)
}

private let since = Date(timeIntervalSinceReferenceDate: 800_000_000)

private func entries(_ m: KnobWeatherMotion, from t0: Double, max: Int = 400) -> [Double] {
    let it = KnobWeatherSchedule(since: since, motion: m).entries(from: since.addingTimeInterval(t0), mode: .normal)
    return Array(AnySequence { it }.prefix(max)).map { $0.timeIntervalSince(since) }
}

@Test func scheduleTicksAt15FpsWhileALoopShows() {
    let e = entries(.loop, from: 0.01, max: 6)
    #expect(e.count == 6 && abs(e[0] - 0.01) < 1e-6, "starts at once")
    for (a, b) in zip(e.dropFirst(), e.dropFirst(2)) { #expect(abs(b - a - 1.0 / 15) < 1e-6) }
    #expect(e.dropFirst().allSatisfy { abs(($0 * 15 - 0.5).rounded() - ($0 * 15 - 0.5)) < 1e-6 }, "mid-frame, aligned to the show")
    #expect(abs(e[1] - 0.5 / 15) < 1e-6)
    #expect(entries(.loop, from: 600).count == 400, "a loop never ends")
}

@Test func scheduleStopsOnceAnArrivalIsOver() {
    let e = entries(.arrival(until: 22.0 / 15), from: 0)
    #expect(e.count == 24 && e.last! >= 22.0 / 15 && e.dropLast().last! < 22.0 / 15, "\(e.count) entries, the last \(e.last!)")
    #expect(entries(.arrival(until: 22.0 / 15), from: 5) == [5], "shown long ago: the rest frame, no more entries")
    #expect(entries(.still, from: 0).isEmpty && entries(.still, from: 3).isEmpty, "a still face never ticks")
}

private let wx = KnobTheme.standard.weather
private let observed: Int64 = 1_782_043_200
private func at(_ t: Int64) -> Date { Date(timeIntervalSince1970: TimeInterval(t)) }

private func windy(_ kmh: Int, aqi: Int? = nil) -> WeatherState {
    WeatherState(generatedAt: at(observed), enabled: true, provider: "open-meteo", units: "metric", locationName: nil,
                 current: .init(fetchedAt: at(observed), stale: false, condition: "clouds", conditionCode: "3", severe: false, tempC: 12,
                                todayMinC: nil, todayMaxC: nil, cloudPct: nil, uv: nil, hourly: []),
                 air: nil, sun: nil,
                 faces: .init(utcOffsetS: 3600, night: false, windKmh: kmh, windDirDeg: nil, rainIntensity: nil, todayPopPct: nil, rain: nil,
                              days: [], twilight: nil, moon: nil, aqi: aqi))
}

@Test func showTimeKeepsCountingWhileTheSameFaceShows() throws {
    var memo = KnobWeatherMemo()
    #expect(memo.shownAt == nil)
    let a0 = Date(timeIntervalSinceReferenceDate: 1000)
    let first = memo.draw(nil, state: windy(45), now: at(observed + 300), at: a0)
    memo = try #require(first.commit)
    #expect(first.face.weatherFace == .wind && memo.shownAt == a0, "the first draw after open starts the clock")
    let refresh = memo.draw(nil, state: windy(41), now: at(observed + 360), at: a0.addingTimeInterval(60))
    if let m = refresh.commit { memo = m }
    #expect(refresh.face.weatherFace == .wind && memo.shownAt == a0, "a data refresh keeps t")
    let a2 = a0.addingTimeInterval(90)
    let change = memo.draw(.air, state: windy(41, aqi: 70), now: at(observed + 360), at: a2)
    memo = try #require(change.commit)
    #expect(memo.shownAt == a2, "a face change starts t at 0")
    #expect(KnobWeatherMemo().draw(.wind, state: windy(45), now: at(observed)).commit?.shownAt == at(observed), "at defaults to now")
}

@Test func anAutoSwitchKeepsTheShowTimeLikeTheKnob() throws {
    var memo = KnobWeatherMemo()
    let a0 = Date(timeIntervalSinceReferenceDate: 1000)
    memo = try #require(memo.draw(nil, state: windy(45), now: at(observed + 300), at: a0).commit)
    let auto = memo.draw(nil, state: windy(10, aqi: 70), now: at(observed + 360), at: a0.addingTimeInterval(60))
    memo = try #require(auto.commit)
    #expect(auto.face.weatherFace == .air && memo.shownAt == a0, "AUTO steps to Air without a cut: s_t keeps counting")
    let a2 = a0.addingTimeInterval(90)
    memo = try #require(memo.draw(.rain, state: windy(10, aqi: 70), now: at(observed + 360), at: a2).commit)
    #expect(memo.shownAt == a2, "a reported face is a cut")
    let a3 = a0.addingTimeInterval(120)
    let back = memo.draw(nil, state: windy(45), now: at(observed + 420), at: a3)
    if let m = back.commit { memo = m }
    #expect(back.face.weatherFace == .wind && memo.shownAt == a2, "back to AUTO is no cut either")
}

@Test func aClockSetBackRestampsTheShowTime() throws {
    var memo = KnobWeatherMemo()
    let a0 = Date(timeIntervalSinceReferenceDate: 1000)
    memo = try #require(memo.draw(.wind, state: windy(45), now: at(observed + 300), at: a0).commit)
    let back = a0.addingTimeInterval(-600)
    memo = try #require(memo.draw(.wind, state: windy(45), now: at(observed + 300), at: back).commit)
    #expect(memo.shownAt == back, "t never goes negative")
}

@Test func theAnimationTicksOnlyInAVisibleWindow() {
    #expect(KnobWeatherLive.ticks(.loop, animated: true, onScreen: true, windowVisible: true))
    #expect(!KnobWeatherLive.ticks(.loop, animated: true, onScreen: true, windowVisible: false), "covered, minimised or inactive")
    #expect(!KnobWeatherLive.ticks(.loop, animated: true, onScreen: false, windowVisible: true))
    #expect(!KnobWeatherLive.ticks(.loop, animated: false, onScreen: true, windowVisible: true))
    #expect(!KnobWeatherLive.ticks(.still, animated: true, onScreen: true, windowVisible: true))
}

@Test func faceViewWithoutAFrameIsTheRestPose() {
    let f = KnobFace.rain(rain())
    #expect(KnobFaceView(f, t: 1) != KnobFaceView(f, t: 2), "a new frame redraws")
}

@Test func subLineFadesInThreeStepsAfterTheArrival() {
    #expect(KnobArrive.subOpacity(nil) == 255, "the rest pose")
    #expect([0, 1.32, 1.33, 1.47, 1.49, 1.63, 1.65, 1.8, 5].map { KnobArrive.subOpacity($0) } == [0, 0, 85, 85, 170, 170, 255, 255, 255])
    #expect(KnobArrive.subOpacity(-1) == 0 && KnobArrive.subOpacity(.nan) == 0)
}

private let arrivalTimes = [0, 0.1, 0.5, 1.0, 1.3, 1.34]

@Test func valuesCountUpAndMarkersEaseOnTheArrivalClock() {
    #expect(arrivalTimes.map { KnobArrive.count(21, $0) } == [0, 4, 15, 20, 20, 21])
    #expect(arrivalTimes.map { KnobArrive.count(-6, $0) } == [0, -1, -4, -5, -5, -6])
    #expect(arrivalTimes.map { KnobArrive.count(45, $0) } == [0, 8, 33, 43, 43, 45])
    #expect(KnobArrive.count(21, nil) == 21 && KnobArrive.eased(nil) == 1)
    let a = [0, 0.210489885, 0.760271447, 0.985752845, 0.999996522, 1]
    #expect(zip(arrivalTimes.map { KnobArrive.eased($0) }, a).allSatisfy { abs($0 - $1) < 1e-8 })
}

@Test func everyFaceCountsItsValueUpLikeCinder() {
    let temp = KnobTempFace(value: 21)
    #expect(arrivalTimes.map { temp.valueText(t: $0) } == ["0°", "4°", "15°", "20°", "20°", "21°"] && temp.valueText() == "21°")
    #expect(temp.valueColor(wx, t: 0.5) == KnobTempFace.color(15, wx.scale), "the colour follows the counted value")
    #expect(KnobTempFace(value: -6).valueText(t: 0.5) == "\u{2212}4°")
    #expect(arrivalTimes.map { rain().valueText(t: $0) } == ["0", "14", "59", "77", "77", "80"] && rain().valueText == "80")
    var wind = KnobWindFace()
    wind.have = true; wind.kmh = 18
    #expect(arrivalTimes.map { wind.valueText(t: $0) } == ["0", "3", "13", "17", "17", "18"])
    #expect(wind.valueColor(wx, t: 0) == KnobWindFace.color(0, wx.wind) && wind.valueColor(wx, t: nil) == KnobWindFace.color(18, wx.wind))
    #expect(KnobWindFace().valueText(t: 0.5) == "--")
    var air = KnobAirFace()
    air.have = true; air.aqi = 42
    #expect(arrivalTimes.map { air.valueText(t: $0) } == ["0", "7", "31", "40", "40", "42"])
    #expect(air.valueColor(wx, t: 0.5) == KnobAirFace.color(31, wx.air))
    var dry = KnobCastFace()
    dry.have = true; dry.dry = true; dry.hours = true; dry.count = 24
    #expect(arrivalTimes.map { dry.valueText(t: $0) } == ["0", "2½", "9", "11½", "11½", "12+"])
    var soon = KnobCastFace()
    soon.have = true; soon.hasCount = true; soon.hours = true; soon.count = 5
    #expect(arrivalTimes.map { soon.valueText(t: $0) } == ["0", "½", "1½", "2", "2", "2½"])
    soon.hours = false; soon.count = 9
    #expect(arrivalTimes.map { soon.valueText(t: $0) } == ["0", "5", "30", "40", "40", "45"])
    #expect(KnobCastFace().valueText(t: 0.5) == "--")
    var sun = KnobSunFace(state: nil, now: .now)
    sun.have = true; sun.hasNow = true; sun.hours = true; sun.count = 18; sun.unit = "h left"
    #expect(arrivalTimes.map { sun.valueText(t: $0) } == ["0", "1½", "6½", "8½", "8½", "9"])
    sun.hours = false; sun.count = 45; sun.unit = "min"
    #expect(arrivalTimes.map { sun.valueText(t: $0) } == ["0", "8", "33", "43", "43", "45"])
}

@Test func theMercuryRisesOnTheArrivalEase() {
    let th = wx.thermo, temp = KnobTempFace(value: 21)
    #expect(arrivalTimes.map { temp.mercuryTop(th, t: $0) } == [184, 173.5, 145.5, 134.5, 133.5, KnobTempFace.mercuryTop(21, th)],
            "half-pixel steps on the way, the rest pose's top once the ease is done")
    #expect(temp.mercuryTop(th, t: nil) == KnobTempFace.mercuryTop(21, th) && KnobTempFace(value: nil).mercuryTop(th, t: 0.5) == th.mercuryBottomPx)
}

@Test func castDropSlidesFromNowToTheRainAndFadesOut() {
    var c = KnobCastFace()
    c.have = true; c.hasCount = true; c.nowDeg = 75; c.toDeg = 153
    let ts = [0, 0.1, 0.3, 0.5, 0.9, 1.0, 1.1, 1.19, 1.2, 2]
    let drops = ts.map { c.drop(t: $0) }
    #expect(drops.map { $0?.deg } == [nil, 76.75, 99, 134.25, 152.75, 153, 153, 153, nil, nil])
    #expect(drops.map { $0?.opa } == [nil, 142, 255, 255, 255, 212, 106, 35, nil, nil])
    #expect(c.drop(t: nil) == nil, "no drop at rest")
    #expect(abs(KnobCastFace.ease(0.5) - 0.729354536) < 1e-8 && KnobCastFace.ease(0) == 0 && KnobCastFace.ease(2) == 1)
    #expect([0, 0.09, 0.5, 1.08, 1.2, 9, .nan].map(KnobCastFace.dropOpacity) == [0, 128, 255, 127, 0, 0, 0])
    c.hasCount = false
    #expect(c.drop(t: 0.5) == nil, "no count: no drop")
}
