import Foundation
import SwiftUI
import Testing
@testable import EmberKit

// MARK: Theme (drift guard: these are cinder's firmware constants)

@Test func knobThemePinsFirmwareConstants() throws {
    let t = try KnobTheme.load()
    #expect(t.version == 1)
    #expect(t.screen.diameterPx == 466)
    // bot_view.c
    #expect(t.bot.fill == 0.84 && t.bot.hopScale == 0.45 && t.bot.eyeScale == 1.15 && t.bot.rimPx == 6)
    #expect(t.bot.rimDimGain == 0.55)
    #expect(t.bot.eyeColor == RGB(hex: "#F4F4F2"))
    #expect(t.bot.host.fontPx == 24 && t.bot.host.y == 0.8 && t.bot.host.maxChars == 10)
    // bot_shape.c / bot_shape.h
    #expect(t.bot.ringPoints == 192)
    #expect(t.bot.triangle.radius == 1.1 && t.bot.triangle.sagitta == 0.11 && t.bot.triangle.tableBins == 720)
    // bot_behavior.h / .c
    #expect(t.bot.hop.durationS == 1.0 && t.bot.hop.squash == 0.75)
    #expect(t.bot.hop.intervalMedianS == 4.5 && t.bot.hop.intervalMinS == 2.5 && t.bot.hop.intervalMaxS == 9)
    // Ember's stateColorRGB, mood_rgb()
    #expect(t.moodColors.waiting == RGB(hex: "#FFC14D"))
    #expect(t.moodColors.error == RGB(hex: "#FF3A3A"))
    #expect(t.moodColors.working == RGB(hex: "#2EE85E"))
    #expect(t.moodColors.done == RGB(hex: "#4FA9FF"))
    #expect(t.moodColors.idle == RGB(hex: "#888888"))
    // pomo_view.c
    #expect(t.pomodoro.ringRadiusPx == 222 && t.pomodoro.ringWidthPx == 12)
    #expect(t.pomodoro.trackGain == 0.18 && t.pomodoro.pausedGain == 0.45)
    #expect(t.pomodoro.colors.focus == RGB(hex: "#FF6A3D") && t.pomodoro.colors.break == RGB(hex: "#4FA9FF"))
    #expect(t.pomodoro.time.fontPx == 48 && t.pomodoro.phase.fontPx == 24 && t.pomodoro.round.fontPx == 14)
    // weather_view.c / weather_scene.h
    #expect(t.weather.sky.widthPx == 200 && t.weather.sky.heightPx == 140 && t.weather.sky.yPx == 96)
    #expect(t.weather.temp.fontPx == 48 && t.weather.maxAgeS == 1800)
    #expect(t.weather.colors.rain == RGB(hex: "#5C9CE0") && t.weather.colors.sun == RGB(hex: "#E0A030"))
}

@Test func knobThemeRejectsMissingKeysAndBadColours() {
    #expect(throws: (any Error).self) { try KnobTheme.decode(Data(#"{"version":1}"#.utf8)) }
    #expect(throws: (any Error).self) { try JSONDecoder().decode([RGB].self, from: Data(#"["red"]"#.utf8)) }
    #expect((try? JSONDecoder().decode([RGB].self, from: Data(##"["#0A0B0C"]"##.utf8))) == [RGB(r: 10, g: 11, b: 12)])
}

// MARK: Bot

@Test func knobTriangleHasSharpVerticesAtTheCircumradius() {
    let shape = KnobBotShape(theme: KnobTheme.standard.bot)
    let apex = shape.triangleRadius(.pi / 2)
    #expect(abs(apex - 1.1) < 0.03)
    let midSide = shape.triangleRadius(.pi / 2 + .pi / 3)
    #expect(midSide < 0.75)
    #expect(shape.ring(0).allSatisfy { abs(hypot($0.x, $0.y) - 1) < 1e-9 })
    #expect(shape.ring(1).count == 192)
}

