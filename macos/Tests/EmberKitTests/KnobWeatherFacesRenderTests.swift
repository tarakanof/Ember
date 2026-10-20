import Foundation
import ImageIO
import SwiftUI
import Testing
@testable import EmberKit

private let th = KnobTheme.standard
private let w = th.weather

@MainActor
private func render(_ f: KnobFace, t: Double? = nil) throws -> CGImage {
    let r = ImageRenderer(content: KnobFaceView(f, t: t).frame(width: 466, height: 466).environment(\.colorScheme, .dark))
    r.scale = 1
    let img = try #require(r.cgImage)
    if let dir = ProcessInfo.processInfo.environment["KNOB_SNAPSHOT_DIR"],
       let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: dir).appendingPathComponent("\(f.name).png") as CFURL,
                                                  "public.png" as CFString, 1, nil) {
        CGImageDestinationAddImage(dest, img, nil)
        CGImageDestinationFinalize(dest)
    }
    return img
}

private extension KnobFace {
    var name: String {
        switch self {
        case .rain(let f): f.dry ? "rain-dry" : "rain-\(f.kind)"
        case .cast(let f): "cast-\(f.variant)\(f.dry ? "-dry" : "")"
        case .wind(let f): "wind-\(f.level)"
        case .days(let f): "days-\(f.rows.count)"
        case .sun(let f): "sun-\(f.have)"
        case .air(let f): "air-\(f.wilted ? "wilted" : "healthy")"
        default: "other"
        }
    }
}

private func pixel(_ img: CGImage, _ p: CGPoint) -> RGB { pixel(img, Int(p.x.rounded()), Int(p.y.rounded())) }

private func pixel(_ img: CGImage, _ x: Int, _ y: Int) -> RGB {
    var buf = [UInt8](repeating: 0, count: 4)
    let ctx = CGContext(data: &buf, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: -x, y: y - img.height + 1, width: img.width, height: img.height))
    return RGB(r: buf[0], g: buf[1], b: buf[2])
}

private func near(_ a: RGB, _ b: RGB, _ tol: Int = 30) -> Bool {
    abs(Int(a.r) - Int(b.r)) <= tol && abs(Int(a.g) - Int(b.g)) <= tol && abs(Int(a.b) - Int(b.b)) <= tol
}

private let black = RGB(r: 0, g: 0, b: 0)

private func scaled(_ c: RGB, _ a: Double) -> RGB {
    RGB(r: UInt8((Double(c.r) * a).rounded()), g: UInt8((Double(c.g) * a).rounded()), b: UInt8((Double(c.b) * a).rounded()))
}

private func has(_ img: CGImage, _ c: RGB, rows: Range<Int>, cols: Range<Int>, tol: Int = 30) -> Bool {
    rows.contains { y in cols.contains { x in near(pixel(img, x, y), c, tol) } }
}

private func litDot(_ img: CGImage) -> Int? {
    (0..<w.dots.faces).first { near(pixel(img, KnobTempFace.dotPoint($0, w.dots, center: 233)), w.dots.onColor, 40) }
}

private func ring(_ f: Double) -> CGPoint {
    let a = (w.ring.fromDeg + w.ring.sweepDeg * f) * .pi / 180
    return CGPoint(x: 233 + w.ring.radiusPx * sin(a), y: 233 - w.ring.radiusPx * cos(a))
}

private func markerRing(_ img: CGImage, _ p: CGPoint) -> Bool {
    let at = CGPoint(x: p.x.rounded(), y: p.y.rounded())
    return near(pixel(img, at), black, 10) && near(pixel(img, Int(at.x + w.marker.ringRadiusPx), Int(at.y)), w.marker.color, 40)
}

@MainActor @Test func rainFaceRendersToTheFirmwareLayout() throws {
    let r = KnobRainFace(dry: false, intensity: 3, pop: 80, untilMin: 885, sub: "until 14:45")
    let img = try render(.rain(r))
    #expect(litDot(img) == 1, "the second face dot")
    #expect(near(pixel(img, ring(0.02)), w.rain.ringFrom, 30) && near(pixel(img, ring(0.98)), w.rain.colors[2], 30))
    #expect(markerRing(img, KnobFaceRender.gaugeMarkerPoint(0.8, t: nil, w)))
    #expect(near(pixel(img, 233, 200), w.rain.umbrellaColor, 40), "the umbrella handle under the canopy")
    #expect(near(pixel(img, 233, 160), black, 10), "a black canopy")
    #expect(has(img, w.rain.colors[2], rows: 210..<300, cols: 150..<320), "the chance in the heavy rain colour")
    #expect(has(img, w.rain.colors[2], rows: 160..<330, cols: 58..<118), "drops in the left sheet")
    #expect(has(img, w.sub.color, rows: 302..<335, cols: 150..<320), "the sub-line")
    #expect(has(img, w.rain.endColor, rows: 362..<402, cols: 96..<160), "the 0% end label")

    let dry = try render(.rain(KnobRainFace(dry: true, intensity: 1, pop: 40, sub: "Dry now")))
    #expect(!has(dry, w.rain.colors[0], rows: 160..<330, cols: 58..<100), "no drops while dry")
    let none = try render(.rain(KnobRainFace(dry: true, intensity: 1)))
    #expect(near(pixel(none, ring(0.5)), w.ring.trackColor, 12) && has(none, w.stillColor, rows: 210..<300, cols: 150..<320))
    #expect(!has(none, w.rain.endColor, rows: 362..<402, cols: 96..<160), "no end labels without a chance")
}

