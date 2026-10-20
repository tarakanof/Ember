import Foundation
import ImageIO
import SwiftUI
import Testing
@testable import EmberKit

private let cDrift: [[[UInt32]]] = [
    [[0x41292a82, 0x3ef529e9, 0x3e66ab64, 0x3f800000]],
    [[0x40fd89a6, 0x3f358b89, 0x3ee05060, 0x3f800000], [0x412da74e, 0x3e89c39a, 0x3f6ba01d, 0x3f800000]],
    [[0x412cea56, 0x3f72d59f, 0x3f6ed627, 0x3f19999a], [0x410bda7a, 0x3eb25f42, 0x3eeadda6, 0x3f19999a], [0x40ebb3e5, 0x3e594b0c, 0x3f351861, 0x3f800000], [0x414bae49, 0x3f13044e, 0x3f61c101, 0x3f800000]],
    [[0x412cea56, 0x3f72d59f, 0x3f6ed627, 0x3f19999a], [0x410bda7a, 0x3eb25f42, 0x3eeadda6, 0x3f19999a], [0x40ebb3e5, 0x3e594b0c, 0x3f351861, 0x3f800000], [0x414bae49, 0x3f13044e, 0x3f61c101, 0x3f800000]],
]
private let cFrames: [Int] = [0, 1, 14, 15, 29, 30, 45, 60, 75, 90, 120, 150, 225, 300, 600, 1500, 9000, 54000]
private let cOffsets: [[[Int]]] = [
    [[0, 0], [0, 0], [-1, 0], [-1, 0], [-5, 1], [-5, 1], [-6, 0], [-5, -1], [-2, -1], [2, -1], [6, 0], [3, 1], [-4, -1], [4, 1], [6, 0], [-2, -1], [6, 0], [-1, -1]],
    [[0, 0, 0, 0], [0, 0, 0, 0], [-2, 0, 2, 0], [-3, 0, 2, 0], [-2, -1, 2, 1], [-1, -1, 2, 1], [3, -1, -2, 1], [6, 0, -5, 1], [5, 0, -6, 1], [1, 1, -5, 0], [-6, 0, 0, -1], [-1, -1, 6, -1], [-4, 1, -5, 1], [6, 0, 4, -1], [-6, 0, -2, -1], [5, 0, 1, 1], [2, 1, -2, 1], [3, -1, -1, -1]],
    [[0, 0, 0, 0, 0, 0, 0, 0], [0, 0, 0, 0, 0, 0, 0, 0], [0, 0, 0, 0, 2, 0, -2, 0], [0, 0, 0, 0, 2, 0, -2, 0], [2, 0, -1, -1, 1, 0, -6, 0], [3, 0, -1, -1, 1, 0, -6, 0], [4, 1, -3, -1, -4, 1, -6, 1], [3, 1, -3, 0, -6, 1, -4, 1], [2, 0, -2, 0, -4, 1, -1, 1], [0, 0, 1, 0, 0, 0, 2, 1], [-3, -1, 4, 0, 6, -1, 6, 0], [-3, 0, 0, 0, -3, 0, 5, -1], [3, 1, 1, 1, 6, -1, -6, 0], [-3, -1, -2, -1, -3, 1, 5, 0], [-3, 0, -2, 0, -5, 1, -6, 0], [3, 1, -3, 0, -6, 1, 3, -1], [1, 0, 0, 0, -5, 1, -6, 0], [1, 0, 3, 1, -2, 0, 4, -1]],
    [[0, 0, 0, 0, 0, 0, 0, 0], [0, 0, 0, 0, 0, 0, 0, 0], [0, 0, 0, 0, 2, 0, -2, 0], [0, 0, 0, 0, 2, 0, -2, 0], [2, 0, -1, -1, 1, 0, -6, 0], [3, 0, -1, -1, 1, 0, -6, 0], [4, 1, -3, -1, -4, 1, -6, 1], [3, 1, -3, 0, -6, 1, -4, 1], [2, 0, -2, 0, -4, 1, -1, 1], [0, 0, 1, 0, 0, 0, 2, 1], [-3, -1, 4, 0, 6, -1, 6, 0], [-3, 0, 0, 0, -3, 0, 5, -1], [3, 1, 1, 1, 6, -1, -6, 0], [-3, -1, -2, -1, -3, 1, 5, 0], [-3, 0, -2, 0, -5, 1, -6, 0], [3, 1, -3, 0, -6, 1, 3, -1], [1, 0, 0, 0, -5, 1, -6, 0], [1, 0, 3, 1, -2, 0, 4, -1]],
]
private let cSampledFNV: [UInt32] = [0xf81758d9, 0x22a38c11, 0x38571f09, 0x38571f09]
private let cShapes: [[[UInt32]]] = [
    [[0x439103c9, 0x43160000, 0x439bb341, 0x42f087e0, 0x43b93367, 0x42e27922, 0x43ca4211, 0x43160000]],
    [[0x425e5be4, 0x43360000, 0x42bf4645, 0x430ad7cb, 0x4324e643, 0x4313181b, 0x433dfcb0, 0x43360000], [0x438e01a8, 0x43200000, 0x439a8cdf, 0x42fa3038, 0x43bd2e6f, 0x42e9af97, 0x43d13484, 0x43200000]],
    [[0x41d5273a, 0x430e0000, 0x428e4864, 0x42bc186e, 0x43141973, 0x42ce6e76, 0x432ff97a, 0x430e0000], [0x43920343, 0x43000000, 0x439ff346, 0x42b26e74, 0x43c66de7, 0x42a0186e, 0x43dcad8c, 0x43000000], [0x4232939d, 0x433e0000, 0x42b24864, 0x430e0c38, 0x43261973, 0x4317373c, 0x4341f97a, 0x433e0000], [0x438b0343, 0x43300000, 0x4398f346, 0x4309373b, 0x43bf6de7, 0x43000c37, 0x43d5ad8c, 0x43300000]],
    [[0xc03e9ba8, 0x430c0000, 0x42090d66, 0x42c7cf69, 0x42be28b8, 0x42b549c3, 0x4313edff, 0x42e01b21, 0x43300000, 0x430c0000], [0x439183c5, 0x43000000, 0x43a101f6, 0x42c2158a, 0x43ba299e, 0x4297de6f, 0x43d7de54, 0x42afcf69, 0x43ea7d37, 0x43000000], [0x00000000, 0x43440000, 0x4224767a, 0x43195c8f, 0x42d2686d, 0x43147f68, 0x431dfd9e, 0x4328bf60, 0x43457e5c, 0x43440000], [0x438440d2, 0x43380000, 0x43961d0c, 0x4318512a, 0x43af10c0, 0x4305c1ce, 0x43ce7131, 0x430d5c8f, 0x43e30000, 0x43380000]],
]
private let cRaysFNV: UInt32 = 0xeea9c16c, cRaysSum = 197_777
private let cRaySamples: [(x: Int, y: Int, bits: UInt32)] = [
    (251, 40, 0x3c3ebdf6), (161, 69, 0x3e1cfa9e), (202, 69, 0x3ec193ce), (321, 69, 0x3c40fda6), (188, 98, 0x3cbd2d93), (275, 127, 0x3ca4be61),
    (167, 156, 0x3e5ce653), (45, 185, 0x3ca3010a), (99, 185, 0x3e67204e), (140, 185, 0x3ea63b31), (297, 185, 0x3d2dff09), (61, 214, 0x3e5e144e),
]

