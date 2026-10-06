import Foundation

public enum BotMood: String, Sendable, CaseIterable {
    case idle, sleepy, working, waiting, error, done

    public init(state: String) {
        switch state {
        case "running": self = .working
        case "waiting": self = .waiting
        case "error":   self = .error
        case "done":    self = .done
        default:        self = .idle
        }
    }

    var eyes: BotEyes {
        switch self {
        case .idle, .sleepy, .working: return .dash
        case .waiting:                 return .round
        case .done:                    return .happy
        case .error:                   return .angry
        }
    }
}

public enum BotEyes: Sendable, Equatable { case dash, round, happy, angry }

public struct BotPose: Equatable, Sendable {
    public var mood: BotMood = .idle
    public var eyes: BotEyes = .dash
    public var gazeX = BotBehavior.rest.x, gazeY = BotBehavior.rest.y
    public var lidLeft = 0.0, lidRight = 0.0
    public var triangle = 0.0
    public var slump = 0.0
    public var badge = 0.0
    public var scaleX = 1.0, scaleY = 1.0
    public var offsetX = 0.0, offsetY = 0.0

    public init() {}

    public func quantized(toPixels radius: Double) -> BotPose {
        let step = 0.5 / max(radius, 1)
        func q(_ v: Double, _ s: Double) -> Double { (v / s).rounded() * s }
        var p = self
        p.gazeX = q(gazeX, step); p.gazeY = q(gazeY, step)
        p.lidLeft = q(lidLeft, step); p.lidRight = q(lidRight, step)
        p.triangle = q(triangle, step); p.slump = q(slump, step); p.badge = q(badge, step)
        p.scaleX = q(scaleX, step / 2); p.scaleY = q(scaleY, step / 2)
        p.offsetX = q(offsetX, step); p.offsetY = q(offsetY, step)
        return p
    }
}

public struct BotBehavior: Sendable {
    public static let rest = (x: 0.67, y: 0.77)
    static let viewer = (x: 0.0, y: 0.1)
    public static let sleepAfter = 300.0

    public struct Tuning: Sendable, Equatable {
        public var sleepAfter: Double
        public var hopLength: Double
        public var hopSquash: Double
        public var hopIntervalMedian: Double
        public var hopIntervalSigma: Double
        public var hopIntervalRange: ClosedRange<Double>

        public init(sleepAfter: Double, hopLength: Double, hopSquash: Double, hopIntervalMedian: Double,
                    hopIntervalSigma: Double, hopIntervalRange: ClosedRange<Double>) {
            self.sleepAfter = sleepAfter; self.hopLength = hopLength; self.hopSquash = hopSquash
            self.hopIntervalMedian = hopIntervalMedian; self.hopIntervalSigma = hopIntervalSigma
            self.hopIntervalRange = hopIntervalRange
        }

        public static let mac = Tuning(sleepAfter: BotBehavior.sleepAfter, hopLength: BotBehavior.hopLength,
                                       hopSquash: 1, hopIntervalMedian: 15, hopIntervalSigma: 0.4,
                                       hopIntervalRange: 8...40)
    }

    public var reduceMotion = false {
        didSet {
            if reduceMotion {
                nextHopAt = .infinity; hopStart = nil; popStart = nil
            } else {
                nextSaccadeAt = 0
                if mood == .waiting { nextHopAt = 0 }
            }
        }
    }
    public private(set) var mood: BotMood = .idle
    public private(set) var isAnimating = true
    public private(set) var nextEventAt = 0.0
    public private(set) var isTransitioning = false

    static let transitionLength = 0.7

    private var rng: SplitMix64
    private var eyes: BotEyes = .dash
    private var swapEyesOnClose = false
    private var moodSince: Double

    private var gazeFrom = (x: rest.x, y: rest.y), gazeTo = (x: rest.x, y: rest.y)
    private var gazeStart = -1.0, gazeDur = 0.0
    private var gazeEase = Ease.outBack
    private var nextSaccadeAt: Double
    private var readX = -0.6

    private var blinkStart: Double?
    private var blinkLag = 0.0, blinkSpeed = 1.0
    private var doubleBlinkPending = false
    private var nextBlinkAt: Double
    private var lastBlinkAt = -Double.infinity

    private var triangle = Tween(0), slump = Tween(0), badge = Tween(0)
    private var leanX = Tween(0), leanY = Tween(0)
    private var popStart: Double?
    private var hopStart: Double?
    private var nextHopAt = Double.infinity