@MainActor @Test func castFaceRendersTheDialLikeTheKnob() throws {
    var c = KnobCastFace()
    c.have = true; c.hasCount = true; c.hours = true; c.count = 2; c.variant = 2; c.nowDeg = 75; c.labelDeg = 110
    c.label = "15:40"; c.sub = "#5C9CE0 rain# until 15:40"; c.wet = true
    c.arcs = [.init(a: 150, n: 70, intensity: 2), .init(a: 480, n: 60, intensity: 3)]
    let img = try render(.cast(c))
    #expect(litDot(img) == 2)
    #expect(near(pixel(img, KnobCastFace.ringPoint(100)), w.rain.colors[1], 30), "lit cell")
    #expect(near(pixel(img, KnobCastFace.ringPoint(245)), w.rain.colors[2], 30), "the later heavy spell")
    #expect(near(pixel(img, KnobCastFace.ringPoint(200)), w.cast.cellColor, 12), "an unlit cell")
    #expect(near(pixel(img, KnobCastFace.ringPoint(90)), black, 12), "the gap between cells")
    #expect(markerRing(img, KnobCastFace.ringPoint(75)))
    #expect(has(img, w.cast.color, rows: 210..<300, cols: 150..<320), "the count in the forecast blue")
    #expect(has(img, w.cast.color, rows: 302..<335, cols: 120..<346) && has(img, w.sub.color, rows: 302..<335, cols: 120..<346),
            "a coloured word in the grey sub-line")
    #expect(has(img, RGB(hex: "#8E96A0")!, rows: 140..<180, cols: 60..<170), "the moderate clouds")
    #expect(has(img, w.cast.color, rows: 287..<314, cols: 360..<430), "the dial label at 15:40")
    var dry = KnobCastFace()
    dry.have = true; dry.dry = true; dry.variant = 1; dry.hours = true; dry.count = 24; dry.sub = "No rain for 12 h"
    let d = try render(.cast(dry))
    #expect(has(d, w.cast.dryColor, rows: 210..<300, cols: 150..<320) && near(pixel(d, KnobCastFace.ringPoint(100)), w.cast.cellColor, 12))
    #expect(has(d, RGB(hex: "#E6ECF2")!, rows: 130..<165, cols: 180..<290), "the light cloud")
}

@MainActor @Test func windFaceRendersTheSockAndBeaufortGauge() throws {
    var f = KnobWindFace()
    f.have = true; f.kmh = 18; f.dir = 225; f.level = 1; f.fraction = 0.36; f.sub = "#8ACF7A Gentle breeze# \u{2022} SW"
    let img = try render(.wind(f))
    let g = RGB(hex: "#8ACF7A")!
    #expect(litDot(img) == 3)
    #expect(near(pixel(img, ring(0.02)), RGB(hex: "#9AA4B0")!, 30))
    #expect(near(pixel(img, ring(0.98)), RGB(hex: "#F08A3A")!, 30), "49 km/h: a strong breeze")
    #expect(markerRing(img, KnobFaceRender.gaugeMarkerPoint(0.36, t: nil, w)))
    let q = KnobWindFace.sockQuads(level: 1, pose: 0)
    func mid(_ p: [CGPoint]) -> CGPoint { CGPoint(x: p.map(\.x).reduce(0, +) / 4, y: p.map(\.y).reduce(0, +) / 4) }
    #expect(near(pixel(img, mid(q[0])), g, 20) && near(pixel(img, mid(q[1])), KnobWindFace.dark(g, w.wind), 20), "striped sock")
    #expect(near(pixel(img, Int(KnobWindFace.mount(level: 1).x - 22), 150), w.wind.poleColor, 30), "the pole")
    #expect(has(img, g, rows: 210..<300, cols: 150..<320) && has(img, w.wind.unitColor, rows: 250..<300, cols: 233..<380))
    #expect(has(img, g, rows: 302..<335, cols: 100..<366), "the band word in its colour")
    let none = try render(.wind(KnobWindFace()))
    #expect(near(pixel(none, ring(0.5)), w.ring.trackColor, 12) && near(pixel(none, mid(KnobWindFace.sockQuads(level: 0)[0])), w.stillColor, 20))
}

