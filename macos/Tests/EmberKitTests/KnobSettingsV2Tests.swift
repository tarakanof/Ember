import Testing
import Foundation
import ImageIO
import SwiftUI
@testable import EmberKit

private func golden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/devices/\(name).json")
    return try Data(contentsOf: url)
}

private struct ConfigEnvelope: Decodable {
    let configVersion: Int
    let config: KnobSettings
    enum CodingKeys: String, CodingKey {
        case configVersion = "config_version"
        case config
    }
}

private let base = #"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true}],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}"#

private func settings(_ extra: String = "") throws -> KnobSettings {
    try JSONDecoder().decode(KnobSettings.self, from: Data((base + extra + "}").utf8))
}

private func knob(features: [String]) -> KnobDevice {
    KnobDevice(id: "knob-61fc8c", hwID: "a0b1c261fc8c", name: "Knob", createdAt: Date(timeIntervalSince1970: 0),
               effectiveCaps: KnobCaps(pages: ["bot", "pomodoro", "weather", "nowplaying"], features: features))
}

@Test func knobConfigV2GoldenDecodesEveryKey() throws {
    for name in ["config_v2", "checkin_reply_config_v2"] {
        let s = try JSONDecoder().decode(ConfigEnvelope.self, from: try golden(name)).config
        #expect(s.idle == .init(dimAfterS: 86399, dimLevel: 255, offAfterS: 86400))
        #expect(s.apps?.pomodoro == .init(focusColor: "#FFFFFF", breakColor: "#FFFFFF"))
        #expect(s.apps?.weather?.faces == ["temp", "rain", "cast", "wind", "days", "sun", "air"])
        #expect(s.apps?.nowplaying?.dimAfterS == 1800)
        #expect(s.patch(from: s).isEmpty)
    }
}

@Test func knobConfigWithoutV2KeysRoundTripsWithoutThem() throws {
    for name in ["config_default", "config_custom"] {
        let s = try JSONDecoder().decode(ConfigEnvelope.self, from: try golden(name)).config
        #expect(s.idle == nil && s.apps == nil)
        let again = try JSONEncoder().encode(s)
        let text = String(decoding: again, as: UTF8.self)
        #expect(!text.contains("idle") && !text.contains("apps"))
        #expect(try JSONDecoder().decode(KnobSettings.self, from: again) == s)
    }
}