private func fnv(_ h: UInt32, _ b: Int) -> UInt32 { (h ^ UInt32(UInt8(bitPattern: Int8(b)))) &* 16_777_619 }

private let moon = WeatherState.Faces.Moon(phasePct: 80, illumPct: 30, up: true)

private func sky(_ band: Int, night: Bool = false, moon m: WeatherState.Faces.Moon? = nil) -> KnobTempFace {
    KnobTempFace(value: 9.4, todayMinC: 6, todayMaxC: 15, sub: "Clear", cloudBand: band, night: night, moon: m)
}

@Test func heroStarsAndMoonFollowCindersRules() {
    #expect(sky(0).hero == .sun && !sky(0).stars && sky(1).hero == .sun, "AT4: the sun up to one cloud")
    #expect(sky(2).hero == .none && sky(4).hero == .none, "two clouds and more hide it")
    let at5 = sky(0, night: true, moon: moon)
    #expect(at5.hero == .moon && at5.stars && at5.moonPhase == 80 && at5.moonIllum == 30, "AT5 moon")
    #expect(sky(0, night: true, moon: .init(phasePct: 80, illumPct: 30, up: false)).hero == .none, "moon down")
    #expect(sky(0, night: true, moon: .init(phasePct: 80, illumPct: 30, up: nil)).hero == .none, "moon.up unknown")
    let noMoon = sky(0, night: true)
    #expect(noMoon.hero == .none && noMoon.stars, "no moon data: stars only")
    let at6 = sky(3, night: true, moon: moon)
    #expect(at6.hero == .moonSmall && !at6.stars, "AT6: a small moon behind light grey clouds")
    #expect(sky(4, night: true, moon: moon).hero == .none, "overcast night: no moon")
    #expect([32, 33, 0, 100, 2, 3].map { sky(0, night: true, moon: .init(phasePct: 10, illumPct: $0, up: true)).moonIllum } == [30, 35, 0, 100, 0, 5])
    #expect(sky(0, night: true, moon: .init(phasePct: 100, illumPct: 30, up: true)).hero == .none, "a phase past 99 is no moon")
    #expect(sky(0, night: true, moon: .init(phasePct: 10, illumPct: 101, up: true)).hero == .none, "illumination past 100 is no moon")
    let none = KnobTempFace(value: nil, cloudBand: 0, night: true, moon: moon)
    #expect(none.hero == .none && !none.stars && KnobTempSky.band(none) == nil, "no temperature: no sky")
    #expect(KnobTempFace(value: 12).hero == .none && KnobTempSky.band(KnobTempFace(value: 12)) == nil, "no band: no sky")
}