@MainActor @Test func daysFaceRendersRowsLikeTheKnob() throws {
    let d = KnobDaysFace(rows: [.init(wday: 1, icon: .rain, pop: 70, lo: 9, hi: 14), .init(wday: 2, icon: .sun, lo: 7, hi: 19),
                                .init(wday: 3, icon: .snow, pop: 40, lo: -3, hi: 1)])
    let img = try render(.days(d))
    let c = d.cols(w.days)
    #expect(litDot(img) == 4)
    #expect(near(pixel(img, 233, Int(233 - w.ring.radiusPx)), w.days.trackColor, 12) && near(pixel(img, 233, Int(233 + w.ring.radiusPx)), w.days.trackColor, 12),
            "a plain full track")
    #expect(has(img, w.days.nameColor, rows: 130..<160, cols: c.dayX..<(c.dayX + c.dayW)), "the weekday")
    #expect(has(img, w.days.popColor, rows: 165..<195, cols: c.iconX..<(c.iconX + 40)), "the chance under the rain icon")
    #expect(has(img, RGB(hex: "#FFB547")!, rows: 210..<246, cols: c.iconX..<(c.iconX + 40)), "the sun icon")
    #expect(near(pixel(img, c.barX + 2, 228), w.days.trackColor, 16), "the bar track left of the span")
    #expect(has(img, KnobTempFace.color(19, w.scale), rows: 210..<250, cols: c.hiX..<(c.hiX + c.hiW)), "the high in its colour")
    #expect(!has(img, w.stillColor, rows: 210..<300, cols: 200..<266, tol: 4), "no value while rows show")
    let none = try render(.days(KnobDaysFace()))
    #expect(has(none, w.stillColor, rows: 210..<300, cols: 180..<290, tol: 4), "-- without rows")
}

@MainActor @Test func sunFaceRendersTheDayGauge() throws {
    let (s, at) = sunFixture()
    let face = KnobSunFace(state: s, now: at)
    let img = try render(.sun(face))
    #expect(litDot(img) == 5)
    #expect(near(pixel(img, ring(0.01)), w.sun.blueOut, 30) && near(pixel(img, ring(0.5)), w.sun.dayNoon, 30))
    #expect(markerRing(img, KnobFaceRender.gaugeMarkerPoint(face.fraction, t: nil, w)))
    #expect(near(pixel(img, 233, 160), w.sun.goldColor, 40) && near(pixel(img, 233, 190), w.sun.goldColor, 60), "the sun above the horizon")
    #expect(near(pixel(img, 233, Int(w.sun.horizonYPx)), RGB(hex: "#9AA4B0")!, 30) && near(pixel(img, 233, 201), black, 10),
            "nothing of the sun below the horizon")
    #expect(has(img, w.sun.valueColor, rows: 210..<300, cols: 150..<320) && has(img, w.sun.goldColor, rows: 302..<335, cols: 100..<366))
    #expect(has(img, w.sun.horizonColor, rows: 362..<402, cols: 96..<190), "sunrise end label")
}

@MainActor @Test func airFaceRendersTheFlowerAndAQIGauge() throws {
    var a = KnobAirFace()
    a.have = true; a.aqi = 42; a.band = 2; a.fraction = 0.42
    let img = try render(.air(a))
    #expect(litDot(img) == 6)
    #expect(near(pixel(img, ring(0.02)), RGB(hex: "#4FD6CC")!, 30) && near(pixel(img, ring(0.98)), RGB(hex: "#D03060")!, 30))
    #expect(markerRing(img, KnobFaceRender.gaugeMarkerPoint(0.42, t: nil, w)))
    #expect(near(pixel(img, 233, 112), RGB(hex: "#F5C84A")!, 30), "the healthy flower's centre")
    #expect(has(img, KnobAirFace.color(42, w.air), rows: 210..<300, cols: 150..<320) && has(img, RGB(hex: "#F0E641")!, rows: 302..<335, cols: 150..<320))
    a.aqi = 68; a.band = 3; a.wilted = true; a.fraction = 0.68
    let wilted = try render(.air(a))
    #expect(!near(pixel(wilted, 233, 112), RGB(hex: "#F5C84A")!, 30) && has(wilted, RGB(hex: "#FF5050")!, rows: 108..<146, cols: 296..<340),
            "the wilted flower and the hazard sign")
}

