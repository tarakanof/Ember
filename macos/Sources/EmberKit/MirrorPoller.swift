import Foundation

public struct MirrorPoller: Sendable {
    public enum ProxyOutcome: Sendable, Equatable {
        case pixels
        case throttled(Duration)
        case failed
    }

    private let cadence: Duration
    private let unreachable: Duration
    private let reprobeEvery: Int

    private var preferProxy = true
    private var tick = 0
    private var lastOutcome: ProxyOutcome?
    private var pacer: RateLimitBackoff

    public init(cadence: Duration = .seconds(1),
                unreachable: Duration = .seconds(3),
                reprobeEvery: Int = 30) {
        self.cadence = cadence
        self.unreachable = unreachable
        self.reprobeEvery = reprobeEvery
        self.pacer = RateLimitBackoff(base: cadence)
    }

    public var probesProxy: Bool { preferProxy || tick % reprobeEvery == 0 }

    public mutating func record(_ outcome: ProxyOutcome) {
        lastOutcome = outcome
        switch outcome {
        case .pixels: preferProxy = true
        case .failed: preferProxy = false
        case .throttled: break
        }
    }

    public func triesDirect(havePixels: Bool) -> Bool { !havePixels }

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
