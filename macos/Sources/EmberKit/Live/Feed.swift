import Foundation

/// One server read that `LiveModel` keeps fresh. Every view reads feeds from
/// `LiveModel`; nothing else polls.
public enum Feed: Hashable, CaseIterable, Sendable {
    /// Tier A: always polled, every 3 s.
    case state, pomodoroState
    /// Tier B: always polled, every 60 s; faster while a view tracks them.
    case stats, usage, meetings, apps
    /// Tier C: polled only while a view tracks them.
    case screen, clockHealth, weather, activity, workhours, heatmap

    public enum Tier: Sendable, Equatable {
        case a, b, c
    }

    public var tier: Tier {
        switch self {
        case .state, .pomodoroState: .a
        case .stats, .usage, .meetings, .apps: .b
        case .screen, .clockHealth, .weather, .activity, .workhours, .heatmap: .c
        }
    }

    /// Cadence with no view tracking the feed. Tier C feeds don't poll then;
    /// their value is the cadence used once tracked.
    public var baseCadence: Duration {
        switch tier {
        case .a: .seconds(3)
        case .b: .seconds(60)
        case .c: heldCadence
        }
    }

    /// Cadence while at least one view tracks the feed.
    public var heldCadence: Duration {
        switch self {
        case .state, .pomodoroState: .seconds(3)
        case .stats, .usage: .seconds(30)
        case .meetings, .apps: .seconds(60)
        case .screen: .seconds(1)
        case .clockHealth: .seconds(15)
        case .weather, .activity, .workhours, .heatmap: .seconds(300)
        }
    }

    /// Feeds polled from launch whether or not anything is on screen.
    public static let alwaysOn: Set<Feed> = Set(allCases.filter { $0.tier != .c })
}
