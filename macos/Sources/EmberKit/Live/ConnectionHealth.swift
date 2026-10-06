import Foundation

public enum ConnectionHealth: Equatable, Sendable {
    case unconfigured
    case connecting
    case online(since: Date)
    case degraded(failures: Int)
    case offline(since: Date)

    public var isOnline: Bool {
        switch self {
        case .online, .degraded: true
        default: false
        }
    }
}
