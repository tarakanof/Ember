import Foundation

/// What the Focus card's ring and footer show.
public struct FocusSummary: Equatable, Sendable {
    /// The ring: today's sessions toward the goal while the timer is idle,
    /// the running phase otherwise.
    public enum Ring: Equatable, Sendable {
        /// `goal` is nil when the daily goal is off; the ring then fills to
        /// the day's count so it still reads as "done so far".
        case goal(completed: Int, goal: Int?)
        /// Seconds left of `planned`; `endsAt` is set while it counts down.
        case phase(PomoPhase, remaining: Int, planned: Int, endsAt: Date?, round: Int)
    }

    public let ring: Ring
    public let completedToday: Int
    public let focusMinutesToday: Int
    public let streak: Int
    public let longestStreak: Int
    /// 0...1 over 30 days; nil before the first focus phase ends.
    public let completionRate: Double?
    public let dailyGoal: Int?
    public let dailyGoalMet: Bool

    /// Nothing to show yet: no focus ever recorded and the timer is idle.
    public var isEmpty: Bool {
        if case .phase = ring { return false }
        return completedToday == 0 && streak == 0 && longestStreak == 0 && completionRate == nil
    }

    /// `stats` may be missing (still loading, or an old server) while the
    /// timer state is known, and the other way round.
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

    /// The gauge's value and range: sessions out of the goal, or elapsed
    /// seconds out of the phase. The range is never empty.
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
