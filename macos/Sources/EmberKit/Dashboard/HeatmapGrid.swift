import Foundation

/// The "When you focus" weekday × hour grid, with rows in the locale's week
/// order (Monday first in most of Europe, Sunday first in the US).
public struct HeatmapGrid: Equatable, Sendable {
    public struct Cell: Equatable, Sendable, Identifiable {
        /// 0 at the top; follows `calendar.firstWeekday`.
        public let row: Int
        /// The server's weekday, 0 = Sunday.
        public let weekday: Int
        public let hour: Int
        public let minutes: Int
        public var id: Int { weekday * 24 + hour }
    }

    /// 7 × 24 cells, row-major.
    public let cells: [Cell]
    /// Short weekday names, top row first ("Mon" … "Sun").
    public let rowLabels: [String]
    public let maxMinutes: Int

    public var isEmpty: Bool { maxMinutes == 0 }

    /// The (weekday, hour) with the most focus; nil when empty. Ties go to
    /// the earliest in the week order.
    public var peak: Cell? {
        cells.max { a, b in a.minutes < b.minutes || (a.minutes == b.minutes && a.id > b.id) }
            .flatMap { $0.minutes > 0 ? $0 : nil }
    }

    public init(heatmap: Heatmap, calendar: Calendar) {
        let order = Self.weekdayOrder(firstWeekday: calendar.firstWeekday)
        let symbols = calendar.shortWeekdaySymbols
        var cells: [Cell] = []
        cells.reserveCapacity(7 * 24)
        for (row, weekday) in order.enumerated() {
            for hour in 0..<24 {
                cells.append(Cell(row: row, weekday: weekday, hour: hour,
                                  minutes: heatmap.minutes(weekday: weekday, hour: hour)))
            }
        }
        self.cells = cells
        rowLabels = order.map { symbols.indices.contains($0) ? symbols[$0] : "" }
        maxMinutes = cells.map(\.minutes).max() ?? 0
    }

    /// Server weekdays (0 = Sunday) in display order for a `Calendar`
    /// `firstWeekday` (1 = Sunday, 2 = Monday).
    public static func weekdayOrder(firstWeekday: Int) -> [Int] {
        let first = ((firstWeekday - 1) % 7 + 7) % 7
        return (0..<7).map { (first + $0) % 7 }
    }
}

/// The consistency strip under the heatmap: one cell per day for the last
/// `weeks` weeks, a column per week and a row per weekday (locale order).
public struct CalendarStrip: Equatable, Sendable {
    public struct Cell: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        /// 0 is the oldest week.
        public let column: Int
        /// 0 is the locale's first weekday.
        public let row: Int
        public let focusMin: Int
        public let sessions: Int
        public var id: String { key }
    }

    /// Up to `weeks × 7` cells, oldest first, ending today: the days after
    /// today in the current week are left out.
    public let cells: [Cell]
    public let weeks: Int
    public let maxMinutes: Int
    public var activeDays: Int { cells.filter { $0.focusMin > 0 }.count }
    public var isEmpty: Bool { maxMinutes == 0 }

    /// `today` is the logical day the server counts as today, when known
    /// (`PomoStats.today.date`); else the calendar day of `now`.
    public init(calendar buckets: [FocusBucket], today: String? = nil, now: Date,
                in calendar: Calendar, weeks: Int = 12) {
        let byKey = Dictionary(buckets.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let end = today.flatMap { DayKey.date($0, in: calendar) } ?? calendar.startOfDay(for: now)
        // Row of `end`, then back to the first weekday of the oldest week.
        let endRow = Self.row(of: end, calendar: calendar)
        let count = (max(1, weeks) - 1) * 7 + endRow + 1
        var cells: [Cell] = []
        for (i, day) in DayKey.days(endingAt: end, count: count, in: calendar).enumerated() {
            let key = DayKey.key(day, in: calendar)
            let b = byKey[key]
            cells.append(Cell(key: key, date: day, column: i / 7, row: i % 7,
                              focusMin: b?.focusMin ?? 0, sessions: b?.sessions ?? 0))
        }
        self.cells = cells
        self.weeks = max(1, weeks)
        maxMinutes = cells.map(\.focusMin).max() ?? 0
    }

    /// 0-based position of `date`'s weekday in the locale's week.
    static func row(of date: Date, calendar: Calendar) -> Int {
        let wd = calendar.component(.weekday, from: date)   // 1 = Sunday
        return ((wd - calendar.firstWeekday) % 7 + 7) % 7
    }
}
