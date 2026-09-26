import Foundation

/// Why a feed or an action failed, in the terms the UI distinguishes.
public enum FeedError: Error, Equatable, Sendable {
    /// Transport failure: the server (or this Mac's network) is unreachable.
    case offline
    /// No answer came in time: the server may be up and waiting on the
    /// clock, so this isn't `offline`.
    case timedOut
    /// macOS Local Network privacy blocks this app from the LAN server: looks
    /// like `offline`, but the fix is a permission, not the server.
    case localNetworkDenied
    /// 401: the token is missing or wrong.
    case unauthorized
    /// 429: the server's per-IP limiter is throttling this Mac.
    case rateLimited
    /// 404/405: the feature is off, or the server predates the route.
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
        if let v = error as? ValidationError {
            self = .server(String(localized: v.message))
            return
        }
        guard let api = error as? APIError else {
            switch (error as? URLError)?.code {
            case .timedOut?: self = .timedOut
            case _?: self = .offline
            case nil: self = .server(error.localizedDescription)
            }
            return
        }
        switch api {
        case .notConfigured, .transport: self = .offline
        case .timedOut: self = .timedOut
        case .localNetworkDenied: self = .localNetworkDenied
        case .rateLimited: self = .rateLimited
        case .http(401, _): self = .unauthorized
        // 405: a server that has only the POST of a route this app reads
        // (/v1/usage before 0.28) — the read is just as missing as a 404.
        case .http(404, _), .http(405, _): self = .featureOff
        case .http, .decoding: self = .server(api.localizedDescription)
        }
    }
}

extension FeedError: LocalizedError {
    /// What went wrong, for a status row or an alert.
    public var message: LocalizedStringResource {
        switch self {
        case .offline: "Server unreachable"
        case .timedOut:
            LocalizedStringResource("The server didn't answer in time",
                                    comment: "Error: a request reached the server but no reply came before the app stopped waiting.")
        case .localNetworkDenied: "Local Network access is off for Ember"
        case .unauthorized: "Unauthorized — check the token in Connection settings."
        case .rateLimited: "The server is rate-limiting this Mac."
        case .featureOff: "Not available — the feature is off or the server is too old."
        case .server(let message): "Server error: \(message)"
        }
    }

    public var errorDescription: String? { String(localized: message) }

    /// Whether no answer came from the server at all, for whatever reason.
    public var isUnreachable: Bool { self == .offline || self == .timedOut || self == .localNetworkDenied }
}