@MainActor @Test func rainRestFrameDrawsTheSheetsAtCindersFrameZero() throws {
    let img = try render(.rain(KnobRainFace(dry: false, intensity: 3, pop: 80, sub: "Heavy rain")))
    let blue = w.rain.colors[2]
    #expect(near(pixel(img, 68, 215), scaled(blue, 0.9), 30), "the left sheet's first drop, 45 px down at frame 0")
    #expect(near(pixel(img, 69, 170), black, 10), "nothing where the strip would sit unshifted")
    #expect(near(pixel(img, 59, 330), black, 16), "the sheet fades out at the bottom of its box")
    #expect(near(pixel(img, 92, 183), scaled(blue, 0.55), 24), "a tile-B drop wrapped 300 px up into the left sheet")
    let snow = try render(.rain(KnobRainFace(dry: false, kind: .snow, intensity: 1, pop: 50, sub: "Light snow")))
    #expect(near(pixel(snow, 68, 193), scaled(w.rain.snowColor, 0.9), 30), "snow falls at half speed: 23 px down")
}

@MainActor @Test func castRestFrameShiftsTheCloudsAndDrawsBothTiles() throws {
    var c = KnobCastFace()
    c.have = true; c.variant = 2; c.wet = true; c.sub = "Rain"
    let img = try render(.cast(c))
    #expect(near(pixel(img, 182, 164), black, 12), "the left cloud breathes 4 px left at frame 0")
    #expect(near(pixel(img, 176, 164), RGB(hex: "#A8B0BA")!, 30), "its outline")
    #expect(near(pixel(img, 64, 191), scaled(w.cast.color, 0.65), 24), "a streak of the second tile")
    let lit = { (p: RGB) in Int(p.r) + Int(p.g) + Int(p.b) }, full = lit(scaled(w.cast.color, 0.85))
    let ramp = (206...207).map { lit(pixel(img, 375, $0)) }
    #expect(ramp[0] > 0 && ramp.allSatisfy { $0 < full } && ramp[1] < ramp[0] * 9 / 10, "the right sheet fades out over its last rows \(ramp)")
    var snow = c
    snow.snow = true
    let s = try render(.cast(snow))
    #expect(!near(pixel(s, 64, 191), scaled(w.cast.color, 0.65), 24), "snow moves the strip")
    #expect(near(pixel(s, 158, 191), scaled(w.rain.snowColor, 0.85), 24), "a flake of the second tile at half speed, 8 px down")
    var heavy = c
    heavy.variant = 3
    let h = try render(.cast(heavy))
    #expect(near(pixel(h, 50, 160), black, 16), "the heavy cloud fades out at the rim")
}

@MainActor @Test func windRestFrameDrawsStreaksAndLeaves() throws {
    var f = KnobWindFace()
    f.have = true; f.kmh = 18; f.level = 1; f.fraction = 0.36; f.sub = "#8ACF7A Gentle breeze#"
    let img = try render(.wind(f))
    #expect(near(pixel(img, 260, 165), scaled(w.wind.streakColor, 112.0 / 255), 16), "the second streak row at frame 0")
    f.kmh = 40; f.level = 2; f.fraction = 0.8
    let strong = try render(.wind(f))
    #expect(near(pixel(strong, 252, 157), w.wind.leafColor, 30), "the second leaf at frame 0")
    #expect(near(pixel(strong, 248, 160), w.wind.leafColor, 30), "the leaf lies along 135 degrees")
    #expect(near(pixel(strong, 309, 87), KnobWindFace.color(40, w.wind), 30), "the sock's tip in pose A")
    var still = KnobWindFace()
    still.level = 1
    #expect(near(pixel(try render(.wind(still)), 260, 165), black, 10), "no streaks without data")
}

private func same(_ a: CGImage, _ b: CGImage) -> Bool {
    func bytes(_ i: CGImage) -> Data? { i.dataProvider?.data as Data? }
    guard a.width == b.width, a.height == b.height, a.bytesPerRow == b.bytesPerRow, let x = bytes(a), let y = bytes(b), x.count == y.count else {
        return false
    }
    let off = zip(x, y).filter { $0 != $1 }
    return off.count <= 16 && off.allSatisfy { abs(Int($0) - Int($1)) <= 1 }
}

private func frame(_ k: Int) -> Double { (Double(k) + 0.5) / 15 }

@MainActor @Test func animatedFacesSettleOnTheRestPose() throws {
    let (s, at) = sunFixture()
    let sun = KnobFace.sun(KnobSunFace(state: s, now: at))
    #expect(same(try render(sun, t: 3), try render(sun)), "the sun has risen and its rays are in")
    let days = KnobFace.days(KnobDaysFace(rows: [.init(wday: 1, icon: .rain, pop: 70, lo: 9, hi: 14), .init(wday: 2, icon: .sun, lo: 7, hi: 19)]))
    #expect(same(try render(days, t: 3), try render(days)), "every row in, the counts done")
    let r = KnobFace.rain(KnobRainFace(dry: false, intensity: 3, pop: 80, sub: "Heavy rain"))
    #expect(same(try render(r, t: frame(8160)), try render(r)), "a whole rain period later: frame 0 again")
    var healthy = KnobAirFace()
    healthy.have = true; healthy.aqi = 42; healthy.band = 2; healthy.fraction = 0.42
    #expect(same(try render(.air(healthy), t: 5), try render(.air(healthy))), "the healthy flower is still")
}

