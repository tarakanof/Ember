import Foundation

/// Paces a polling loop against the server's per-IP rate limiter.
///
/// A loop that keeps its cadence through a 429 is the reason it keeps getting
/// 429s: the token bucket cannot refill while it is still being drained. This
/// turns each denial into breathing room — at least what the server asked for
/// (`Retry-After`), doubling while denials continue, and back to the loop's
/// normal cadence on the first success.
///
/// Value type: each poll loop owns its own pacer (`var pacer = …`), so the
/// Device tab's stats poll and the matrix mirror back off independently.
public struct RateLimitBackoff: Sendable {
    /// What to assume when a 429 arrives without a `Retry-After` header. The
    /// Ember server always sends one; a proxy in between might not.
    public static let fallbackRetryAfter: Duration = .seconds(1)

    /// The outcome of one poll, as far as pacing is concerned.
    public enum Outcome: Sendable, Equatable {
        case succeeded
        case rateLimited(retryAfter: Duration)
        /// Any other failure. Paced exactly like a success — an unreachable
        /// device is not the limiter's business, and callers that want a slower
        /// retry for it (the matrix mirror) apply that themselves.
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
    /// server sends). A missing or unparseable value falls back rather than
    /// spinning at full speed.
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
            // Doubling from the previous backoff (not from base) is what makes a
            // sustained squeeze converge instead of re-probing every retryAfter.
            let grown = current.map { $0 * 2 } ?? max(retryAfter, base)
            // The floor is applied AFTER the cap on purpose: waiting less than
            // the server asked for just earns another denial, so retryAfter wins
            // over the cap rather than the other way round.
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