@Test func knobV2KeysRoundTrip() throws {
    let s = try settings(##","idle":{"dim_after_s":600,"dim_level":40,"off_after_s":0},"apps":{"pomodoro":{"focus_color":"#FF6A3D"},"weather":{"faces":["rain","temp"]},"nowplaying":{"dim_after_s":300}}"##)
    #expect(try JSONDecoder().decode(KnobSettings.self, from: JSONEncoder().encode(s)) == s)
}

@Test func knobV2RowsNeedTheirOwnTokens() {
    let none = knob(features: [])
    let all = knob(features: [KnobCaps.idleDim, KnobCaps.pomoColors, KnobCaps.wxFaceList, KnobCaps.npDim])
    for token in [KnobCaps.idleDim, KnobCaps.pomoColors, KnobCaps.wxFaceList, KnobCaps.npDim] {
        #expect(!none.supports(feature: token))
        #expect(all.supports(feature: token))
        #expect(!knob(features: [KnobCaps.wxMoments]).supports(feature: token))
    }
    var legacy = KnobDevice(id: "knob-1", hwID: "a0b1c2000001", name: "Old", createdAt: Date(timeIntervalSince1970: 0))
    legacy.lastCheckin = KnobCheckin(seenAt: Date(timeIntervalSince1970: 0), fw: "0.9.99", ip: "192.0.2.10", rssi: -58,
                                     heapInternalFree: 0, heapInternalLargest: 0, uptimeS: 0, appliedVersion: 1)
    #expect(![KnobCaps.idleDim, KnobCaps.pomoColors, KnobCaps.wxFaceList, KnobCaps.npDim].contains { legacy.supports(feature: $0) })
}

@Test func knobPatchCarriesOnlyTheChangedIdleField() throws {
    let old = try settings()
    var new = old
    new.editIdle { $0.dimLevel = 40 }
    #expect(new.patch(from: old) == ["idle": .object(["dim_level": .int(40)])])

    let withIdle = try settings(#","idle":{"dim_after_s":600,"dim_level":40}"#)
    var next = withIdle
    next.editIdle { $0.offAfterS = 1800 }
    #expect(next.patch(from: withIdle) == ["idle": .object(["off_after_s": .int(1800)])])
}

@Test func knobPatchCarriesOnlyTheChangedAppField() throws {
    let old = try settings(##","apps":{"pomodoro":{"focus_color":"#FF6A3D","break_color":"#4FA9FF"},"nowplaying":{"dim_after_s":300}}"##)
    var new = old
    new.editPomodoro { $0.breakColor = "#112233" }
    #expect(new.patch(from: old) == ["apps": .object(["pomodoro": .object(["break_color": .string("#112233")])])])

    var np = old
    np.setNowPlayingDim(600)
    #expect(np.patch(from: old) == ["apps": .object(["nowplaying": .object(["dim_after_s": .int(600)])])])

    var faces = old
    faces.moveWeatherFace("rain", by: -1)
    #expect(faces.patch(from: old) == ["apps": .object(["weather": .object(["faces": .array(
        ["rain", "temp", "cast", "wind", "days", "sun", "air"].map(JSONValue.string))])])])
}