@MainActor @Test func animatedFramesDrawCindersMotion() throws {
    let blue = w.rain.colors[2]
    let r = KnobFace.rain(KnobRainFace(dry: false, intensity: 3, pop: 80, sub: "Heavy rain"))
    let drip = try render(r, t: frame(8168))
    #expect(near(pixel(drip, 107, 210), blue, 40), "the left drip at its fullest, 6 px down")
    #expect(near(pixel(try render(r), 107, 210), black, 10), "no drip at rest")
    let storm = KnobFace.rain(KnobRainFace(dry: false, kind: .storm, intensity: 2, pop: 80, sub: "Storm"))
    #expect(near(pixel(try render(storm, t: frame(102)), 212, 66), RGB(hex: "#F0D040")!, 40), "the bolt flashes")
    #expect(!near(pixel(try render(storm, t: frame(104)), 212, 66), RGB(hex: "#F0D040")!, 40))
    #expect(near(pixel(try render(r, t: 0), 68, 215), black, 10), "the rain fades in from nothing")

    var wind = KnobWindFace()
    wind.have = true; wind.kmh = 18; wind.level = 1; wind.fraction = 0.36
    let q = KnobWindFace.sockQuads(level: 1, pose: 0)[0]
    let mid = CGPoint(x: q.map(\.x).reduce(0, +) / 4, y: q.map(\.y).reduce(0, +) / 4)
    #expect(near(pixel(try render(.wind(wind), t: 0), mid), black, 10), "the sock fades in")
    #expect(near(pixel(try render(.wind(wind), t: frame(40)), mid), KnobWindFace.color(18, w.wind), 30), "pose A again at frame 40")
    let rest = try render(.wind(wind)), c = try render(.wind(wind), t: frame(50))
    #expect(KnobWindFace.frame(level: 1, k: 50).pose == 2)
    let tip = KnobWindFace.sockQuads(level: 1, pose: 2)[4]
    let tipMid = CGPoint(x: tip.map(\.x).reduce(0, +) / 4, y: tip.map(\.y).reduce(0, +) / 4)
    let sock = KnobWindFace.color(18, w.wind)
    #expect(near(pixel(c, tipMid), sock, 30) && !near(pixel(rest, tipMid), sock, 30), "pose C swings the tip")
    let st = KnobWindFace.frame(level: 1, k: 50).streaks[1], st0 = KnobWindFace.frame(level: 1, k: 0).streaks[1]
    #expect(st.dx != st0.dx && st.opa > 40)
    let row = KnobWindFace.streakStart(level: 1, row: 1), len = Double(KnobWindFace.geo(level: 1).rows[1].len)
    let mark = CGPoint(x: row.x + Double(st.dx) + len / 2, y: row.y)
    #expect(!near(pixel(c, mark), black, 10) && !near(pixel(c, mark), pixel(rest, mark), 10), "the streak has moved on by frame 50")

    var cast = KnobCastFace()
    cast.have = true; cast.variant = 2; cast.wet = true; cast.sub = "Rain"
    #expect(near(pixel(try render(.cast(cast), t: 0.5), 64, 191), black, 12), "the rain waits for the gate")

    let (s, at) = sunFixture()
    let sun = KnobFace.sun(KnobSunFace(state: s, now: at))
    #expect(near(pixel(try render(sun, t: 0), 233, 160), black, 10), "the sun starts below the horizon")

    let days = KnobFace.days(KnobDaysFace(rows: [.init(wday: 1, icon: .sun, lo: 9, hi: 14)]))
    #expect(!has(try render(days, t: 0), RGB(hex: "#FFB547")!, rows: 190..<270, cols: 100..<366), "the rows fade in")

    var wilted = KnobAirFace()
    wilted.have = true; wilted.aqi = 68; wilted.band = 3; wilted.wilted = true; wilted.fraction = 0.68
    let petal = try render(.air(wilted), t: frame(90))
    #expect(near(pixel(petal, 262, 150), RGB(hex: "#C98A9A")!, 40), "the falling petal")
    #expect(!same(petal, try render(.air(wilted))), "no petal at rest")
}

