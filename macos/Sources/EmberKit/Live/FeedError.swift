import Foundation

public enum FeedError: Error, Equatable, Sendable {
    case offline
    case timedOut
    case localNetworkDenied
    case unauthorized
    case rateLimited
    case featureOff
    case clockTimedOut(ClockWriteOutcome?)
    case server(String)

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
        case .clockTimedOut(let outcome): self = .clockTimedOut(outcome)
        case .rateLimited: self = .rateLimited
        case .http(401, _): self = .unauthorized
        case .http(404, _), .http(405, _): self = .featureOff
        case .http, .decoding: self = .server(api.localizedDescription)
        }
    }
}

extension FeedError: LocalizedError {
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
        case .clockTimedOut(nil):
            LocalizedStringResource("The clock didn't finish in time.",
                                    comment: "Error: reading the clock's settings failed because the clock was too slow; nothing was changed.")
        case .clockTimedOut(.notSent):
            LocalizedStringResource("The clock didn't finish in time. Nothing was changed.",
                                    comment: "Error: a clock setting wasn't saved because the clock was too slow; the clock is unchanged.")
        case .clockTimedOut(.unknown):
            LocalizedStringResource("The clock didn't finish in time. The change may not have been saved.",
                                    comment: "Error: a clock setting was sent but the clock never confirmed it, so it may or may not be saved.")
        case .clockTimedOut(.applied):
            LocalizedStringResource("The clock didn't finish in time. Saved, but not fully applied yet.",
                                    comment: "Error: the clock saved a setting, but the follow-up work (for example during a focus session) didn't finish.")
        case .server(let message): "Server error: \(message)"
        }
    }

    public var errorDescription: String? { String(localized: message) }

    public var isUnreachable: Bool { self == .offline || self == .timedOut || self == .localNetworkDenied }
}