@Test func knobSameAsClockClearsTheColourKeysWithNull() throws {
    let old = try settings(##","apps":{"pomodoro":{"focus_color":"#FF6A3D","break_color":"#4FA9FF"}}"##)
    var one = old
    one.editPomodoro { $0.focusColor = nil }
    #expect(one.normalized().patch(from: old) == ["apps": .object(["pomodoro": .object(["focus_color": .null])])])

    var both = old
    both.editPomodoro { $0.focusColor = nil; $0.breakColor = nil }
    #expect(both.normalized().patch(from: old) == ["apps": .object(["pomodoro": .null])])
}

@Test func knobWeatherFacesKeepAtLeastOneOnAndFollowTheList() throws {
    var s = try settings()
    #expect(s.weatherFaceRows.map { $0.id } == KnobSettings.weatherFaceIDs)
    #expect(s.weatherFaceRows.allSatisfy { $0.on })
    for id in KnobSettings.weatherFaceIDs.dropLast() { s.setWeatherFace(id, on: false) }
    #expect(s.apps?.weather?.faces == ["air"])
    s.setWeatherFace("air", on: false)
    #expect(s.apps?.weather?.faces == ["air"])
    s.setWeatherFace("sun", on: true)
    #expect(s.weatherFaceRows.filter { $0.on }.map { $0.id } == ["air", "sun"])
    s.moveWeatherFace("sun", by: -1)
    #expect(s.apps?.weather?.faces == ["sun", "air"])
}

@Test func knobIdleChoicesNeverCrossAndNormalizeFixesAConflict() throws {
    #expect(KnobSettings.idleDimAfterChoices(off: 1800, current: 300) == [0, 60, 300, 600])
    #expect(KnobSettings.idleOffAfterChoices(dim: 1800, current: 0) == [0, 3600, 7200, 14400])
    #expect(KnobSettings.idleOffAfterChoices(dim: nil, current: 0) == [0] + KnobSettings.idleOffAfterSteps)
    var s = try settings(#","idle":{"dim_after_s":3600,"off_after_s":1800}"#)
    s = s.normalized()
    #expect(s.idle == .init(dimAfterS: 3600))
}

@Test func knobPreviewThemeFollowsTheSettingsOnlyWithTheTokens() throws {
    let s = try settings(##","apps":{"pomodoro":{"focus_color":"#010203","break_color":"#040506"},"weather":{"faces":["rain","wind"]}}"##)
    let plain = KnobTheme.standard
    let off = s.previewTheme(for: knob(features: []))
    #expect(off.pomodoro.colors == plain.pomodoro.colors)
    #expect(off.weather.dots == plain.weather.dots)

    let on = s.previewTheme(for: knob(features: [KnobCaps.pomoColors, KnobCaps.wxFaceList]))
    #expect(on.pomodoro.colors.focus == RGB(r: 1, g: 2, b: 3))
    #expect(on.pomodoro.colors.break == RGB(r: 4, g: 5, b: 6))
    #expect(on.weather.dots.listed == [.rain, .wind])
    let (dots, lit) = on.weather.dots.placement(of: .wind)
    #expect(dots.faces == 2 && lit == 1)
    #expect(on.weather.dots.placement(of: .temp).lit == nil)
    #expect(plain.weather.dots.placement(of: .wind).lit == KnobWeatherFaceID.wind.dot)
}

@MainActor
private func render(_ f: KnobFace, theme: KnobTheme, name: String) throws -> CGImage {
    let r = ImageRenderer(content: KnobFaceView(f, theme: theme).frame(width: 466, height: 466).environment(\.colorScheme, .dark))
    r.scale = 1
    let img = try #require(r.cgImage)
    if let dir = ProcessInfo.processInfo.environment["KNOB_SNAPSHOT_DIR"],
       let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: dir).appendingPathComponent("\(name).png") as CFURL,
                                                  "public.png" as CFString, 1, nil) {
        CGImageDestinationAddImage(dest, img, nil)
        CGImageDestinationFinalize(dest)
    }
    return img
}

private func pixel(_ img: CGImage, _ p: CGPoint) -> RGB {
    var buf = [UInt8](repeating: 0, count: 4)
    let ctx = CGContext(data: &buf, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    let (x, y) = (Int(p.x.rounded()), Int(p.y.rounded()))
    ctx.draw(img, in: CGRect(x: -x, y: y - img.height + 1, width: img.width, height: img.height))
    return RGB(r: buf[0], g: buf[1], b: buf[2])
}

private func near(_ a: RGB, _ b: RGB, _ tol: Int = 40) -> Bool {
    abs(Int(a.r) - Int(b.r)) <= tol && abs(Int(a.g) - Int(b.g)) <= tol && abs(Int(a.b) - Int(b.b)) <= tol
}

@MainActor
@Test func knobPomodoroPreviewDrawsTheChosenFocusColour() throws {
    let s = try settings(##","apps":{"pomodoro":{"focus_color":"#00FF00"}}"##)
    let state = PomoState(phase: "focus", running: true, paused: false, remainingSec: 750, plannedSec: 1500, round: 1)
    let at = Date(timeIntervalSince1970: 0)
    let top = CGPoint(x: 233, y: 233 - KnobTheme.standard.pomodoro.ringRadiusPx)

    let plainTheme = KnobTheme.standard
    let plain = try render(.pomodoro(KnobPomoFace(state: state, fetchedAt: at, now: at, theme: plainTheme.pomodoro)),
                           theme: plainTheme, name: "pomodoro-firmware-colours")
    #expect(near(pixel(plain, top), plainTheme.pomodoro.colors.focus))

    let theme = s.previewTheme(for: knob(features: [KnobCaps.pomoColors]))
    let custom = try render(.pomodoro(KnobPomoFace(state: state, fetchedAt: at, now: at, theme: theme.pomodoro)),
                            theme: theme, name: "pomodoro-custom-focus")
    #expect(near(pixel(custom, top), RGB(r: 0, g: 255, b: 0)))

    let gated = s.previewTheme(for: knob(features: []))
    #expect(gated.pomodoro.colors.focus == plainTheme.pomodoro.colors.focus)
}

@MainActor
@Test func knobWeatherPreviewDotsFollowTheFaceList() throws {
    let s = try settings(##","apps":{"weather":{"faces":["wind","rain","temp"]}}"##)
    let theme = s.previewTheme(for: knob(features: [KnobCaps.wxFaceList]))
    let dots = theme.weather.dots
    let face = KnobFace.rain(KnobRainFace(dry: true, intensity: 1, pop: 40, sub: "Dry now"))
    let img = try render(face, theme: theme, name: "weather-dots-wind-rain-temp")
    let (placed, lit) = dots.placement(of: .rain)
    #expect(placed.faces == 3 && lit == 1)
    for k in 0..<3 {
        let want = k == 1 ? dots.onColor : dots.offColor
        #expect(near(pixel(img, KnobTempFace.dotPoint(k, placed, center: 233)), want), "dot \(k)")
    }
    let stock = try render(face, theme: .standard, name: "weather-dots-all-seven")
    let (all, stockLit) = KnobTheme.standard.weather.dots.placement(of: .rain)
    #expect(all.faces == 7 && stockLit == 1)
    #expect(near(pixel(stock, KnobTempFace.dotPoint(1, all, center: 233)), dots.onColor))
}

@MainActor
@Test func knobWeatherPreviewDrawsNoDotsForAOneFaceList() throws {
    let s = try settings(##","apps":{"weather":{"faces":["rain"]}}"##)
    let theme = s.previewTheme(for: knob(features: [KnobCaps.wxFaceList]))
    let dots = theme.weather.dots
    let face = KnobFace.rain(KnobRainFace(dry: true, intensity: 1, pop: 40, sub: "Dry now"))
    let img = try render(face, theme: theme, name: "weather-dots-one-face")
    let (placed, lit) = dots.placement(of: .rain)
    #expect(placed.faces == 1 && lit == 0)
    let p = pixel(img, KnobTempFace.dotPoint(0, placed, center: 233))
    #expect(!near(p, dots.onColor) && !near(p, dots.offColor))
}

@Test func knobWeatherMoveIsOnlyOfferedWithinTheOnFaces() throws {
    var s = try settings(##","apps":{"weather":{"faces":["temp","rain"]}}"##)
    #expect(!s.canMoveWeatherFace("temp", by: -1))
    #expect(s.canMoveWeatherFace("temp", by: 1))
    #expect(s.canMoveWeatherFace("rain", by: -1))
    #expect(!s.canMoveWeatherFace("rain", by: 1))
    for off in ["cast", "wind", "days", "sun", "air"] {
        #expect(!s.canMoveWeatherFace(off, by: -1) && !s.canMoveWeatherFace(off, by: 1))
    }
    let before = s
    s.moveWeatherFace("rain", by: 1)
    s.moveWeatherFace("cast", by: -1)
    #expect(s == before)
}

@Test func knobPatchNeverEmitsNullForPreV2Keys() throws {
    let full = ##"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true}],"home":"bot","swipe_pages":true,"poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20,"source_label":true,"working_ring":true,"weather_moments":true,"weather_every_min":30},"diagnostics":"basic","stats_interval_s":60,"live_interval_s":5,"display":{"fast_link":true,"rotation":0},"quiet":{"calm":true,"dim_level":20}}"##
    let old = try JSONDecoder().decode(KnobSettings.self, from: Data(full.utf8))
    var new = old
    new.swipePages = nil
    new.bot.sourceLabel = nil; new.bot.weatherEveryMin = nil
    new.statsIntervalS = nil
    new.display = nil
    new.quiet = nil
    func hasNull(_ v: JSONValue) -> Bool {
        switch v {
        case .null: true
        case .object(let o): o.values.contains(where: hasNull)
        case .array(let a): a.contains(where: hasNull)
        default: false
        }
    }
    #expect(!hasNull(.object(new.patch(from: old))))
    #expect(old.patch(from: old).isEmpty)
}
