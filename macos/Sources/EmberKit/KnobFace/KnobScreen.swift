import SwiftUI

/// A knob face behind the round glass: a thin metal bezel, a glass sheen,
/// and the face dimmed when its page is off.
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

/// The bot page, animated with the knob's `BotBehavior` timings while
/// `animated` and on screen; Reduce Motion keeps only the blinks.
public struct KnobBotLive: View {
    let mood: KnobMood
    let sleepAfter: Double
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double
    @State private var driver = KnobBotDriver()
    @State private var visible = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// `sleepAfter` is the knob's `bot.sleepy_after_s` (0: never).
    public init(mood: KnobMood, sleepAfter: Double, animated: Bool = true, theme: KnobTheme = .standard,
                brightness: Double = 1) {
        self.mood = mood; self.sleepAfter = sleepAfter; self.animated = animated; self.theme = theme
        self.brightness = brightness
    }

    public var body: some View {
        Group {
            if animated {
                TimelineView(.animation(minimumInterval: 1.0 / 20, paused: !visible)) { tl in
                    KnobFaceView(.bot(pose: driver.pose(at: tl.date, mood: mood.mood, config: config), mood: mood),
                                 theme: theme, brightness: brightness)
                }
            } else {
                KnobFaceView(.bot(pose: KnobBotDriver.restingPose(mood.mood, theme: theme), mood: mood),
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

/// Owns the knob bot's `BotBehavior` across frames.
@MainActor
final class KnobBotDriver {
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
        return behavior?.pose(at: t) ?? BotPose()
    }

    /// A settled, eyes-open frame for `mood`, looking where that mood looks.
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

/// The Pomodoro page, ticking once a second while `animated`.
public struct KnobPomoLive: View {
    let state: PomoState?
    let fetchedAt: Date?
    let note: String?
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double

    /// `fetchedAt` is when `state` was read; the countdown runs on from it.
    public init(state: PomoState?, fetchedAt: Date?, note: String? = nil, animated: Bool = true,
                theme: KnobTheme = .standard, brightness: Double = 1) {
        self.state = state; self.fetchedAt = fetchedAt; self.note = note; self.animated = animated; self.theme = theme
        self.brightness = brightness
    }

    public var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { tl in
            KnobFaceView(.pomodoro(KnobPomoFace(state: state, fetchedAt: fetchedAt, now: animated ? tl.date : .now,
                                                note: note, theme: theme.pomodoro)),
                         theme: theme, brightness: brightness)
        }
    }
}

/// The weather page: the sky animates at the firmware's frame rate while
/// `animated`, on screen and Reduce Motion is off.
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