@Test func tempFaceReadsTheMoonFromTheWeatherState() throws {
    let night = Date(timeIntervalSince1970: 1_790_000_000)
    var faces = WeatherState.Faces(utcOffsetS: 0, night: true, windKmh: nil, windDirDeg: nil, rainIntensity: nil, todayPopPct: nil, rain: nil,
                                   days: [], twilight: nil, moon: moon, aqi: nil)
    let state = WeatherState(generatedAt: night, enabled: true, provider: "open-meteo", units: "metric", locationName: nil,
                             current: .init(fetchedAt: night, stale: false, condition: "clear", conditionCode: "0", severe: false, tempC: 9.4,
                                            todayMinC: 6, todayMaxC: 15, cloudPct: 0, uv: nil, hourly: []),
                             air: nil, sun: .init(sunrise: night.addingTimeInterval(8 * 3600), sunset: night.addingTimeInterval(-4 * 3600), night: true),
                             faces: faces)
    var utc = Calendar(identifier: .gregorian)
    utc.timeZone = try #require(TimeZone(identifier: "UTC"))
    let f = KnobTempFace(state: state, now: night, calendar: utc)
    #expect(f.night && f.hero == .moon && f.stars && f.moonPhase == 80 && f.moonIllum == 30)
    faces.moon = nil
    var dark = state
    dark.faces = faces
    #expect(KnobTempFace(state: dark, now: night, calendar: utc).hero == .none)
}

@Test func cloudBandsMatchCindersTablesAndTones() {
    #expect(KnobTempSky.bands.map(\.clouds.count) == [0, 1, 2, 4, 4])
    #expect(KnobTempSky.bands.map(\.fadeFrom) == [175, 175, 175, 165, 160] && KnobTempSky.bands.map(\.tone) == [0, 0, 0, 1, 2])
    #expect(KnobTempSky.bands[3].clouds.map(\.back) == [true, true, false, false], "back clouds first")
    #expect(KnobTempSky.tone(sky(3, night: true), KnobTempSky.bands[3]) == KnobTempSky.nightTone && KnobTempSky.tone(sky(3), KnobTempSky.bands[3]) == 1)
    #expect(KnobTempSky.heroCenter(.sun) == (116.3, 116.3) && KnobTempSky.heroCenter(.moon) == (144.6, 144.6) && KnobTempSky.heroCenter(.moonSmall) == (300, 72))
}

