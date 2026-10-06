import Foundation

public enum Feed: Hashable, CaseIterable, Sendable {
    case state, pomodoroState
    case stats, usage, meetings, apps
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

    public var baseCadence: Duration {
        switch tier {
        case .a: .seconds(3)
        case .b: .seconds(60)
        case .c: heldCadence
        }
    }

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

    public static let alwaysOn: Set<Feed> = Set(allCases.filter { $0.tier != .c })
}
