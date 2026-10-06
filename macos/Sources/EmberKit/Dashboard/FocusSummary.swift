import Foundation

public struct FocusSummary: Equatable, Sendable {
    public enum Ring: Equatable, Sendable {
        case goal(completed: Int, goal: Int?)
        case phase(PomoPhase, remaining: Int, planned: Int, endsAt: Date?, round: Int)
    }

    public let ring: Ring
    public let completedToday: Int
    public let focusMinutesToday: Int
    public let streak: Int
    public let longestStreak: Int
    public let completionRate: Double?
    public let dailyGoal: Int?
    public let dailyGoalMet: Bool

    public var isEmpty: Bool {
        if case .phase = ring { return false }
        return completedToday == 0 && streak == 0 && longestStreak == 0 && completionRate == nil
    }

    public init(stats: PomoStats?, state: PomoState?, now: Date) {
        let goal = stats.flatMap { $0.goal.dailySessions > 0 ? $0.goal.dailySessions : nil }
        let done = stats?.today.completedFocus ?? 0
        completedToday = done
        focusMinutesToday = stats?.today.focusMin ?? 0
        streak = stats?.streak ?? 0
        longestStreak = max(stats?.longestStreak ?? 0, streak)
        completionRate = (stats?.completion.totalFocus ?? 0) > 0 ? stats?.completion.completionRate : nil
        dailyGoal = goal
        dailyGoalMet = goal.map { done >= $0 } ?? false
        if let state, state.mode != .idle {
            let endsAt = state.mode == .running
                ? now.addingTimeInterval(TimeInterval(max(0, state.remainingSec))) : nil
            ring = .phase(state.phaseEnum, remaining: max(0, state.remainingSec),
                          planned: max(state.plannedSec, state.remainingSec, 1),
                          endsAt: endsAt, round: state.round)
        } else {
            ring = .goal(completed: done, goal: goal)
        }
    }

    public var gauge: (value: Double, total: Double) {
        switch ring {
        case .goal(let done, let goal):
            let total = Double(max(goal ?? max(done, 1), 1))
            return (min(Double(done), total), total)
        case .phase(_, let remaining, let planned, _, _):
            return (Double(max(0, planned - remaining)), Double(planned))
        }
    }
}