@Test func cloudShapesMatchTheCHarness() throws {
    for (b, band) in KnobTempSky.bands.dropFirst().enumerated() {
        #expect(band.clouds.count == cShapes[b].count)
        for (i, cl) in band.clouds.enumerated() {
            let pts = try #require(KnobTempSky.shape(cl), "band \(b + 1) cloud \(i)")
            #expect(pts.flatMap { [$0.x.bitPattern, $0.y.bitPattern] } == cShapes[b][i], "band \(b + 1) cloud \(i)")
            let path = KnobFaceRender.tempCloudPaths[b + 1][i], box = path.boundingRect
            #expect(path == KnobFaceRender.skyCloud(cl), "prebuilt")
            let top = cl.puffs.map { Double($0.y - $0.r) }.min() ?? 0
            let left = cl.puffs.map { Double($0.x - $0.r) }.min() ?? 0, right = cl.puffs.map { Double($0.x + $0.r) }.max() ?? 0
            #expect(abs(box.maxY - Double(cl.base)) < 0.05 && abs(box.minY - top) < 0.05 && box.minX >= left - 0.05 && box.maxX <= right + 0.05,
                    "band \(b + 1) cloud \(i): the outline runs from the highest puff down to the base, inside the puffs")
        }
    }
    #expect(KnobTempSky.shape(.init(back: false, base: 100, puffs: [.init(x: 0, y: 0, r: 10)])) == nil, "a puff above the base is no cloud")
    #expect(KnobTempSky.shape(.init(back: false, base: 100, puffs: [.init(x: 0, y: 95, r: 10), .init(x: 50, y: 95, r: 10)])) == nil,
            "puffs that do not touch are no cloud")
}

private func fullReplay(_ d: KnobTempDrift, through last: Int) -> [[KnobTempDrift.Offset]] {
    var at = d.rest, turn = 0, out: [[KnobTempDrift.Offset]] = []
    for f in 0...last {
        for k in 0..<d.count {
            let i = (turn + k) % d.count, to = d.path(i, Double(f) / KnobTempDrift.fps)
            guard to != at[i] else { continue }
            at[i] = .init(dx: at[i].dx + (to.dx > at[i].dx ? 1 : 0) - (to.dx < at[i].dx ? 1 : 0),
                          dy: at[i].dy + (to.dy > at[i].dy ? 1 : 0) - (to.dy < at[i].dy ? 1 : 0))
            turn = (i + 1) % d.count
            break
        }
        out.append(at)
    }
    return out
}

@Test func driftSeedsAndPathsMatchTheCHarness() {
    for (b, band) in KnobTempSky.bands.dropFirst().enumerated() {
        let d = KnobTempDrift(band)
        #expect(d.count == band.clouds.count)
        for i in 0..<d.count {
            #expect([d.period[i], d.phaseX[i], d.phaseY[i], d.depth[i]].map(\.bitPattern) == cDrift[b][i], "band \(b + 1) cloud \(i)")
        }
        for (k, f) in cFrames.enumerated() {
            let at = d.offsets(t: (Double(f) + 0.5) / KnobTempDrift.fps)
            #expect(at.flatMap { [$0.dx, $0.dy] } == cOffsets[b][k], "band \(b + 1) frame \(f)")
        }
        var h: UInt32 = 2_166_136_261
        for f in stride(from: 0, through: 9000, by: 150) {
            for o in d.offsets(t: (Double(f) + 0.5) / KnobTempDrift.fps) { h = fnv(fnv(h, o.dx), o.dy) }
        }
        #expect(h == cSampledFNV[b], "band \(b + 1): every 150th frame up to 10 min")
        let full = fullReplay(d, through: 2700)
        for f in 901...2700 where d.offsets(t: (Double(f) + 0.5) / KnobTempDrift.fps) != full[f] {
            Issue.record("band \(b + 1) frame \(f): the bounded replay differs from the full one")
        }
        for a in stride(from: 0, through: 54000, by: 2700) {
            #expect(d.anchor(from: a, to: a + KnobTempDrift.window) != nil, "band \(b + 1): a known state within a minute of frame \(a)")
        }
    }
    #expect(KnobTempDrift(back: []).offsets(t: 9).isEmpty && KnobTempDrift(KnobTempSky.bands[1]).offsets(t: -0.1) == [.init(dx: 0, dy: 0)])
    #expect(KnobTempDrift(back: [false]).path(0, 0) == .init(dx: 0, dy: 0), "every path starts at rest")
    #expect(KnobTempDrift(back: [false], starts: 2) != KnobTempDrift(back: [false]), "a second start takes new paths")
}

