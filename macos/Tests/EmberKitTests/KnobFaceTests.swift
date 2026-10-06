import Foundation
import SwiftUI
import Testing
@testable import EmberKit

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
    #expect(KnobMood(sessions: [s("a", "error"), s("b", "error", 5)]).host == "A +1")
    #expect(KnobMood(sessions: [s("mini", "running", 9), s("m4", "running"), s("m4", "running", 1)]).host == "M4 +1")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting")]).host == "VERY-LONG-")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting"), s("b", "waiting")]).host == "B +1")
    #expect(KnobMood(sessions: [s("very-long-hostname", "waiting"), s("very-long-hostname", "waiting", 1),
                                s("b", "waiting")]).host == "VERY-LO +1")
    #expect(KnobMood(sessions: [s("", "running")]).host == "")
    #expect(KnobMood(sessions: [s("m4", "running"), s("M4", "running", 1)]).host == "M4", "hosts compare uppercased")
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
    #expect(near(pixel(waiting, 233, Int(233 - r) - 7), .init(r: 0, g: 0, b: 0), 8))

    let error = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.error),
                                                     mood: KnobMood(mood: .error, host: "MINI")))))
    save(error, "bot-error")
    #expect(near(pixel(error, 233, Int(233 - r * 1.1) + 3), th.moodColors.error, 90))

    let purple = RGB(r: 0xB4, g: 0x8C, b: 0xFF)
    let working = try #require(render(KnobFaceView(.bot(pose: KnobBotDriver.restingPose(.working),
                                                       mood: KnobMood(mood: .working, host: "M4 +1", hostColor: purple,
                                                                      tool: "claude"),
                                                       glint: -90))))
    save(working, "bot-working-glint")
    let glint = RGB(r: 0xE0, g: 0xFC, b: 0xE7)
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

private func solid(_ c: RGB, _ side: Int) -> CGImage {
    let ctx = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.setFillColor(red: Double(c.r) / 255, green: Double(c.g) / 255, blue: Double(c.b) / 255, alpha: 1)
    ctx.fill(CGRect(x: 0, y: 0, width: side, height: side))
    return ctx.makeImage()!
}

private func np(_ state: String = "playing", pos: Int64 = 62_000, dur: Int64 = 190_000, at: Int64 = 0,
                title: String? = "Paranoid Android", artist: String? = "Radiohead", album: String? = "OK Computer",
                source: String = "music", art: Bool = true) -> NowPlayingState {
    let json: [String: Any?] = ["state": state, "source": source, "title": title, "artist": artist, "album": album,
                                "duration_ms": dur, "position_ms": pos, "position_at": at, "art_version": art ? "abc123" : nil,
                                "has_album_art": art, "has_artist_art": art]
    let data = try! JSONSerialization.data(withJSONObject: json.compactMapValues { $0 })
    return try! JSONDecoder().decode(NowPlayingState.self, from: data)
}

@Test func knobNowPlayingFaceMatchesNowplayingView() {
    let t0 = Date(timeIntervalSince1970: 1000)
    let at = Int64(1000 * 1000)
    let playing = KnobNowPlayingFace(state: np(pos: 62_000, at: at), now: t0.addingTimeInterval(3.6))
    #expect(playing.mode == .playing)
    #expect(playing.title == "Paranoid Android" && playing.sub == "Radiohead - OK Computer")
    #expect(playing.meta == "APPLE MUSIC  1:05 / 3:10")
    #expect(abs(playing.fraction - 65.0 / 190) < 1e-9)
    let paused = KnobNowPlayingFace(state: np("paused", at: at), now: t0.addingTimeInterval(30))
    #expect(paused.mode == .paused && paused.meta == "PAUSED  1:02 / 3:10")
    #expect(KnobNowPlayingFace(state: np(pos: 5_000, dur: 0, at: at, source: "plex"), now: t0).meta == "PLEX  0:05")
    #expect(KnobNowPlayingFace(state: np(pos: 180_000, at: at), now: t0.addingTimeInterval(60)).fraction == 1)
    #expect(KnobNowPlayingFace.time(3_725_000) == "1:02:05")
    let bare = KnobNowPlayingFace(state: np(at: at, title: nil, artist: "Solo", album: nil), now: t0)
    #expect(bare.title == "Unknown track" && bare.sub == "Solo")
    #expect(KnobNowPlayingFace(state: np("none"), now: t0).idleLine == "Nothing playing")
    #expect(KnobNowPlayingFace(state: nil, now: t0).idleLine == "Waiting for Ember")
    #expect(KnobNowPlayingFace(state: np(), offline: true, now: t0).idleLine == "Ember offline")
    #expect(KnobNowPlayingFace.fold("Björk – “Hyperballad” Ænima Łódź") == "Bjork - \"Hyperballad\" AEnima Lodz")
}