    private let tuning: Tuning

    public init(seed: UInt64, now: Double, tuning: Tuning = .mac) {
        self.tuning = tuning
        rng = SplitMix64(state: seed)
        moodSince = now
        nextSaccadeAt = now + 1
        nextBlinkAt = now + 0.8
    }

    @discardableResult
    public mutating func setMood(_ m: BotMood, at t: Double) -> Bool {
        if m == mood || (m == .idle && mood == .sleepy) { return false }
        enter(m, at: t)
        return true
    }

    /// `t` is monotonic seconds.
    public mutating func pose(at t: Double) -> BotPose {
        if mood == .idle && t - moodSince >= tuning.sleepAfter { enter(.sleepy, at: t) }
        if blinkStart == nil && t >= nextBlinkAt { startBlink(at: t) }
        if t >= nextSaccadeAt { startSaccade(at: t) }
        if t >= nextHopAt {
            hopStart = t
            nextHopAt = t + lognormal(median: tuning.hopIntervalMedian, sigma: tuning.hopIntervalSigma,
                                      in: tuning.hopIntervalRange)
        }

        var p = BotPose()
        p.mood = mood

        var blinkL = 0.0, blinkR = 0.0
        if let b = blinkStart {
            let u = t - b
            blinkL = Self.lid(u, speed: blinkSpeed)
            blinkR = Self.lid(u - blinkLag, speed: blinkSpeed)
            if swapEyesOnClose && max(blinkL, blinkR) >= 0.9 {
                eyes = mood.eyes
                swapEyesOnClose = false
            }
            if u - blinkLag >= Self.blinkLength(speed: blinkSpeed) {
                if swapEyesOnClose && !doubleBlinkPending { eyes = mood.eyes; swapEyesOnClose = false }
                blinkStart = nil
                if doubleBlinkPending {
                    doubleBlinkPending = false
                    blinkStart = t + 0.06
                }
            }
        }
        p.eyes = eyes
        let heavy = slump.value(t) * 0.55
        p.lidLeft = heavy + (1 - heavy) * blinkL
        p.lidRight = heavy + (1 - heavy) * blinkR

        let sacActive = gazeStart >= 0 && t < gazeStart + gazeDur
        (p.gazeX, p.gazeY) = currentGaze(at: t)

        p.triangle = triangle.value(t)
        p.slump = slump.value(t)
        p.badge = badge.value(t)
        p.offsetX = leanX.value(t)
        p.offsetY = leanY.value(t)

        var hopping = false
        if let h = hopStart {
            let u = t - h
            if u < tuning.hopLength {
                let cu = tuning.hopLength == Self.hopLength ? u : u * Self.hopLength / tuning.hopLength
                let (sx, sy, dy) = Self.hop(cu, squash: tuning.hopSquash)
                p.scaleX *= sx; p.scaleY *= sy; p.offsetY += dy
                hopping = true
            } else {
                hopStart = nil
            }
        }
        var popping = false
        if let s = popStart {
            let u = (t - s) / 0.4
            if u < 1 {
                let k = 1 + 0.1 * sin(.pi * u) * (1 - u)
                p.scaleX *= k; p.scaleY *= k
                popping = true
            } else {
                popStart = nil
            }
        }

        isTransitioning = t < moodSince + Self.transitionLength && !reduceMotion
        isAnimating = blinkStart != nil || sacActive || hopping || popping
            || [triangle, slump, badge, leanX, leanY].contains { $0.isActive(at: t) }
        var next = min(nextBlinkAt, nextSaccadeAt, nextHopAt)
        if mood == .idle { next = min(next, moodSince + tuning.sleepAfter) }
        nextEventAt = next
        return p
    }