@MainActor @Test func temperatureArrivalCountsUpRaisesTheMercuryAndSettles() throws {
    let f = KnobTempFace(value: 21, todayMinC: 6, todayMaxC: 25, sub: "Clear")
    let rest = try render(.weather(f)), start = try render(.weather(f), t: 0), mid = try render(.weather(f), t: 0.5)
    #expect(same(try render(.weather(f), t: 3), rest), "the arrival settles on the rest pose")
    let th = w.thermo, tube = CGPoint(x: 233, y: (KnobTempFace.mercuryTop(21, th) + th.mercuryBottomPx) / 2 + th.dyPx)
    #expect(near(pixel(rest, tube), KnobTempFace.color(21, w.scale), 30) && near(pixel(start, tube), black, 10), "the mercury rises from the bulb")
    #expect(markerRing(start, KnobFaceRender.gaugeMarkerPoint(0, t: nil, w)) && !markerRing(start, KnobFaceRender.gaugeMarkerPoint(f.fraction, t: nil, w)))
    #expect(markerRing(mid, KnobFaceRender.gaugeMarkerPoint(f.fraction, t: 0.5, w)), "the marker eases along the ring")
    #expect(has(mid, KnobTempFace.color(15, w.scale), rows: 210..<300, cols: 150..<320, tol: 12)
        && !has(mid, KnobTempFace.color(21, w.scale), rows: 210..<300, cols: 150..<320, tol: 4), "15° on the way to 21°")
}

@MainActor @Test func subLineFadesInAfterTheArrivalOnEveryFace() throws {
    let rows = 302..<335, cols = 150..<320
    let temp = KnobFace.weather(KnobTempFace(value: 21, sub: "Clear"))
    let rain = KnobFace.rain(KnobRainFace(dry: false, intensity: 3, pop: 80, sub: "Heavy rain"))
    for f in [temp, rain] {
        #expect(!has(try render(f, t: 1.3), w.sub.color, rows: rows, cols: cols, tol: 40), "hidden until 1.32 s: \(f)")
        let third = try render(f, t: 1.4)
        #expect(has(third, scaled(w.sub.color, 85.0 / 255), rows: rows, cols: cols, tol: 12)
            && !has(third, w.sub.color, rows: rows, cols: cols, tol: 40), "a third in: \(f)")
        #expect(has(try render(f, t: 1.7), w.sub.color, rows: rows, cols: cols, tol: 12), "full from 1.64 s: \(f)")
    }
}

@MainActor @Test func gaugeFacesEaseTheirMarkerAndCountUp() throws {
    let dry = KnobRainFace(dry: true, intensity: 1, pop: 40, sub: "Dry now")
    #expect(markerRing(try render(.rain(dry), t: 0.5), KnobFaceRender.gaugeMarkerPoint(0.4, t: 0.5, w)))
    #expect(same(try render(.rain(dry), t: 3), try render(.rain(dry))))
    var wind = KnobWindFace()
    wind.have = true; wind.kmh = 30; wind.level = 2; wind.fraction = 0.6
    let start = try render(.wind(wind), t: 0)
    #expect(markerRing(start, KnobFaceRender.gaugeMarkerPoint(0, t: nil, w)))
    #expect(has(start, KnobWindFace.color(0, w.wind), rows: 210..<300, cols: 150..<320, tol: 8)
        && !has(start, KnobWindFace.color(30, w.wind), rows: 210..<300, cols: 150..<320, tol: 8), "0 km/h in its colour")
    var air = KnobAirFace()
    air.have = true; air.aqi = 42; air.band = 2; air.fraction = 0.42
    #expect(markerRing(try render(.air(air), t: 0.5), KnobFaceRender.gaugeMarkerPoint(0.42, t: 0.5, w)))
    let (s, now) = sunFixture()
    let sun = KnobSunFace(state: s, now: now)
    let eased = KnobFaceRender.gaugeMarkerPoint(sun.fraction, t: 0.5, w)
    let half = try render(.sun(sun), t: 0.5)
    #expect(markerRing(half, eased) && !markerRing(half, ring(sun.fraction)), "the sun marker eases in too")
}

@MainActor @Test func thermometerTicksLightFromTheHalfPixelMercuryTopLikeTheKnob() throws {
    let f = KnobTempFace(value: -2), th = w.thermo, tick = CGPoint(x: (th.tickXPx[0] + th.tickXPx[1]) / 2, y: th.ticksPx[0] + th.dyPx)
    #expect(KnobTempFace.mercuryTop(-2, th) > th.ticksPx[0], "unrounded, -2° sits just under the 160 tick")
    #expect(near(pixel(try render(.weather(f), t: 1.3), tick), f.valueColor(w, t: 1.3), 30), "lit on the way up")
    #expect(near(pixel(try render(.weather(f)), tick), f.valueColor(w), 30), "and still lit at rest, as on the knob")
}

@MainActor @Test func castDropSlidesAlongTheRingDuringTheArrival() throws {
    var c = KnobCastFace()
    c.have = true; c.hasCount = true; c.hours = true; c.count = 5; c.variant = 2; c.wet = false; c.nowDeg = 75; c.toDeg = 153
    c.label = "17"; c.labelDeg = 151.5; c.sub = "rain at 17:00"
    let head = KnobCastFace.ringPoint(134.25)
    let mid = try render(.cast(c), t: 0.5), rest = try render(.cast(c))
    #expect(near(pixel(mid, head), w.cast.color, 30), "the drop's head on the ring")
    #expect(!near(pixel(rest, head), w.cast.color, 60), "no drop at rest")
    let a = 134.25 * .pi / 180
    #expect(near(pixel(mid, CGPoint(x: head.x - 10 * cos(a), y: head.y - 10 * sin(a))), w.cast.color, 40), "its tail trails up the ring")
    #expect(near(pixel(mid, CGPoint(x: head.x + 7 * cos(a), y: head.y + 7 * sin(a))), black, 30), "a black rim round the head")
    let late = try render(.cast(c), t: 1.25)
    #expect(!near(pixel(late, KnobCastFace.ringPoint(153)), w.cast.color, 60), "gone by 1.2 s")
}

