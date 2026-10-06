import Foundation

public struct RateLimitBackoff: Sendable {
    public static let fallbackRetryAfter: Duration = .seconds(1)

    public enum Outcome: Sendable, Equatable {
        case succeeded
        case rateLimited(retryAfter: Duration)
        case failed
    }

    private let base: Duration
    private let cap: Duration
    private var current: Duration?

    public init(base: Duration, cap: Duration = .seconds(30)) {
        self.base = base
        self.cap = cap
    }

    public static func retryAfter(header: String?) -> Duration {
        guard let header, let seconds = Int(header.trimmingCharacters(in: .whitespaces)), seconds > 0 else {
            return fallbackRetryAfter
        }
        return .seconds(seconds)
    }

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
    public var wholeSecondsRoundedUp: Int {
        let parts = components
        return Int(parts.seconds) + (parts.attoseconds > 0 ? 1 : 0)
    }
}