    private mutating func enter(_ m: BotMood, at t: Double) {
        let waking = mood == .sleepy
        mood = m
        moodSince = t
        swapEyesOnClose = true
        if blinkStart == nil { startBlink(at: t); blinkSpeed = max(blinkSpeed, 1.6) }
        else { doubleBlinkPending = true }
        if waking { doubleBlinkPending = true }

        let morph = reduceMotion ? 0.0 : 0.45
        triangle = Tween(from: triangle.value(t), to: m == .error ? 1 : 0, start: t, dur: morph, ease: .inOut)
        slump = Tween(from: slump.value(t), to: m == .sleepy ? 1 : 0, start: t,
                      dur: m == .sleepy ? 1.6 : morph, ease: .inOut)
        let badged = m == .waiting || m == .done
        badge = Tween(from: badge.value(t), to: badged ? 1 : 0, start: t + 0.1,
                      dur: reduceMotion ? 0 : 0.35, ease: badged ? .outBack : .inOut)
        popStart = reduceMotion || m == .sleepy ? nil : t
        readX = -0.6
        if !reduceMotion { startSaccade(at: t, glide: true) }
        nextHopAt = m == .waiting && !reduceMotion ? t + 0.6 : .infinity
    }

    private mutating func startBlink(at t: Double) {
        blinkStart = t
        lastBlinkAt = t
        blinkLag = Double.random(in: 0...0.02, using: &rng)
        blinkSpeed = mood == .sleepy ? 2.2 : 1
        doubleBlinkPending = Double.random(in: 0..<1, using: &rng) < 0.12
        nextBlinkAt = t + blinkInterval()
    }

    private mutating func startSaccade(at t: Double, glide: Bool = false) {
        guard !reduceMotion else { nextSaccadeAt = .infinity; return }
        let target = nextTarget()
        let from = currentGaze(at: t)
        let amp = hypot(target.x - from.x, target.y - from.y)
        gazeFrom = from
        gazeTo = target
        gazeStart = t
        gazeDur = glide ? 0.42 : 0.025 + 0.045 * amp
        gazeEase = glide ? .inOut : .outBack
        if !glide && amp > 0.8 && blinkStart == nil && t - lastBlinkAt > 1.2
            && Double.random(in: 0..<1, using: &rng) < 0.6 {
            startBlink(at: t)
        }
        let leanDur = glide ? 0.5 : 0.2
        leanX = Tween(from: leanX.value(t), to: target.x * 0.045, start: t + 0.03, dur: leanDur, ease: .inOut)
        leanY = Tween(from: leanY.value(t), to: target.y * 0.03, start: t + 0.03, dur: leanDur, ease: .inOut)
        nextSaccadeAt = t + gazeDur + fixation()
    }

    private func currentGaze(at t: Double) -> (x: Double, y: Double) {
        guard gazeStart >= 0 && t < gazeStart + gazeDur else { return gazeTo }
        let k = gazeEase.apply((t - gazeStart) / gazeDur)
        return (gazeFrom.x + (gazeTo.x - gazeFrom.x) * k, gazeFrom.y + (gazeTo.y - gazeFrom.y) * k)
    }

    private mutating func nextTarget() -> (x: Double, y: Double) {
        let r = Double.random(in: 0..<1, using: &rng)
        func j(_ a: Double) -> Double { Double.random(in: -a...a, using: &rng) }
        switch mood {
        case .idle:
            if r < 0.45 { return (Self.rest.x + j(0.1), Self.rest.y + j(0.08)) }
            if r < 0.7 { return (Self.viewer.x + j(0.08), Self.viewer.y + j(0.08)) }
            return (j(0.9), Double.random(in: -0.6...0.8, using: &rng))
        case .working:
            if r < 0.1 { return (Self.viewer.x + j(0.05), Self.viewer.y) }
            readX += Double.random(in: 0.28...0.45, using: &rng)
            if readX > 0.7 { readX = -0.7 }
            return (readX, -0.15 + j(0.04))
        case .waiting:
            if r < 0.75 { return (Self.viewer.x + j(0.05), Self.viewer.y + j(0.05)) }
            return (r < 0.875 ? -0.7 : 0.7, j(0.2))
        case .sleepy:
            return (j(0.5), Double.random(in: -0.5 ... -0.2, using: &rng))
        case .error:
            return (j(0.9), j(0.7))
        case .done:
            if r < 0.5 { return (Self.rest.x + j(0.08), Self.rest.y + j(0.06)) }
            return (Self.viewer.x + j(0.06), Self.viewer.y + j(0.06))
        }
    }

    private mutating func fixation() -> Double {
        switch mood {
        case .idle:    return lognormal(median: 2.2, sigma: 0.5, in: 0.6...7)
        case .working: return lognormal(median: 0.9, sigma: 0.35, in: 0.35...2)
        case .waiting: return lognormal(median: 2.5, sigma: 0.4, in: 0.8...6)
        case .sleepy:  return lognormal(median: 8, sigma: 0.4, in: 4...20)
        case .error:   return lognormal(median: 0.9, sigma: 0.4, in: 0.3...2.5)
        case .done:    return lognormal(median: 2.5, sigma: 0.4, in: 0.8...6)
        }
    }

