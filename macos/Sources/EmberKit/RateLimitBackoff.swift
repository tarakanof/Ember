import Foundation

/// Paces a polling loop against the server's per-IP rate limiter.
public struct RateLimitBackoff: Sendable {
    /// What to assume when a 429 arrives without a `Retry-After` header.
    public static let fallbackRetryAfter: Duration = .seconds(1)

    /// The outcome of one poll, as far as pacing is concerned.
    public enum Outcome: Sendable, Equatable {
        case succeeded
        case rateLimited(retryAfter: Duration)
        /// Any other failure.
        case failed
    }

    private let base: Duration
    private let cap: Duration
    private var current: Duration?

    public init(base: Duration, cap: Duration = .seconds(30)) {
        self.base = base
        self.cap = cap
    }

    /// Parses a `Retry-After` header value in delta-seconds form (what the Ember
    /// server sends).
    public static func retryAfter(header: String?) -> Duration {
        guard let header, let seconds = Int(header.trimmingCharacters(in: .whitespaces)), seconds > 0 else {
            return fallbackRetryAfter
        }
        return .seconds(seconds)
    }

    /// How long to wait before the next poll.
    public mutating func nextDelay(after outcome: Outcome) -> Duration {
        switch outcome {
        case .succeeded, .failed:
            current = nil
            return base
        case .rateLimited(let retryAfter):
            let grown = current.map { $0 * 2 } ?? max(retryAfter, base)
            current = max(min(grown, cap), retryAfter)
            return current!
        }
    }
}

extension Duration {
    /// Whole seconds, rounded up, for user-facing text ("retrying in 3s").
    public var wholeSecondsRoundedUp: Int {
        let parts = components
        return Int(parts.seconds) + (parts.attoseconds > 0 ? 1 : 0)
    }
}
