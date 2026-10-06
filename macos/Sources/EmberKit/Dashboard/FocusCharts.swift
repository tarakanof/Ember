import Foundation

public struct WeekBars: Equatable, Sendable {
    public struct Bar: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        public let focusMin: Int
        public let sessions: Int
        public let isToday: Bool
        public var id: String { key }
    }

    public let bars: [Bar]
    public let goalMinutes: Int?
    public let yMax: Int

    public var isEmpty: Bool { bars.allSatisfy { $0.focusMin == 0 } }
    public var totalMinutes: Int { bars.reduce(0) { $0 + $1.focusMin } }
    public var daysAtGoal: Int? {
        goalMinutes.map { goal in bars.filter { $0.focusMin >= goal }.count }
    }

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

    public static func goalMinutes(sessions: Int, focusMinutes: Int?) -> Int? {
        guard sessions > 0, let focusMinutes, focusMinutes > 0 else { return nil }
        return sessions * focusMinutes
    }

    public static func axisTop(_ value: Int) -> Int {
        let padded = Int((Double(value) * 1.15).rounded(.up))
        let step = 30
        return max(60, ((padded + step - 1) / step) * step)
    }
}

public struct WeeklyTrend: Equatable, Sendable {
    public struct Point: Equatable, Sendable, Identifiable {
        public let key: String
        public let weekStart: Date
        public let focusMin: Int
        public let sessions: Int
        public var id: String { key }
    }

    public static let minimumWeeks = 4

    public let points: [Point]
    public let averageMinutes: Int?

    public var isEmpty: Bool { points.allSatisfy { $0.focusMin == 0 } }

    public static func serverLacksWeekly(_ stats: PomoStats) -> Bool {
        stats.weekly.isEmpty && stats.history.contains { $0.focusMin > 0 }
    }

    public var last: Point? { points.last }
    public var yMax: Int { WeekBars.axisTop(points.map(\.focusMin).max() ?? 0) }

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
        let active = points.dropLast().filter { $0.focusMin > 0 }
        averageMinutes = active.count >= 2
            ? Int((Double(active.reduce(0) { $0 + $1.focusMin }) / Double(active.count)).rounded())
            : nil
    }
}
