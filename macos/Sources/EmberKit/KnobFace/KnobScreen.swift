import SwiftUI

public struct KnobScreen<Face: View>: View {
    let on: Bool
    let face: Face
    @Environment(\.colorScheme) private var scheme

    public init(on: Bool = true, @ViewBuilder face: () -> Face) {
        self.on = on; self.face = face()
    }

    public var body: some View {
        GeometryReader { g in
            let side = min(g.size.width, g.size.height)
            let ring = max(3, side * 0.035)
            ZStack {
                Circle()
                    .fill(LinearGradient(colors: bezelColors, startPoint: .top, endPoint: .bottom))
                    .shadow(color: .black.opacity(scheme == .dark ? 0.5 : 0.25), radius: side * 0.025, y: side * 0.01)
                Circle()
                    .fill(.black)
                    .padding(ring)
                face
                    .opacity(on ? 1 : 0.35)
                    .padding(ring)
                Circle()
                    .inset(by: ring)
                    .fill(LinearGradient(colors: [.white.opacity(0.14), .white.opacity(0.02), .clear],
                                         startPoint: .topLeading, endPoint: .center))
                    .allowsHitTesting(false)
                Circle()
                    .inset(by: ring - 0.5)
                    .stroke(.black.opacity(0.6), lineWidth: 1)
            }
            .frame(width: side, height: side)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .aspectRatio(1, contentMode: .fit)
    }

    private var bezelColors: [Color] {
        scheme == .dark
            ? [Color(white: 0.42), Color(white: 0.16), Color(white: 0.28)]
            : [Color(white: 0.93), Color(white: 0.62), Color(white: 0.80)]
    }
}

public struct KnobBotLive: View {
    let mood: KnobMood
    let sleepAfter: Double
    let sourceLabel: Bool
    let workingRing: Bool
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double
    @State private var driver = KnobBotDriver()
    @State private var visible = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(mood: KnobMood, sleepAfter: Double, sourceLabel: Bool = true, workingRing: Bool = true,
                animated: Bool = true, theme: KnobTheme = .standard, brightness: Double = 1) {
        self.mood = mood; self.sleepAfter = sleepAfter; self.animated = animated; self.theme = theme
        self.brightness = brightness
        self.sourceLabel = sourceLabel; self.workingRing = workingRing
    }

    private var shown: KnobMood {
        var m = mood
        if !sourceLabel { m.host = "" }
        return m
    }

    private var glintOn: Bool { workingRing && mood.mood == .working }

    private func glint(at date: Date) -> Double? {
        guard glintOn else { return nil }
        let g = theme.bot.glint, step = 1 / g.fps
        let steps = (date.timeIntervalSinceReferenceDate / step).rounded(.down)
        let turns = steps * step / g.periodS
        return (turns - turns.rounded(.down)) * 360 - 90
    }

    public var body: some View {
        Group {
            if animated && visible {
                TimelineView(KnobBotSchedule(clock: driver.clock, mood: mood.mood,
                                             continuous: glintOn && !reduceMotion ? 1 / theme.bot.glint.fps : nil)) { tl in
                    KnobFaceView(.bot(pose: driver.pose(at: Date(), mood: mood.mood, config: config)
                                        .quantized(toPixels: theme.screen.diameterPx / 2 * theme.bot.fill),
                                      mood: shown, glint: reduceMotion ? (glintOn ? -40 : nil) : glint(at: tl.date)),
                                 theme: theme, brightness: brightness)
                    .equatable()
                }
            } else {
                KnobFaceView(.bot(pose: KnobBotDriver.restingPose(mood.mood, theme: theme), mood: shown,
                                  glint: glintOn ? -40 : nil),
                             theme: theme, brightness: brightness)
            }
        }
        .onAppear { visible = true }
        .onDisappear { visible = false }
    }

    private var config: KnobBotDriver.Config {
        var t = theme.botTuning
        t.sleepAfter = sleepAfter > 0 ? sleepAfter : .infinity
        return .init(tuning: t, reduceMotion: reduceMotion)
    }
}

struct KnobBotSchedule: TimelineSchedule {
    static let frame = 1.0 / 20
    let clock: KnobBotClock
    let mood: BotMood
    var continuous: Double? = nil

    func entries(from start: Date, mode: Mode) -> AnyIterator<Date> {
        var last = start
        var first = true
        return AnyIterator {
            if first { first = false; return start }
            if let continuous {
                let n = (last.timeIntervalSinceReferenceDate / continuous).rounded(.down) + 1
                last = Date(timeIntervalSinceReferenceDate: n * continuous)
                return last
            }
            let (moving, next) = clock.read()
            let step = last.addingTimeInterval(Self.frame)
            last = moving ? step : max(step, next)
            return last
        }
    }
}

final class KnobBotClock: @unchecked Sendable {
    private let lock = NSLock()
    private var moving = true
    private var next = Date.distantPast

    func read() -> (Bool, Date) { lock.withLock { (moving, next) } }
    func write(moving m: Bool, next n: Date) { lock.withLock { moving = m; next = n } }
}

@MainActor
final class KnobBotDriver {
    let clock = KnobBotClock()
    struct Config: Equatable {
        var tuning: BotBehavior.Tuning
        var reduceMotion: Bool
    }

