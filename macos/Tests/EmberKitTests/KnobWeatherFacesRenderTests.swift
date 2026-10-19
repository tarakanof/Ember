import Foundation
import ImageIO
import SwiftUI
import Testing
@testable import EmberKit

private let th = KnobTheme.standard
private let w = th.weather

@MainActor
private func render(_ f: KnobFace) throws -> CGImage {
    let r = ImageRenderer(content: KnobFaceView(f).frame(width: 466, height: 466).environment(\.colorScheme, .dark))
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
    #expect(markerRing(img, KnobTempFace.gaugePoint(0.8, w, center: 233)))
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
    #expect(markerRing(img, KnobTempFace.gaugePoint(0.36, w, center: 233)))
    let q = KnobWindFace.sockQuads(level: 1)
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
    let mid: Int64 = 1_781_996_400
    let now = mid + 10 * 3600 + 1800
    var f = WeatherState.Faces(utcOffsetS: 3600, night: false, windKmh: nil, gustKmh: nil, windDirDeg: nil, rainIntensity: nil,
                               todayPopPct: nil, rain: nil, days: [], twilight: nil, moon: .init(phasePct: 0, illumPct: 0, up: true), aqi: nil)
    func d(_ h: Int64, _ m: Int64) -> Date { Date(timeIntervalSince1970: TimeInterval(mid + h * 3600 + m * 60)) }
    f.twilight = .init(dawn: d(6, 55), blueEnd: d(7, 13), goldenEnd: d(8, 30), goldenStart: d(18, 30), blueStart: d(19, 47), dusk: d(20, 5))
    let at = Date(timeIntervalSince1970: TimeInterval(now))
    let s = WeatherState(generatedAt: at, enabled: true, provider: "open-meteo", units: "metric", locationName: nil,
                         current: .init(fetchedAt: at, stale: false, condition: "clear", conditionCode: "0", severe: false, tempC: 12,
                                        todayMinC: nil, todayMaxC: nil, cloudPct: nil, uv: nil, hourly: []),
                         air: nil, sun: .init(sunrise: d(7, 30), sunset: d(19, 30), night: false), faces: f)
    let face = KnobSunFace(state: s, now: at)
    let img = try render(.sun(face))
    #expect(litDot(img) == 5)
    #expect(near(pixel(img, ring(0.01)), w.sun.blueOut, 30) && near(pixel(img, ring(0.5)), w.sun.dayNoon, 30))
    #expect(markerRing(img, KnobTempFace.gaugePoint(face.fraction, w, center: 233)))
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
    #expect(markerRing(img, KnobTempFace.gaugePoint(0.42, w, center: 233)))
    #expect(near(pixel(img, 233, 112), RGB(hex: "#F5C84A")!, 30), "the healthy flower's centre")
    #expect(has(img, KnobAirFace.color(42, w.air), rows: 210..<300, cols: 150..<320) && has(img, RGB(hex: "#F0E641")!, rows: 302..<335, cols: 150..<320))
    a.aqi = 68; a.band = 3; a.wilted = true; a.fraction = 0.68
    let wilted = try render(.air(a))
    #expect(!near(pixel(wilted, 233, 112), RGB(hex: "#F5C84A")!, 30) && has(wilted, RGB(hex: "#FF5050")!, rows: 108..<146, cols: 296..<340),
            "the wilted flower and the hazard sign")
}