@Test func knobEyesAreTwoStrokesAndHappyIsAnArc() {
    var p = BotPose()
    p.eyes = .dash
    let dash = KnobBotShape.eyes(p, scale: 1.15)
    #expect(dash.count == 2 && dash.allSatisfy { $0.points.count == 2 })
    #expect(dash[0].points[0].x < dash[1].points[0].x)
    p.eyes = .happy
    #expect(KnobBotShape.eyes(p, scale: 1.15).allSatisfy { $0.points.count == 7 })
}

@Test func knobTuningSlowsAndFlattensTheHop() {
    var b = BotBehavior(seed: 7, now: 0, tuning: KnobTheme.standard.botTuning)
    b.setMood(.waiting, at: 0)
    var minY = 1.0, lastHop = 0.0
    for i in 0..<(20 * 60) {
        let t = Double(i) / 60
        let p = b.pose(at: t)
        minY = min(minY, p.scaleY)
        if p.offsetY > 0.05 { lastHop = t }
    }
    #expect(minY > 1 - 0.14 * 0.75 - 0.01)
    #expect(minY < 0.95)
    #expect(lastHop > 10)
}

@Test func macTuningIsUnchanged() {
    var a = BotBehavior(seed: 3, now: 0)
    var b = BotBehavior(seed: 3, now: 0, tuning: .mac)
    a.setMood(.waiting, at: 0); b.setMood(.waiting, at: 0)
    for i in 0..<1200 { #expect(a.pose(at: Double(i) / 30) == b.pose(at: Double(i) / 30)) }
}

@Test func knobMoodFollowsRenderPriorityAndHost() {
    func s(_ src: String, _ state: String, _ at: Double = 0) -> Session {
        try! JSONDecoder.iso.decode(Session.self, from: Data("""
        {"source":"\(src)","tool":"claude","session":"\(src)\(state)","state":"\(state)","message":"",
         "tokens_today":0,"activity":"","context_number":false,"rate_bottom_bar":false,"rate_reset_at":0,
         "rate_reset":false,"updated_at":"\(ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: at)))"}
        """.utf8))
    }
    #expect(KnobMood(sessions: []) == KnobMood(mood: .idle))
    #expect(KnobMood(sessions: [s("dt-mbp", "running"), s("mini", "waiting")]) == KnobMood(mood: .waiting, host: "MINI"))
    #expect(KnobMood(sessions: [s("a", "error"), s("b", "error", 5)]).host == "")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting")]).host == "VERY-LONG-")
    #expect(KnobMood(mood: .working, host: "X").showsHost == false)
    #expect(KnobMood(mood: .error, host: "X").showsHost)
}

extension JSONDecoder {
    static var iso: JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }
}

// MARK: Pomodoro

private func pomo(_ phase: String, running: Bool = true, paused: Bool = false, rem: Int = 750,
                  planned: Int = 1500, round: Int = 2) -> PomoState {
    PomoState(phase: phase, running: running, paused: paused, remainingSec: rem, plannedSec: planned, round: round)
}

@Test func knobPomoFaceMatchesPomoView() {
    let th = KnobTheme.standard.pomodoro
    let at = Date(timeIntervalSince1970: 1000)
    let run = KnobPomoFace(state: pomo("focus"), fetchedAt: at, now: at.addingTimeInterval(10.5), theme: th)
    #expect(run.time == "12:20" && run.phase == "FOCUS" && run.round == "2 DONE" && run.mode == .running)
    #expect(run.arc == th.colors.focus && abs(run.fraction - 740.0 / 1500) < 1e-9)
    #expect(run.track == th.colors.focus.scaled(0.18))

    let paused = KnobPomoFace(state: pomo("short_break", running: false, paused: true), fetchedAt: at,
                              now: Date(timeIntervalSince1970: 1001), theme: th)
    #expect(paused.phase == "PAUSED" && paused.time == "12:30" && paused.arc == th.colors.break.scaled(0.45))
    #expect(paused.timeColor == th.colors.textDim)

    let parked = KnobPomoFace(state: pomo("focus", running: false, rem: 1500), fetchedAt: at, now: at, theme: th)
    #expect(parked.phase == "FOCUS NEXT" && parked.mode == .parked)

    let idle = KnobPomoFace(state: pomo("idle", running: false, rem: 0, planned: 0, round: 0), fetchedAt: at, now: at,
                            theme: th)
    #expect(idle.time == "--:--" && idle.phase == "PUSH TO START" && idle.arc == nil && idle.round == "")
    #expect(KnobPomoFace(state: nil, fetchedAt: nil, now: at, theme: th).phase == "OFFLINE")
}