@Test func rayFanMatchesTheCHarness() throws {
    for s in cRaySamples {
        #expect(KnobTempSky.rayAlpha(Float(s.x) + 0.5, Float(s.y) + 0.5).bitPattern == s.bits, "ray alpha at \(s.x),\(s.y)")
    }
    var h: UInt32 = 2_166_136_261, sum = 0
    for y in 0..<466 {
        for x in 0..<466 {
            let q = KnobTempSky.rayRange.contains(x) && KnobTempSky.rayRange.contains(y) ? KnobTempSky.alpha8(KnobTempSky.rayAlpha(Float(x) + 0.5, Float(y) + 0.5)) : 0
            h = (h ^ UInt32(q)) &* 16_777_619
            sum += Int(q)
        }
    }
    #expect(h == cRaysFNV && sum == cRaysSum)
    let rays = try #require(KnobFaceRender.tempRays)
    #expect(rays.box == CGRect(x: 40, y: 39, width: 360, height: 358) && rays.image.width == 360 && rays.image.height == 358)
}

@Test func moonGeometryFollowsPhaseAndIllumination() {
    let waning = KnobTempSky.moon(sky(0, night: true, moon: moon))
    #expect(!waning.waxing && waning.r == 50 && waning.glowR == 80 && waning.glow == 0.2 && abs(waning.rx - 20) < 1e-5)
    #expect(abs(waning.lightX - 162.1) < 1e-4 && abs(waning.lightY - 134.6) < 1e-4 && abs(waning.lightR - 60) < 1e-4)
    let small = KnobTempSky.moon(sky(3, night: true, moon: .init(phasePct: 10, illumPct: 30, up: true)))
    #expect(small.waxing && small.cx == 300 && small.cy == 72 && small.r == 34 && small.glowR == 54 && small.glow == 0.14)
}

@Test func cloudyTemperatureLoopsAndClearSkiesSettle() {
    #expect(KnobWeatherMotion(.weather(sky(1))) == .loop, "the cloud drifts forever, like cinder's")
    #expect(KnobWeatherMotion(.weather(sky(4, night: true))) == .loop)
    #expect(KnobWeatherMotion(.weather(sky(0))) == .arrival(until: KnobArrive.subRestS), "a clear sky settles once the sun has faded in")
    #expect(KnobWeatherMotion(.weather(sky(2)), reduceMotion: true) == .still)
    #expect(KnobWeatherMotion(.weather(KnobTempFace(value: nil, cloudBand: 2))) == .still)
    let since = Date(timeIntervalSince1970: 1_000)
    let it = KnobWeatherSchedule(since: since, motion: KnobWeatherMotion(.weather(sky(2))))
        .entries(from: since.addingTimeInterval(600), mode: .normal)
    let ticks = (0..<4).compactMap { _ in it.next()?.timeIntervalSince(since) }
    #expect(ticks.count == 4 && abs(ticks[3] - ticks[2] - 1 / 15) < 1e-6, "still ticking at 15 fps ten minutes in")
}

@MainActor
private func render(_ f: KnobTempFace, t: Double?) throws -> CGImage {
    let r = ImageRenderer(content: KnobFaceView(.weather(f), t: t).frame(width: 466, height: 466).environment(\.colorScheme, .dark))
    r.scale = 1
    let img = try #require(r.cgImage)
    if let dir = ProcessInfo.processInfo.environment["KNOB_SNAPSHOT_DIR"],
       let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: dir)
           .appendingPathComponent("temp-\(f.cloudBand ?? -1)-\(f.hero)-\(t.map { String($0) } ?? "rest").png") as CFURL,
                                                  "public.png" as CFString, 1, nil) {
        CGImageDestinationAddImage(dest, img, nil)
        CGImageDestinationFinalize(dest)
    }
    return img
}

