import Foundation

/// Why a feed or an action failed, in the terms the UI distinguishes.
public enum FeedError: Error, Equatable, Sendable {
    /// Transport failure: the server (or this Mac's network) is unreachable.
    case offline
    /// 401: the token is missing or wrong.
    case unauthorized
    /// 429: the server's per-IP limiter is throttling this Mac.
    case rateLimited
    /// 404: the feature is off, or the server predates the route.
    case featureOff
    /// Anything else, with the server's message.
    case server(String)

    /// Maps any error from `APIClient` (or below) to a `FeedError`.
    public init(_ error: Error) {
        if let e = error as? FeedError {
            self = e
            return
        }
        if let e = error as? RequestNotSent {
            self.init(e.underlying)
            return
        }
        guard let api = error as? APIError else {
            self = error is URLError ? .offline : .server(error.localizedDescription)
            return
        }
        switch api {
        case .notConfigured, .transport: self = .offline
        case .rateLimited: self = .rateLimited
        case .http(401, _): self = .unauthorized
        case .http(404, _): self = .featureOff
        case .http, .decoding: self = .server(api.localizedDescription)
        }
    }
}

extension FeedError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .offline: "Server unreachable"
        case .unauthorized: "Unauthorized — check the token in Connection settings."
        case .rateLimited: "The server is rate-limiting this Mac."
        case .featureOff: "Not available — the feature is off or the server is too old."
        case .server(let message): message
        }
    }
}
