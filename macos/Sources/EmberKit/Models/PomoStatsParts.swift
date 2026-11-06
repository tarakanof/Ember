import Foundation

public struct CompletionStat: Codable, Sendable, Equatable {
    public var completedFocus: Int
    public var abandonedFocus: Int
    public var totalFocus: Int
    public var completionRate: Double
    public var focusSec: Int

    public init(completedFocus: Int = 0, abandonedFocus: Int = 0, totalFocus: Int = 0,
                completionRate: Double = 0, focusSec: Int = 0) {
        self.completedFocus = completedFocus
        self.abandonedFocus = abandonedFocus
        self.totalFocus = totalFocus
        self.completionRate = completionRate
        self.focusSec = focusSec
    }

    enum CodingKeys: String, CodingKey {
        case completedFocus = "completed_focus"
        case abandonedFocus = "abandoned_focus"
        case totalFocus = "total_focus"
        case completionRate = "completion_rate"
        case focusSec = "focus_sec"
    }
}

public struct GoalStatus: Codable, Sendable, Equatable {
    public var dailySessions: Int
    public var todayCompleted: Int
    public var dailyMet: Bool
    public var weeklyDays: Int
    public var weekActiveDays: Int
    public var weeklyMet: Bool

    public init(dailySessions: Int = 0, todayCompleted: Int = 0, dailyMet: Bool = true,
                weeklyDays: Int = 0, weekActiveDays: Int = 0, weeklyMet: Bool = true) {
        self.dailySessions = dailySessions
        self.todayCompleted = todayCompleted
        self.dailyMet = dailyMet
        self.weeklyDays = weeklyDays
        self.weekActiveDays = weekActiveDays
        self.weeklyMet = weeklyMet
    }

    enum CodingKeys: String, CodingKey {
        case dailySessions = "daily_sessions"
        case todayCompleted = "today_completed"
        case dailyMet = "daily_met"
        case weeklyDays = "weekly_days"
        case weekActiveDays = "week_active_days"
        case weeklyMet = "weekly_met"
    }
}

public struct FocusBucket: Codable, Sendable, Equatable, Identifiable {
    public var key: String
    public var focusMin: Int
    public var sessions: Int
    public var id: String { key }

    public init(key: String, focusMin: Int, sessions: Int) {
        self.key = key
        self.focusMin = focusMin
        self.sessions = sessions
    }

    enum CodingKeys: String, CodingKey {
        case key, sessions
        case focusMin = "focus_min"
    }
}