private func bytes(_ img: CGImage) -> [UInt8] {
    var buf = [UInt8](repeating: 0, count: img.width * img.height * 4)
    let ctx = CGContext(data: &buf, width: img.width, height: img.height, bitsPerComponent: 8, bytesPerRow: img.width * 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: 0, y: 0, width: img.width, height: img.height))
    return buf
}

private func pixel(_ img: CGImage, _ x: Int, _ y: Int) -> RGB {
    let b = bytes(img), i = (y * img.width + x) * 4
    return RGB(r: b[i], g: b[i + 1], b: b[i + 2])
}

private func near(_ a: RGB, _ b: RGB, _ tol: Int = 24) -> Bool {
    abs(Int(a.r) - Int(b.r)) <= tol && abs(Int(a.g) - Int(b.g)) <= tol && abs(Int(a.b) - Int(b.b)) <= tol
}

private let black = RGB(r: 0, g: 0, b: 0)

@MainActor @Test func theRestPoseDrawsNoSkyAndStaysAsItWas() throws {
    for f in [sky(1), sky(4, night: true, moon: moon), sky(0, night: true, moon: moon)] {
        var bare = KnobTempFace(value: 9.4, todayMinC: 6, todayMaxC: 15, sub: "Clear")
        bare.cloudBand = f.cloudBand
        #expect(sameImage(try render(f, t: nil), try render(bare, t: nil)), "band \(f.cloudBand ?? -1) \(f.hero): pixel-identical without t")
        #expect(!sameImage(try render(f, t: 5), try render(bare, t: nil)), "band \(f.cloudBand ?? -1): the tolerance still sees the sky")
    }
}

@MainActor @Test func theSkyIsDrawnAtTimeZero() throws {
    for f in [sky(1), sky(4, night: true, moon: moon), sky(0, night: true, moon: moon)] {
        var bare = KnobTempFace(value: 9.4, todayMinC: 6, todayMaxC: 15, sub: "Clear")
        bare.cloudBand = f.cloudBand
        #expect(!sameImage(try render(f, t: 0), try render(bare, t: nil)), "band \(f.cloudBand ?? -1): t=0 is not the bare rest pose")
    }
}

private let cinderPixels: [(face: KnobTempFace, x: Int, y: Int, rgb: RGB)] = [
    (sky(1), 116, 116, RGB(r: 188, g: 170, b: 91)), (sky(1), 150, 150, RGB(r: 211, g: 126, b: 49)), (sky(1), 140, 185, RGB(r: 87, g: 65, b: 26)),
    (sky(1), 279, 136, RGB(r: 192, g: 204, b: 208)), (sky(1), 289, 136, RGB(r: 224, g: 236, b: 240)), (sky(1), 341, 130, RGB(r: 224, g: 236, b: 240)),
    (sky(0, night: true, moon: moon), 305, 95, RGB(r: 72, g: 80, b: 88)), (sky(0, night: true, moon: moon), 105, 145, RGB(r: 200, g: 208, b: 216)),
    (sky(0, night: true, moon: moon), 144, 200, RGB(r: 25, g: 29, b: 33)),
    (sky(3, night: true, moon: .init(phasePct: 10, illumPct: 30, up: true)), 300, 100, RGB(r: 40, g: 52, b: 56)),
    (sky(3, night: true, moon: .init(phasePct: 10, illumPct: 30, up: true)), 120, 160, RGB(r: 56, g: 68, b: 80)),
    (sky(4), 120, 180, RGB(r: 80, g: 92, b: 104)), (sky(4), 330, 175, RGB(r: 80, g: 92, b: 104)), (sky(2), 130, 160, RGB(r: 224, g: 236, b: 240)),
]

@MainActor @Test func theSkyDrawsLikeCindersBakeOnceAFrameRuns() throws {
    #expect(KnobTempDrift(KnobTempSky.bands[1]).offsets(t: 5 - KnobArrive.seconds) == [.init(dx: -5, dy: 0)])
    for p in cinderPixels {
        let drawn = pixel(try render(p.face, t: 5), p.x, p.y)
        #expect(near(drawn, p.rgb, 12), "band \(p.face.cloudBand ?? -1) \(p.face.hero) at \(p.x),\(p.y): \(drawn), cinder \(p.rgb)")
        #expect(pixel(try render(p.face, t: nil), p.x, p.y) == black, "no sky without a frame at \(p.x),\(p.y)")
    }
    #expect(pixel(try render(sky(0, night: true, moon: moon), t: 5), 180, 145) == black, "the waning moon's right side is dark")
    #expect(pixel(try render(sky(1), t: 1.4), 279, 136) == black, "the cloud has not drifted yet")
}