// MARK: Weather

@Test func knobWeatherLookMapsCodes() {
    #expect(KnobWeatherLook(provider: "open-meteo", condition: "rain", code: "65", night: false)
            == .init(face: .rain, intensity: .heavy))
    #expect(KnobWeatherLook(provider: "open-meteo", condition: "clear", code: "0", night: true).face == .clearNight)
    #expect(KnobWeatherLook(provider: "open-meteo", condition: "fog", code: "48", night: nil).rime)
    #expect(KnobWeatherLook(provider: "met-no", condition: "clouds", code: "partlycloudy_night", night: nil)
            == .init(face: .partlyCloudy, night: true))
    #expect(KnobWeatherLook(provider: "met-no", condition: "snow", code: "lightsnowshowers_day", night: nil)
            == .init(face: .snow, intensity: .light))
    #expect(KnobWeatherLook(provider: "open-meteo", condition: "clouds", code: "1234", night: nil).face == .overcast)
    #expect(KnobWeatherLook.isNight(now: 23 * 60, rise: 6 * 60, set: 20 * 60))
    #expect(!KnobWeatherLook.isNight(now: 12 * 60, rise: 6 * 60, set: 20 * 60))
    #expect(KnobWeatherLook(state: nil, now: .now, maxAge: 1800) == .init(face: .overcast, still: true))
}

@Test func knobWeatherSceneIsDeterministicAndStaysInTheSky() {
    var a = KnobWeatherScene(), b = KnobWeatherScene()
    let look = KnobWeatherLook(face: .storm, intensity: .heavy)
    a.setLook(look); b.setLook(look)
    var flashed = false
    for _ in 0..<(12 * 20) {
        a.step(1.0 / 12); b.step(1.0 / 12)
        flashed = flashed || a.flash
        let d = a.draws(colors: KnobTheme.standard.weather.colors)
        #expect(d == b.draws(colors: KnobTheme.standard.weather.colors))
        for x in d where x.sprite == .rain {
            #expect(x.y >= 49 && x.y + 18 <= 141)
        }
    }
    #expect(flashed)
    #expect(KnobWeatherScene.period(.init(face: .clearDay, still: true)) == 0)
}

// MARK: Snapshots

@MainActor
private func render<V: View>(_ v: V, side: CGFloat = 466, dark: Bool = true) -> CGImage? {
    let r = ImageRenderer(content: v.frame(width: side, height: side)
        .environment(\.colorScheme, dark ? .dark : .light))
    r.scale = 1
    return r.cgImage
}

private func pixel(_ img: CGImage, _ x: Int, _ y: Int) -> RGB {
    var buf = [UInt8](repeating: 0, count: 4)
    let ctx = CGContext(data: &buf, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: -x, y: y - img.height + 1, width: img.width, height: img.height))
    return RGB(r: buf[0], g: buf[1], b: buf[2])
}

private func near(_ a: RGB, _ b: RGB, _ tol: Int = 24) -> Bool {
    abs(Int(a.r) - Int(b.r)) <= tol && abs(Int(a.g) - Int(b.g)) <= tol && abs(Int(a.b) - Int(b.b)) <= tol
}

