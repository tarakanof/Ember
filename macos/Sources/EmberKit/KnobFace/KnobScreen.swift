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
        if !sourceLabel { m.host = ""; m.sub = "" }
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

public struct KnobWeatherLive: View {
    let face: KnobFace
    let since: Date?
    let animated: Bool
    let theme: KnobTheme
    let brightness: Double
    @State private var visible = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(face: KnobFace, since: Date?, animated: Bool = true, theme: KnobTheme = .standard, brightness: Double = 1) {
        self.face = face; self.since = since; self.animated = animated; self.theme = theme; self.brightness = brightness
    }

    public var body: some View {
        let motion = KnobWeatherMotion(face, reduceMotion: reduceMotion)
        Group {
            if animated, visible, motion != .still, let since {
                TimelineView(KnobWeatherSchedule(since: since, motion: motion)) { tl in
                    KnobFaceView(face, theme: theme, brightness: brightness, t: tl.date.timeIntervalSince(since)).equatable()
                }
                .id(since)
            } else {
                KnobFaceView(face, theme: theme, brightness: brightness).equatable()
            }
        }
        .onAppear { visible = true }
        .onDisappear { visible = false }
    }
}

struct KnobWeatherSchedule: TimelineSchedule {
    let since: Date
    let motion: KnobWeatherMotion

    func entries(from start: Date, mode: Mode) -> AnyIterator<Date> {
        let step = 1 / KnobWeatherMotion.fps, t0 = start.timeIntervalSince(since)
        var until = Double.infinity
        switch motion {
        case .still: return AnyIterator { nil }
        case .loop: break
        case .arrival(let end): until = end
        }
        var n = (t0 / step - 0.5).rounded(.down)
        var last = t0
        var first = true
        return AnyIterator {
            if first { first = false; return start }
            guard last < until else { return nil }
            n += 1
            last = (n + 0.5) * step
            return since.addingTimeInterval(last)
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

public struct KnobWeatherMemo: Equatable, Sendable {
    public private(set) var band: Int?
    public private(set) var drawn: KnobWeatherFaceID?
    public private(set) var shownAt: Date?
    private var rain: KnobRainFace?
    private var cast: KnobCastFace?
    private var wind: KnobWindFace?
    private var air: KnobAirFace?

    public init() {}

    public func draw(_ pick: KnobWeatherFaceID?, state: WeatherState?, now: Date, at: Date? = nil,
                     theme: KnobTheme.Weather = KnobTheme.standard.weather,
                     calendar: Calendar = .current) -> (face: KnobFace, commit: KnobWeatherMemo?) {
        let p = peek(pick, state: state, now: now, at: at, theme: theme, calendar: calendar)
        return (p.face, p.memo == self ? nil : p.memo)
    }

    public func peek(_ pick: KnobWeatherFaceID?, state: WeatherState?, now: Date, at: Date? = nil,
                     theme: KnobTheme.Weather = KnobTheme.standard.weather,
                     calendar: Calendar = .current) -> (face: KnobFace, memo: KnobWeatherMemo) {
        var m = self
        let f = m.face(pick, state: state, now: now, at: at, theme: theme, calendar: calendar)
        return (f, m)
    }

    public mutating func face(_ pick: KnobWeatherFaceID?, state: WeatherState?, now: Date, at: Date? = nil,
                              theme: KnobTheme.Weather = KnobTheme.standard.weather,
                              calendar: Calendar = .current) -> KnobFace {
        let temp = face(state: state, now: now, theme: theme, calendar: calendar)
        let id = pick ?? KnobWeatherAuto.face(state: state, now: now, shown: drawn, theme: theme, calendar: calendar)
        let kept = drawn == id
        drawn = id
        if !kept { shownAt = at ?? now }
        switch id {
        case .temp:
            return .weather(temp)
        case .rain:
            let f = KnobRainFace(state: state, now: now, shown: kept ? rain : nil, theme: theme, calendar: calendar)
            rain = f
            return .rain(f)
        case .cast:
            let f = KnobCastFace(state: state, now: now, shown: kept ? cast : nil, theme: theme, calendar: calendar)
            cast = f
            return .cast(f)
        case .wind:
            let f = KnobWindFace(state: state, now: now, shown: kept ? wind : nil, theme: theme, calendar: calendar)
            wind = f
            return .wind(f)
        case .days:
            return .days(KnobDaysFace(state: state, now: now, theme: theme, calendar: calendar))
        case .sun:
            return .sun(KnobSunFace(state: state, now: now, theme: theme, calendar: calendar))
        case .air:
            let f = KnobAirFace(state: state, now: now, shown: kept ? air : nil, theme: theme, calendar: calendar)
            air = f
            return .air(f)
        }
    }

    public mutating func face(state: WeatherState?, now: Date, theme: KnobTheme.Weather = KnobTheme.standard.weather,
                              calendar: Calendar = .current) -> KnobTempFace {
        let f = KnobTempFace(state: state, now: now, theme: theme, calendar: calendar, shownBand: band)
        band = f.cloudBand
        return f
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
