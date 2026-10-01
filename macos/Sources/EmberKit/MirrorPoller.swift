import Foundation

/// The matrix mirror's per-tick decisions, as state rather than control flow.
public struct MirrorPoller: Sendable {
    /// What the proxy call did this tick.
    public enum ProxyOutcome: Sendable, Equatable {
        case pixels
        /// The server's rate limiter said no.
        case throttled(Duration)
        /// Anything else — most importantly a 404 from a server too old to have
        /// the route, which is what demotion is for.
        case failed
    }

    private let cadence: Duration
    private let unreachable: Duration
    private let reprobeEvery: Int

    private var preferProxy = true
    private var tick = 0
    private var lastOutcome: ProxyOutcome?
    private var pacer: RateLimitBackoff

    /// - Parameters:
    ///   - cadence: normal interval between frames.
    public init(cadence: Duration = .seconds(1),
                unreachable: Duration = .seconds(3),
                reprobeEvery: Int = 30) {
        self.cadence = cadence
        self.unreachable = unreachable
        self.reprobeEvery = reprobeEvery
        self.pacer = RateLimitBackoff(base: cadence)
    }

    /// Whether this tick should ask the proxy: always while it's working, and
    /// periodically after it has been demoted, so a server that gains the route
    /// (or comes back) is picked up without restarting the app.
    public var probesProxy: Bool { preferProxy || tick % reprobeEvery == 0 }

    /// Records the proxy attempt.
    public mutating func record(_ outcome: ProxyOutcome) {
        lastOutcome = outcome
        switch outcome {
        case .pixels: preferProxy = true
        case .failed: preferProxy = false
        case .throttled: break
        }
    }

    /// Whether to read the clock directly.
    public func triesDirect(havePixels: Bool) -> Bool { !havePixels }

    /// Closes the tick and returns how long to wait before the next one.
    public mutating func endTick(havePixels: Bool) -> Duration {
        let outcome = lastOutcome
        lastOutcome = nil
        tick += 1

        if case .throttled(let retryAfter) = outcome {
            return pacer.nextDelay(after: .rateLimited(retryAfter: retryAfter))
        }
        guard havePixels else { return unreachable }
        return pacer.nextDelay(after: .succeeded)
    }
}