    private mutating func blinkInterval() -> Double {
        let median: Double
        switch mood {
        case .idle:    median = 4.0
        case .working: median = 6.0
        case .waiting: median = 3.0
        case .sleepy:  median = 2.8
        case .error:   median = 2.2
        case .done:    median = 3.2
        }
        return lognormal(median: median, sigma: 0.45, in: 1.0...12)
    }

    private mutating func lognormal(median: Double, sigma: Double, in range: ClosedRange<Double>) -> Double {
        let u1 = Double.random(in: Double.leastNonzeroMagnitude...1, using: &rng)
        let u2 = Double.random(in: 0..<1, using: &rng)
        let n = (-2 * log(u1)).squareRoot() * cos(2 * .pi * u2)
        return min(max(median * exp(sigma * n), range.lowerBound), range.upperBound)
    }

    static func lid(_ u: Double, speed: Double) -> Double {
        let close = 0.075 * speed, hold = 0.035 * speed, open = 0.15 * speed
        if u <= 0 { return 0 }
        if u < close { let v = u / close; return v * v }
        if u < close + hold { return 1 }
        if u < close + hold + open { let v = (u - close - hold) / open; return (1 - v) * (1 - v) }
        return 0
    }

    static func blinkLength(speed: Double) -> Double { (0.075 + 0.035 + 0.15) * speed }

    static let hopLength = 0.62

    static func hop(_ u: Double, squash: Double) -> (Double, Double, Double) {
        let (sx, sy, dy) = hop(u)
        guard squash != 1 else { return (sx, sy, dy) }
        return (1 + (sx - 1) * squash, 1 + (sy - 1) * squash, dy)
    }

    static func hop(_ u: Double) -> (Double, Double, Double) {
        func lerp(_ a: Double, _ b: Double, _ k: Double) -> Double { a + (b - a) * min(max(k, 0), 1) }
        switch u {
        case ..<0.12:
            let k = Ease.inOut.apply(u / 0.12)
            return (1 + 0.1 * k, 1 - 0.14 * k, 0)
        case ..<0.30:
            let v = (u - 0.12) / 0.18
            let s = v < 0.3 ? (lerp(1.1, 0.93, v / 0.3), lerp(0.86, 1.1, v / 0.3))
                            : (lerp(0.93, 1, (v - 0.3) / 0.7), lerp(1.1, 1, (v - 0.3) / 0.7))
            return (s.0, s.1, 0.22 * (1 - (1 - v) * (1 - v)))
        case ..<0.46:
            let v = (u - 0.30) / 0.16
            return (lerp(1, 0.95, v), lerp(1, 1.07, v), 0.22 * (1 - v * v))
        default:
            let v = min((u - 0.46) / 0.16, 1)
            let k = sin(.pi * v)
            return (1 + 0.1 * k, 1 - 0.12 * k, 0)
        }
    }
}

enum Ease: Sendable {
    case inOut, outBack

    func apply(_ u: Double) -> Double {
        let u = min(max(u, 0), 1)
        switch self {
        case .inOut:
            return u < 0.5 ? 4 * u * u * u : 1 - pow(-2 * u + 2, 3) / 2
        case .outBack:
            let c1 = 1.4, c3 = c1 + 1
            return 1 + c3 * pow(u - 1, 3) + c1 * pow(u - 1, 2)
        }
    }
}

struct Tween: Sendable {
    var from, to, start, dur: Double
    var ease: Ease

    init(_ v: Double) { from = v; to = v; start = 0; dur = 0; ease = .inOut }
    init(from: Double, to: Double, start: Double, dur: Double, ease: Ease) {
        self.from = from; self.to = to; self.start = start; self.dur = dur; self.ease = ease
    }

    func value(_ t: Double) -> Double {
        if t < start { return from }
        if dur <= 0 || t >= start + dur { return to }
        return from + (to - from) * ease.apply((t - start) / dur)
    }

    func isActive(at t: Double) -> Bool { from != to && t < start + dur }
}

struct SplitMix64: RandomNumberGenerator, Sendable {
    var state: UInt64
    mutating func next() -> UInt64 {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }
}
