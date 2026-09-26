import Foundation

/// The "Last 7 days" bar chart: focus minutes per logical day, oldest first,
/// with the daily goal as a line.
public struct WeekBars: Equatable, Sendable {
    public struct Bar: Equatable, Sendable, Identifiable {
        /// The server's day key ("2026-09-26").
        public let key: String
        /// Local midnight of the day.
        public let date: Date
        public let focusMin: Int
        public let sessions: Int
        public let isToday: Bool
        public var id: String { key }
    }

    /// Oldest first; the last bar is today.
    public let bars: [Bar]
    /// Daily goal in minutes (goal sessions × focus length); nil when the goal
    /// is off.
    public let goalMinutes: Int?
    /// The y-axis top: the tallest bar or the goal, with headroom, and never
    /// under an hour, so a first 25-minute session doesn't fill the card.
    public let yMax: Int

    /// No day in the window has focus.
    public var isEmpty: Bool { bars.allSatisfy { $0.focusMin == 0 } }
    public var totalMinutes: Int { bars.reduce(0) { $0 + $1.focusMin } }
    /// Days that reached the goal; nil when the goal is off.
    public var daysAtGoal: Int? {
        goalMinutes.map { goal in bars.filter { $0.focusMin >= goal }.count }
    }

    /// `stats.history` is newest first (index 0 is today); `focusMinutes` is
    /// the configured focus length (the server doesn't send it with stats).
    public init(stats: PomoStats, focusMinutes: Int?, calendar: Calendar) {
        let ordered = Array(stats.history.reversed())
        let todayKey = stats.history.first?.date ?? stats.today.date
        bars = ordered.compactMap { day in
            guard let date = DayKey.date(day.date, in: calendar) else { return nil }
            return Bar(key: day.date, date: date, focusMin: day.focusMin,
                       sessions: day.completedFocus, isToday: day.date == todayKey)
        }
        goalMinutes = Self.goalMinutes(sessions: stats.goal.dailySessions, focusMinutes: focusMinutes)
        yMax = Self.axisTop(max(bars.map(\.focusMin).max() ?? 0, goalMinutes ?? 0))
    }

    /// Goal sessions × focus length; nil when either is unknown or the goal
    /// is off (0 sessions).
    public static func goalMinutes(sessions: Int, focusMinutes: Int?) -> Int? {
        guard sessions > 0, let focusMinutes, focusMinutes > 0 else { return nil }
        return sessions * focusMinutes
    }

    /// A round axis top above `value`: +15 % headroom, rounded up to 30 min,
    /// at least 60.
    public static func axisTop(_ value: Int) -> Int {
        let padded = Int((Double(value) * 1.15).rounded(.up))
        let step = 30
        return max(60, ((padded + step - 1) / step) * step)
    }
}

/// The "12 weeks" trend: focus minutes per ISO week, zero-filled, oldest
/// first, ending with the current week.
public struct WeeklyTrend: Equatable, Sendable {
    public struct Point: Equatable, Sendable, Identifiable {
        /// "2026-W39".
        public let key: String
        /// Monday of the week, local midnight.
        public let weekStart: Date
        public let focusMin: Int
        public let sessions: Int
        public var id: String { key }
    }

    /// Weeks shown even when the history is shorter, so one week of data is a
    /// line with context and not a lone dot.
    public static let minimumWeeks = 4

    public let points: [Point]
    /// Mean of the non-empty weeks, drawn as a reference line; nil with fewer
    /// than two of them.
    public let averageMinutes: Int?

    public var isEmpty: Bool { points.allSatisfy { $0.focusMin == 0 } }

    /// A server before 0.28 sends no `weekly` at all, which decodes as empty;
    /// a current one always has this week's focus in it. So focus in the
    /// 7-day history with no weeks means the server is too old, not that
    /// there's no data.
    public static func serverLacksWeekly(_ stats: PomoStats) -> Bool {
        stats.weekly.isEmpty && stats.history.contains { $0.focusMin > 0 }
    }

    public var last: Point? { points.last }
    public var yMax: Int { WeekBars.axisTop(points.map(\.focusMin).max() ?? 0) }

    /// `weekly` is the server's list (only weeks with focus). Leading empty
    /// weeks are dropped down to `minimumWeeks`; the window is at most
    /// `weeks` long and ends with the week of `now`.
    public init(weekly: [FocusBucket], now: Date, calendar: Calendar, weeks: Int = 12) {
        let byKey = Dictionary(weekly.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let iso = DayKey.isoCalendar(like: calendar)
        let thisWeek = DayKey.weekStart(DayKey.weekKey(now, in: calendar), in: calendar) ?? now
        var all: [Point] = []
        for back in (0..<max(1, weeks)).reversed() {
            guard let start = iso.date(byAdding: .weekOfYear, value: -back, to: thisWeek) else { continue }
            let key = DayKey.weekKey(start, in: calendar)
            let b = byKey[key]
            all.append(Point(key: key, weekStart: iso.startOfDay(for: start),
                             focusMin: b?.focusMin ?? 0, sessions: b?.sessions ?? 0))
        }
        let firstData = all.firstIndex { $0.focusMin > 0 } ?? all.count
        let keep = max(Self.minimumWeeks, all.count - firstData)
        points = Array(all.suffix(keep))
        let active = points.filter { $0.focusMin > 0 }
        averageMinutes = active.count >= 2
            ? Int((Double(active.reduce(0) { $0 + $1.focusMin }) / Double(active.count)).rounded())
            : nil
    }
}