@MainActor @Test func heroAndCloudsFadeInOnTheSpriteClock() throws {
    let x = 360, y = 140
    #expect(KnobTempSky.rayAlpha(Float(x) + 0.5, Float(y) + 0.5) == 0, "a cloud pixel with no ray behind it")
    let full = pixel(try render(sky(1), t: 5), x, y), early = pixel(try render(sky(1), t: 0.2), x, y)
    let k = Double(KnobArrive.spriteOpacity(0.2)) / 255
    func scaled(_ c: UInt8) -> UInt8 { UInt8((Double(c) * k).rounded()) }
    #expect(near(early, RGB(r: scaled(full.r), g: scaled(full.g), b: scaled(full.b)), 3), "\(early) is \(k) of \(full)")
    let start = try render(sky(1), t: 0)
    #expect(pixel(start, x, y) == black && pixel(start, 116, 116) == black, "no hero or cloud at t 0")
    #expect(near(pixel(start, 140, 185), RGB(r: 87, g: 65, b: 26), 12), "the rays are background: no fade")
}

@Test func reduceMotionSettlesTheTemperatureSky() {
    #expect(KnobWeatherLive.rest(.weather(sky(1)), animated: true, reduceMotion: true) == (KnobArrive.subRestS, 0.0))
    #expect(KnobArrive.subOpacity(KnobArrive.subRestS) == 255 && KnobArrive.spriteOpacity(KnobArrive.subRestS) == 255
        && KnobArrive.eased(KnobArrive.subRestS) == 1 && KnobArrive.count(21, KnobArrive.subRestS) == 21, "every arrival has settled")
    #expect(KnobTempDrift(KnobTempSky.bands[4]).offsets(t: 0) == KnobTempDrift(KnobTempSky.bands[4]).rest, "the clouds at rest")
    #expect(KnobWeatherLive.rest(.weather(sky(1)), animated: true, reduceMotion: false) == (nil, nil), "animating: the timeline gives t")
    #expect(KnobWeatherLive.rest(.weather(sky(1)), animated: false, reduceMotion: true) == (nil, nil), "the page strip stays the static rest pose")
    #expect(KnobWeatherLive.rest(.rain(KnobRainFace(dry: false, intensity: 2, pop: 60)), animated: true, reduceMotion: true) == (nil, nil),
            "the other faces' rest pose is their t-less draw")
}

@MainActor @Test func reduceMotionDrawsTheSettledSkyOverTheRestPose() throws {
    for f in [sky(1), sky(4), sky(0, night: true, moon: moon)] {
        let rest = KnobWeatherLive.rest(.weather(f), animated: true, reduceMotion: true)
        let r = ImageRenderer(content: KnobFaceView(.weather(f), t: rest.t, drift: rest.drift).frame(width: 466, height: 466)
            .environment(\.colorScheme, .dark))
        r.scale = 1
        let settled = try #require(r.cgImage), bare = try render(f, t: nil)
        let below = 230 * 466 * 4
        if f.hero == .none && !f.stars {
            #expect(Array(bytes(settled)[below...]) == Array(bytes(bare)[below...]), "band \(f.cloudBand ?? -1): the value, sub-line and gauge at rest")
        }
        for p in cinderPixels where p.face == f && !(p.x == 279 && p.y == 136) {
            #expect(near(pixel(settled, p.x, p.y), p.rgb, 12), "band \(f.cloudBand ?? -1) \(f.hero) at \(p.x),\(p.y)")
        }
    }
    let sun = try #require(ImageRenderer(content: KnobFaceView(.weather(sky(1)), t: KnobArrive.subRestS, drift: 0).frame(width: 466, height: 466)
        .environment(\.colorScheme, .dark)).cgImage)
    #expect(pixel(sun, 279, 136) == black && near(pixel(sun, 289, 136), RGB(r: 224, g: 236, b: 240), 12), "the cloud sits at its rest place")
}