private func sunFixture() -> (WeatherState, Date) {
    let mid: Int64 = 1_781_996_400
    let at = Date(timeIntervalSince1970: TimeInterval(mid + 10 * 3600 + 1800))
    func d(_ h: Int64, _ m: Int64) -> Date { Date(timeIntervalSince1970: TimeInterval(mid + h * 3600 + m * 60)) }
    var fx = WeatherState.Faces(utcOffsetS: 3600, night: false, windKmh: nil, windDirDeg: nil, rainIntensity: nil,
                                todayPopPct: nil, rain: nil, days: [], twilight: nil, moon: .init(phasePct: 0, illumPct: 0, up: true), aqi: nil)
    fx.twilight = .init(dawn: d(6, 55), blueEnd: d(7, 13), goldenEnd: d(8, 30), goldenStart: d(18, 30), blueStart: d(19, 47), dusk: d(20, 5))
    let s = WeatherState(generatedAt: at, enabled: true, provider: "open-meteo", units: "metric", locationName: nil,
                         current: .init(fetchedAt: at, stale: false, condition: "clear", conditionCode: "0", severe: false, tempC: 12,
                                        todayMinC: nil, todayMaxC: nil, cloudPct: nil, uv: nil, hourly: []),
                         air: nil, sun: .init(sunrise: d(7, 30), sunset: d(19, 30), night: false), faces: fx)
    return (s, at)
}

@MainActor private func drawCost(_ f: KnobFace, t: Double? = nil, n: Int) throws -> Double {
    _ = try render(f, t: t)
    let t0 = ContinuousClock.now
    for _ in 0..<n { _ = try render(f, t: t) }
    return (ContinuousClock.now - t0) / .milliseconds(n)
}

@MainActor @Test(.enabled(if: ProcessInfo.processInfo.environment["KNOB_BENCH"] != nil))
func weatherFacesDrawCostVersusTemp() throws {
    let built = ContinuousClock.now
    _ = KnobFaceRender.tempRays
    print("KNOB_BENCH temp rays raster built once in \(String(format: "%.1f", (ContinuousClock.now - built) / .milliseconds(1)))ms")
    let (s, at) = sunFixture()
    var cast = KnobCastFace()
    cast.have = true; cast.hasCount = true; cast.hours = true; cast.count = 2; cast.variant = 3; cast.nowDeg = 75; cast.labelDeg = 110
    cast.label = "15:40"; cast.sub = "rain"; cast.wet = true
    cast.arcs = [.init(a: 150, n: 70, intensity: 2)]
    var air = KnobAirFace()
    air.have = true; air.aqi = 42; air.band = 2; air.fraction = 0.42
    var wind = KnobWindFace()
    wind.have = true; wind.kmh = 18; wind.dir = 225; wind.level = 1; wind.fraction = 0.36
    let days = KnobDaysFace(rows: [.init(wday: 1, icon: .rain, pop: 70, lo: 9, hi: 14), .init(wday: 2, icon: .storm, lo: 7, hi: 19),
                                   .init(wday: 3, icon: .partly, lo: 7, hi: 19), .init(wday: 4, icon: .cloud, lo: 7, hi: 19)])
    let faces: [(String, KnobFace)] = [
        ("temp", .weather(KnobTempFace(value: 12, todayMinC: 6, todayMaxC: 15, sub: "Clear"))),
        ("rain", .rain(KnobRainFace(dry: false, intensity: 3, pop: 80, untilMin: 885, sub: "until 14:45"))),
        ("cast", .cast(cast)), ("wind", .wind(wind)), ("days", .days(days)),
        ("sun", .sun(KnobSunFace(state: s, now: at))), ("air", .air(air)),
    ]
    let moon = WeatherState.Faces.Moon(phasePct: 80, illumPct: 30, up: true)
    let skies: [(String, KnobFace)] = [
        ("temp-sun", .weather(KnobTempFace(value: 12, todayMinC: 6, todayMaxC: 15, sub: "Mostly sunny", cloudBand: 1))),
        ("temp-moon", .weather(KnobTempFace(value: 12, todayMinC: 6, todayMaxC: 15, sub: "Clear", cloudBand: 0, night: true, moon: moon))),
        ("temp-overcast", .weather(KnobTempFace(value: 12, todayMinC: 6, todayMaxC: 15, sub: "Overcast", cloudBand: 4))),
    ]
    for t in [nil, 0.5, 1.4] as [Double?] {
        var report = ""
        for (name, f) in faces + skies { report += "\(name)=\(String(format: "%.2f", try drawCost(f, t: t, n: 300)))ms " }
        print("KNOB_BENCH t=\(t.map { String($0) } ?? "rest") \(report)")
    }
    for t in [60.0, 600, 3600] {
        let replay = KnobTempDrift(KnobTempSky.bands[4]), t0 = ContinuousClock.now
        for _ in 0..<20 { _ = replay.offsets(t: t - KnobArrive.seconds) }
        let drift = (ContinuousClock.now - t0) / .milliseconds(20)
        print("KNOB_BENCH t=\(Int(t)) temp-overcast=\(String(format: "%.2f", try drawCost(skies[2].1, t: t, n: 20)))ms drift-replay=\(String(format: "%.2f", drift))ms")
    }
}

