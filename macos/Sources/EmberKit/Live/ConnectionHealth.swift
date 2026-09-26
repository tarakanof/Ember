import Foundation

/// Whether the server answers `/state`, as the menu, the Dashboard subtitle
/// and the bot see it. One failed poll isn't an outage on this Wi-Fi, so
/// `.degraded` holds the last snapshot for a couple of misses first.
public enum ConnectionHealth: Equatable, Sendable {
    /// No server URL in producer.env.
    case unconfigured
    /// Configured, no answer yet.
    case connecting
    case online(since: Date)
    /// Online before, the last `failures` polls failed (fewer than
    /// `LiveModel.offlineAfterFailures`); the snapshot is still shown as live.
    case degraded(failures: Int)
    case offline(since: Date)

    public var isOnline: Bool {
        switch self {
        case .online, .degraded: true
        default: false
        }
    }
}
