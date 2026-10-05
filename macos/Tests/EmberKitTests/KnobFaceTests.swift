import Foundation
import SwiftUI
import Testing
@testable import EmberKit

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
    let dash = KnobBotShape.eyes(p, scale: 1.15, geometry: KnobTheme.standard.bot.eyes)
    #expect(dash.count == 2 && dash.allSatisfy { $0.points.count == 2 })
    #expect(dash[0].points[0].x < dash[1].points[0].x)
    p.eyes = .happy
    #expect(KnobBotShape.eyes(p, scale: 1.15, geometry: KnobTheme.standard.bot.eyes).allSatisfy { $0.points.count == 7 })
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
    let mac = BotBehavior.Tuning.mac
    #expect(mac == BotBehavior.Tuning(sleepAfter: 300, hopLength: 0.62, hopSquash: 1, hopIntervalMedian: 15,
                                      hopIntervalSigma: 0.4, hopIntervalRange: 8...40))
    // Golden poses from BotBehavior on main before Tuning existed (seed 3, waiting, 30 fps).
    let golden: [(Int, Double, Double, Double, Double, Double, Double)] = [
        (18, 0.013622231572764792, 0.063514585858115064, 0, 1, 1, 0.0019054375757434519),
        (25, 0.013622231572764792, 0.063514585858115064, 0, 0.96296296296296302, 1.052910052910053, 0.19172711110112076),
        (300, -0.012163484960656182, 0.086968916976464528, 0, 1, 1, 0.0026090675092939357),
        (457, 0.0097419153423897939, 0.087008303393338038, 0, 1, 1, 0.002610249101800141),
        (900, 0.038895177665241129, 0.091894506513728602, 0, 1, 1, 0.0027568351954118582),
        (1199, -0.69999999999999996, -0.085259932788898318, 0, 1, 1, -0.0025577979836669497),
    ]
    var b = BotBehavior(seed: 3, now: 0)
    b.setMood(.waiting, at: 0)
    var poses: [Int: BotPose] = [:]
    for i in 0..<1200 { poses[i] = b.pose(at: Double(i) / 30) }
    for g in golden {
        let p = poses[g.0]!
        #expect([p.gazeX, p.gazeY, p.lidLeft, p.scaleX, p.scaleY, p.offsetY] == [g.1, g.2, g.3, g.4, g.5, g.6], "frame \(g.0)")
    }
}

@Test func knobRimIgnoresThePopAndFollowsTheSquash() {
    let b = KnobTheme.standard.bot
    let v = KnobBotShape.rimVariants(steps: b.rimSteps, squash: b.hop.squash)
    #expect(v.count == 11 && v[5] == (1, 1))
    var p = BotPose()
    p.scaleX = 1.08; p.scaleY = 1.08
    #expect(KnobBotShape.rimVariant(for: p, variants: v) == 5)
    p.scaleX = 1 + 0.1 * 0.75; p.scaleY = 1 - 0.14 * 0.75
    #expect(KnobBotShape.rimVariant(for: p, variants: v) == 0)
    p.scaleX = 1 - 0.07 * 0.75; p.scaleY = 1 + 0.1 * 0.75
    #expect(KnobBotShape.rimVariant(for: p, variants: v) == 10)
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
    #expect(KnobMood(sessions: [s("dt-mbp", "running"), s("mini", "waiting")])
        == KnobMood(mood: .waiting, host: "MINI", tool: "claude"))
    // Several hosts: the one with the most sessions leads (ties to the smaller name), + the others.
    #expect(KnobMood(sessions: [s("a", "error"), s("b", "error", 5)]).host == "A +1")
    #expect(KnobMood(sessions: [s("mini", "running", 9), s("m4", "running"), s("m4", "running", 1)]).host == "M4 +1")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting")]).host == "VERY-LONG-")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting"), s("b", "waiting")]).host == "B +1")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting"), s("very-long-hostname", "waiting", 1),
                                s("b", "waiting")]).host == "VERY-LO +1")
    #expect(KnobMood(sessions: [s("", "running")]).host == "")
    #expect(KnobMood(mood: .working, host: "X").showsHost)
    #expect(KnobMood(mood: .error, host: "X").showsHost)
    #expect(KnobMood(mood: .done, host: "X").showsHost == false)
}

@Test func knobMoodCarriesTheLeadColourAndTool() throws {
    func s(_ src: String, _ tool: String, _ color: String?, _ session: String) throws -> Session {
        let c = color.map { #""source_color":"\#($0)","# } ?? ""
        return try JSONDecoder.iso.decode(Session.self, from: Data("""
        {"source":"\(src)","tool":"\(tool)","session":"\(session)","state":"running","message":"",\(c)
         "tokens_today":0,"activity":"","context_number":false,"rate_bottom_bar":false,"rate_reset_at":0,
         "rate_reset":false,"updated_at":"2026-10-05T12:00:00Z"}
        """.utf8))
    }
    let m = KnobMood(sessions: [try s("m4", "claude", nil, "a"), try s("m4", "claude", "#b48cff", "b")])
    #expect(m.host == "M4" && m.hostColor == RGB(r: 0xB4, g: 0x8C, b: 0xFF) && m.tool == "claude")
    let mixed = KnobMood(sessions: [try s("m4", "claude", "bad", "a"), try s("m4", "codex", nil, "b")])
    #expect(mixed.hostColor == nil && mixed.tool == "")
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
    #expect(near(pixel(waiting, 233, 233 + 40), .init(r: 0, g: 0, b: 0), 8))
    // No glow: a few pixels outside the 6 px rim stays black.
    #expect(near(pixel(waiting, 233, Int(233 - r) - 7), .init(r: 0, g: 0, b: 0), 8))

    let error = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.error),
                                                     mood: KnobMood(mood: .error, host: "MINI")))))
    save(error, "bot-error")
    #expect(near(pixel(error, 233, Int(233 - r * 1.1) + 3), th.moodColors.error, 90))

    // Working: the glint over the ring at its head, the curved label in the host colour.
    let purple = RGB(r: 0xB4, g: 0x8C, b: 0xFF)
    let working = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.working),
                                                       mood: KnobMood(mood: .working, host: "M4 +1", hostColor: purple,
                                                                      tool: "claude"),
                                                       glint: -90))))
    save(working, "bot-working-glint")
    let glint = RGB(r: 0xC0, g: 0xF8, b: 0xCF)
    #expect(near(pixel(working, 233, Int(233 - r)), glint, 40), "glint head at 12 o'clock")
    #expect(near(pixel(working, Int(233 + r), 233), th.moodColors.working, 60), "plain ring at 3 o'clock")
    let labelPixels = (380..<410).flatMap { y in (190..<280).map { x in pixel(working, x, y) } }
    #expect(labelPixels.contains { near($0, purple, 40) }, "label in the host colour near the bottom")

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
        if look.face == .clearDay {
            #expect(near(pixel(img, 233, 96 + 70 - 13), th.weather.colors.sun, 60))
            let rows = (236..<300).filter { y in (200..<266).contains { x in pixel(img, x, y).r > 60 } }
            let mid = Double(rows.first! + rows.last!) / 2
            #expect(abs(mid - 262) <= 2, "temperature digits centred at \(mid), firmware ~262")
        }
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