private func save(_ img: CGImage, _ name: String) {
    guard let dir = ProcessInfo.processInfo.environment["KNOB_SNAPSHOT_DIR"] else { return }
    let url = URL(fileURLWithPath: dir).appendingPathComponent(name + ".png")
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil) else { return }
    CGImageDestinationAddImage(dest, img, nil)
    CGImageDestinationFinalize(dest)
}

@MainActor @Test func knobFacesRenderToTheFirmwareLayout() throws {
    let th = KnobTheme.standard
    let r = 233 * th.bot.fill

    let waiting = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.waiting),
                                                       mood: KnobMood(mood: .waiting, host: "DT-MBP")))))
    save(waiting, "bot-waiting")
    #expect(near(pixel(waiting, 233, Int(233 - r)), th.moodColors.waiting, 60))
    #expect(pixel(waiting, 233, 233 + 40) == RGB(r: 0, g: 0, b: 0) || near(pixel(waiting, 233, 233 + 40), .init(r: 0, g: 0, b: 0), 8))

    let error = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.error),
                                                     mood: KnobMood(mood: .error, host: "MINI")))))
    save(error, "bot-error")
    #expect(near(pixel(error, 233, Int(233 - r * 1.1) + 3), th.moodColors.error, 90))

    for mood in [BotMood.idle, .working, .done, .sleepy] {
        let img = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(mood), mood: KnobMood(mood: mood)))))
        save(img, "bot-\(mood.rawValue)")
    }

    let at = Date(timeIntervalSince1970: 0)
    let focus = KnobPomoFace(state: pomo("focus", rem: 900), fetchedAt: at, now: at, theme: th.pomodoro)
    let pimg = try #require(render(KnobFaceView(.pomodoro(focus))))
    save(pimg, "pomodoro-focus")
    #expect(near(pixel(pimg, 233 + 222, 233), th.pomodoro.colors.focus))
    #expect(near(pixel(pimg, 233 - 222, 233), th.pomodoro.colors.focus.scaled(0.18)))
    for (name, s) in [("pomodoro-paused", pomo("short_break", running: false, paused: true)),
                      ("pomodoro-parked", pomo("focus", running: false, rem: 1500)),
                      ("pomodoro-idle", pomo("idle", running: false, rem: 0, planned: 0, round: 0))] {
        let img = try #require(render(KnobFaceView(.pomodoro(KnobPomoFace(state: s, fetchedAt: at, now: at,
                                                                       theme: th.pomodoro)))))
        save(img, name)
    }

    for look in [KnobWeatherLook(face: .clearDay), .init(face: .clearNight), .init(face: .partlyCloudy),
                 .init(face: .overcast), .init(face: .fog), .init(face: .rain, intensity: .moderate),
                 .init(face: .snow, intensity: .heavy), .init(face: .storm, intensity: .heavy),
                 .init(face: .rain, intensity: .moderate, still: true)] {
        var scene = KnobWeatherScene()
        scene.setLook(look)
        for _ in 0..<30 { scene.step(0.1) }
        let img = try #require(render(KnobFaceView(.weather(look: look, draws: scene.draws(colors: th.weather.colors),
                                                            tempC: look.still ? nil : 14.4))))
        save(img, "weather-\(look.face.rawValue)\(look.still ? "-stale" : "")")
        if look.face == .clearDay { #expect(near(pixel(img, 233, 96 + 70 - 13), th.weather.colors.sun, 60)) }
    }

    for dark in [true, false] {
        let framed = try #require(render(KnobScreen { KnobFaceView(.pomodoro(focus)) }, side: 240, dark: dark))
        save(framed, "bezel-\(dark ? "dark" : "light")")
        let off = try #require(render(KnobScreen(on: false) {
            KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.idle), mood: KnobMood(mood: .idle)))
        }, side: 240, dark: dark))
        save(off, "bezel-off-\(dark ? "dark" : "light")")
        #expect(pixel(framed, 120, 120) != pixel(framed, 2, 120))
    }
}