@MainActor @Test func knobNowPlayingRendersToTheFirmwareLayout() throws {
    let th = KnobTheme.standard
    let c = 233
    let t0 = Date(timeIntervalSince1970: 1000)
    let at = Int64(1000 * 1000)
    let pics = KnobNowPlayingPictures(backdrop: .init(image: solid(.init(r: 0x30, g: 0x10, b: 0x10), 466), id: "b"),
                                      album: .init(image: solid(.init(r: 0x20, g: 0x80, b: 0x20), 240), id: "a"),
                                      artist: .init(image: solid(.init(r: 0x20, g: 0x20, b: 0xC0), 64), id: "r"))
    let playing = KnobNowPlayingFace(state: np(pos: 48_000, dur: 192_000, at: at), now: t0, pictures: pics)
    let img = try #require(render(KnobFaceView(.nowPlaying(playing))))
    save(img, "nowplaying-playing")
    #expect(near(pixel(img, c, c + th.nowplaying.albumDyPx.rounded().asInt), .init(r: 0x20, g: 0x80, b: 0x20), 6), "album centre")
    #expect(near(pixel(img, c + 198, c), .init(r: 0x20, g: 0x20, b: 0xC0), 6), "artist avatar on the ring at 25 %")
    #expect(near(pixel(img, c, c - 198), th.nowplaying.colors.arc, 30), "arc starts at 12 o'clock")
    #expect(near(pixel(img, c - 198, c), th.nowplaying.colors.track, 8), "unplayed track")
    #expect(near(pixel(img, c - 150, c), .init(r: 0x30, g: 0x10, b: 0x10), 6), "backdrop inside the disk")
    #expect(near(pixel(img, c - 192, c), .init(r: 0, g: 0, b: 0), 6), "black band outside the 376 px disk")
    let titleRows = ((c + 90)..<(c + 125)).filter { y in (170..<300).contains { x in pixel(img, x, y).r > 200 } }
    #expect(!titleRows.isEmpty, "title drawn at +96")

    let pausedFace = KnobNowPlayingFace(state: np("paused", pos: 48_000, dur: 192_000, at: at, art: false), now: t0)
    let paused = try #require(render(KnobFaceView(.nowPlaying(pausedFace))))
    save(paused, "nowplaying-paused")
    #expect(near(pixel(paused, c, c - 198), th.nowplaying.colors.arcPaused, 30))
    #expect(near(pixel(paused, c + 198, c), th.nowplaying.colors.arcPaused, 30), "dot at the progress point")
    #expect(near(pixel(paused, c + 100, c - 44), th.nowplaying.colors.placeholder, 6), "placeholder disc")

    let idle = try #require(render(KnobFaceView(.nowPlaying(KnobNowPlayingFace(state: np("none"), now: t0)))))
    save(idle, "nowplaying-idle")
    #expect(near(pixel(idle, c, c - 150), .init(r: 0, g: 0, b: 0), 2))
    #expect((200..<270).contains { x in (c - 14..<c + 14).contains { y in pixel(idle, x, y).r > 0x40 } }, "grey line")

    let long = KnobFaceRender.fit(String(repeating: "Wide Title ", count: 12), 24, width: 290, theme: th)
    #expect(long.hasSuffix("...") && long.count < 40)
    #expect(KnobFaceRender.fit("Short", 24, width: 290, theme: th) == "Short")
}