@Test func prebuiltCloudPathsMatchADirectBuild() {
    for (v, sides) in KnobCastFace.clouds.enumerated() {
        for (si, side) in sides.enumerated() {
            for (ci, cl) in side.clouds.enumerated() {
                #expect(KnobFaceRender.castCloudPaths[v][si][ci] == KnobFaceRender.buildCloud(cl.base, cl.circles.map { ($0.x, $0.y, $0.r) }))
            }
        }
    }
    #expect(KnobFaceRender.dayCloudPartly == KnobFaceRender.buildCloud(33, [(13, 28, 7), (21, 23, 8.5), (30, 28, 7)]))
    #expect(KnobFaceRender.dayCloudMid == KnobFaceRender.buildCloud(30, [(10, 25, 7), (19, 20, 9), (29, 25, 7)]))
    #expect(KnobFaceRender.dayCloudHigh == KnobFaceRender.buildCloud(24, [(10, 19, 7), (19, 14, 9), (29, 19, 7)]))
}

@MainActor
private func canvas(_ draw: @escaping (inout GraphicsContext) -> Void) throws -> CGImage {
    let r = ImageRenderer(content: Canvas { ctx, _ in
        ctx.fill(Path(CGRect(x: 0, y: 0, width: 466, height: 466)), with: .color(.black))
        draw(&ctx)
    }.frame(width: 466, height: 466))
    r.scale = 1
    return try #require(r.cgImage)
}

private func lit(_ p: RGB) -> Int { Int(p.r) + Int(p.g) + Int(p.b) }

@MainActor @Test func rainSheetsDrawEveryWrapTheirGuardLetsThrough() throws {
    let up = KnobRainFace.Sheet(box: CGRect(x: 100, y: 100, width: 60, height: 182), tileFrames: 24, delayFrames: 47,
                                drops: [.init(x: 130, y: 149, len: 18, op: 1, slant: 0, tile: 1, level: 1)])
    let down = KnobRainFace.Sheet(box: CGRect(x: 300, y: 40, width: 60, height: 400), tileFrames: 24, delayFrames: 0,
                                  drops: [.init(x: 330, y: 10, len: 18, op: 1, slant: 0, tile: 0, level: 1)])
    #expect(KnobRainFace.sheetOffset(up, snow: false, k: 0) == 294)
    let img = try canvas { KnobFaceRender.drops(KnobRainFace(dry: false, intensity: 1, pop: 50), w, sheets: [up, down], in: &$0) }
    #expect(lit(pixel(img, 130, 106)) > 20, "a tile-B drop wrapped two periods up into the top of the box")
    #expect(lit(pixel(img, 330, 360)) > 200, "a drop wrapped one period down in a box taller than the period")
}

@MainActor @Test func castSheetsDrawEveryWrapTheirGuardLetsThrough() throws {
    let up = KnobCastFace.Sheet(box: CGRect(x: 100, y: 100, width: 100, height: 40), h: 40, tileFrames: 40, delayFrames: 79, width: 4,
                                a: [], b: [.init(x: 150, y: 37, len: 10, op: 1, slant: 0)])
    let down = KnobCastFace.Sheet(box: CGRect(x: 300, y: 100, width: 60, height: 130), h: 40, tileFrames: 40, delayFrames: 0, width: 4,
                                  a: [.init(x: 320, y: 5, len: 10, op: 1, slant: 0)], b: [])
    #expect(KnobCastFace.sheetOffset(up, snow: false, k: 0) == 79)
    let img = try canvas { ctx in
        for s in [up, down] { KnobFaceRender.castSheet(s, snow: false, color: .white, in: &ctx) }
    }
    #expect(lit(pixel(img, 150, 104)) > 600, "a tile-B streak wrapped two periods up into the box")
    #expect(lit(pixel(img, 320, 190)) > 600, "a streak wrapped one period down in a box taller than the period")
}
