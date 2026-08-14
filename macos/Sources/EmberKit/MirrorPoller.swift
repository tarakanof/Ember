import Foundation

/// The matrix mirror's per-tick decisions, as state rather than control flow.
///
/// The mirror polls two independent sources — the server's `/v1/device/screen`
/// proxy, and (for servers predating that route) the clock's own address — and
/// the rules for choosing between them are exactly where two bugs have lived:
/// treating a 429 as "the proxy is missing", and skipping the direct read while
/// throttled, which left the panel black for the whole throttled window. Both
/// are one-line tests here; neither was reachable while the rules lived inside
/// a SwiftUI `.task` loop.
///
/// Usage per tick: check `probesProxy`, `record` the proxy attempt if you made
/// one, ask `triesDirect(havePixels:)`, then `endTick(havePixels:)` for the
/// sleep interval.
public struct MirrorPoller: Sendable {
    /// What the proxy call did this tick.
    public enum ProxyOutcome: Sendable, Equatable {
        case pixels
        /// The server's rate limiter said no. Says nothing about whether the
        /// route exists, so it must not demote the proxy.
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
    ///   - unreachable: interval while neither source yields pixels.
    ///   - reprobeEvery: how often to re-probe a demoted proxy, in ticks.
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

    /// Records the proxy attempt. Call once per tick, only when one was made.
    public mutating func record(_ outcome: ProxyOutcome) {
        lastOutcome = outcome
        switch outcome {
        case .pixels: preferProxy = true
        case .failed: preferProxy = false
        case .throttled: break // deliberately unchanged
        }
    }

    /// Whether to read the clock directly. True whenever the tick has no pixels
    /// yet — throttled or not, because that read never touches the server and so
    /// costs no rate-limit budget.
    public func triesDirect(havePixels: Bool) -> Bool { !havePixels }

    /// Closes the tick and returns how long to wait before the next one.
    public mutating func endTick(havePixels: Bool) -> Duration {
        let outcome = lastOutcome
        lastOutcome = nil
        tick += 1

        if case .throttled(let retryAfter) = outcome {
            // Back off even if the direct read saved this frame: the proxy is
            // throttled either way, and hammering it keeps it that way.
            return pacer.nextDelay(after: .rateLimited(retryAfter: retryAfter))
        }
        guard havePixels else { return unreachable }
        return pacer.nextDelay(after: .succeeded)
    }
}