private func pngData(_ side: Int) -> Data {
    let data = NSMutableData()
    let d = CGImageDestinationCreateWithData(data, "public.png" as CFString, 1, nil)!
    CGImageDestinationAddImage(d, solid(.init(r: 1, g: 2, b: 3), side), nil)
    CGImageDestinationFinalize(d)
    return data as Data
}

private extension Double { var asInt: Int { Int(self) } }

@MainActor @Test func knobNowPlayingFeedLoadsPicturesOnceAndRetriesFailures() async {
    final class Calls: @unchecked Sendable {
        var art: [String] = []; var failArt = false; var fail = false; var state = np(); var serverNow: Date?
        var now = Date(timeIntervalSince1970: 5000)
    }
    let calls = Calls()
    let feed = KnobNowPlayingFeed(
        fetchState: { if calls.fail { throw URLError(.cannotConnectToHost) }; return (calls.state, calls.serverNow) },
        fetchArt: { kind, size, v in
            calls.art.append("\(kind)/\(size)/\(v)")
            if calls.failArt { throw APIError.http(status: 404, body: "") }
            return pngData(size)
        },
        now: { calls.now })
    calls.failArt = true
    await feed.refresh()
    #expect(calls.art == ["backdrop/466/abc123"] && feed.artError != nil && feed.pictures.backdrop == nil)
    await feed.refresh()
    #expect(calls.art.count == 1, "no retry inside the back-off")
    calls.now.addTimeInterval(5.1)
    calls.failArt = false
    await feed.refresh()
    #expect(feed.artError == nil && feed.pictures.backdrop != nil && feed.pictures.album != nil && feed.pictures.artist != nil)
    await feed.refresh()
    #expect(calls.art.suffix(3) == ["backdrop/466/abc123", "album/240/abc123", "artist/64/abc123"] && calls.art.count == 4,
            "each picture fetched once per art_version")
    calls.fail = true
    await feed.refresh(); await feed.refresh()
    #expect(!feed.failed && feed.state != nil)
    await feed.refresh()
    #expect(feed.failed)
    calls.fail = false
    calls.state = np("none", art: false)
    await feed.refresh()
    #expect(!feed.failed && feed.pictures == .init())
    calls.serverNow = calls.now.addingTimeInterval(0.4)
    await feed.refresh()
    #expect(feed.serverOffset == 0)
    calls.serverNow = calls.now.addingTimeInterval(-30)
    await feed.refresh()
    #expect(abs(feed.serverOffset + 29.5) < 0.01)
}

@MainActor @Test func knobNowPlayingThumbnailSkipsTheBackdrop() async {
    final class Calls: @unchecked Sendable { var art: [String] = [] }
    let calls = Calls()
    let feed = KnobNowPlayingFeed(thumbnail: true, fetchState: { (np(), nil) },
                                  fetchArt: { k, s, _ in calls.art.append("\(k)/\(s)"); return pngData(s) })
    await feed.refresh()
    #expect(calls.art == ["album/120", "artist/64"] && feed.pictures.backdrop == nil && feed.pictures.album != nil)
}

@Test func rainSpriteSlantsByTheThemeSlant() throws {
    let slant = try KnobTheme.load().weather.scene.rain.slant
    let end = try #require(KnobWeatherScene.shape(.rain, frame: 0).strokes.first?.last)
    #expect(abs(end.x - (6 + 14 * slant[0] / slant[1])) < 1e-9)
}