    private var behavior: BotBehavior?
    private var config: Config?
    private let start = Date()

    func pose(at date: Date, mood: BotMood, config c: Config) -> BotPose {
        let t = date.timeIntervalSince(start)
        if behavior == nil || config?.tuning != c.tuning {
            behavior = BotBehavior(seed: 0x454D_4252, now: t, tuning: c.tuning)
            behavior?.reduceMotion = c.reduceMotion
        } else if config?.reduceMotion != c.reduceMotion {
            behavior?.reduceMotion = c.reduceMotion
        }
        config = c
        behavior?.setMood(mood, at: t)
        let p = behavior?.pose(at: t) ?? BotPose()
        if let b = behavior {
            clock.write(moving: b.isAnimating || b.isTransitioning,
                        next: start.addingTimeInterval(min(b.nextEventAt, t + 10)))
        }
        return p
    }

    nonisolated static func restingPose(_ mood: BotMood, theme: KnobTheme = .standard) -> BotPose {
        var b = BotBehavior(seed: 1, now: 0, tuning: theme.botTuning)
        b.reduceMotion = true
        b.setMood(mood, at: 0)
        var p = b.pose(at: 0.95)
        p.lidLeft = mood == .sleepy ? 0.55 : 0
        p.lidRight = p.lidLeft
        p.scaleX = 1; p.scaleY = 1; p.offsetX = 0; p.offsetY = 0
        switch mood {
        case .waiting, .error: (p.gazeX, p.gazeY) = (0, 0.1)
        case .working: (p.gazeX, p.gazeY) = (-0.25, -0.15)
        case .sleepy: (p.gazeX, p.gazeY) = (0, -0.35)
        case .idle, .done: (p.gazeX, p.gazeY) = BotBehavior.rest
        }
        return p
    }
}

public struct KnobPomoLive: View {
    let state: PomoState?
    let fetchedAt: Date?
    let note: String?
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double

    public init(state: PomoState?, fetchedAt: Date?, note: String? = nil, animated: Bool = true,
                theme: KnobTheme = .standard, brightness: Double = 1) {
        self.state = state; self.fetchedAt = fetchedAt; self.note = note; self.animated = animated; self.theme = theme
        self.brightness = brightness
    }

    public var body: some View {
        if animated {
            TimelineView(.periodic(from: .now, by: 1)) { tl in face(at: tl.date) }
        } else {
            face(at: fetchedAt ?? .now)
        }
    }

    private func face(at now: Date) -> some View {
        KnobFaceView(.pomodoro(KnobPomoFace(state: state, fetchedAt: fetchedAt, now: now, note: note,
                                            theme: theme.pomodoro)),
                     theme: theme, brightness: brightness)
        .equatable()
    }
}

public struct KnobWeatherLive: View {
    let state: WeatherState?
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double
    @State private var driver = KnobWeatherDriver()
    @State private var visible = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(state: WeatherState?, animated: Bool = true, theme: KnobTheme = .standard, brightness: Double = 1) {
        self.state = state; self.animated = animated; self.theme = theme; self.brightness = brightness
    }

    public var body: some View {
        let look = KnobWeatherLook(state: state, now: .now, maxAge: theme.weather.maxAgeS)
        let period = KnobWeatherScene.period(look)
        let moving = animated && !reduceMotion && period > 0
        TimelineView(.animation(minimumInterval: max(period, 0.05), paused: !moving || !visible)) { tl in
            let draws = driver.draws(look: look, at: moving ? tl.date : nil, colors: theme.weather.colors)
            KnobFaceView(.weather(look: look, draws: draws, tempC: state?.current?.tempC),
                         theme: theme, brightness: brightness)
        }
        .onAppear { visible = true }
        .onDisappear { visible = false }
    }
}

@MainActor
final class KnobWeatherDriver {
    private var scene = KnobWeatherScene()
    private var last: Date?

    func draws(look: KnobWeatherLook, at date: Date?, colors: KnobTheme.Weather.Colors) -> [KnobWeatherScene.Draw] {
        scene.setLook(look)
        if let date {
            if let last { scene.step(date.timeIntervalSince(last)) }
            last = date
        } else {
            last = nil
        }
        return scene.draws(colors: colors)
    }
}

public struct KnobNowPlayingLive: View {
    let state: NowPlayingState?
    let offline: Bool
    let offset: TimeInterval
    let pictures: KnobNowPlayingPictures
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double

    public init(state: NowPlayingState?, offline: Bool = false, offset: TimeInterval = 0,
                pictures: KnobNowPlayingPictures = .init(),
                animated: Bool = true, theme: KnobTheme = .standard, brightness: Double = 1) {
        self.state = state; self.offline = offline; self.offset = offset; self.pictures = pictures; self.animated = animated
        self.theme = theme; self.brightness = brightness
    }

    public var body: some View {
        if animated {
            TimelineView(.periodic(from: .now, by: 1)) { tl in face(at: tl.date) }
        } else {
            face(at: .now)
        }
    }

    private func face(at now: Date) -> some View {
        KnobFaceView(.nowPlaying(KnobNowPlayingFace(state: state, offline: offline, now: now.addingTimeInterval(offset),
                                                  pictures: pictures)),
                     theme: theme, brightness: brightness)
        .equatable()
    }
}
