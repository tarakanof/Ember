import Foundation

public struct HeatmapGrid: Equatable, Sendable {
    public struct Cell: Equatable, Sendable, Identifiable {
        public let row: Int
        public let weekday: Int
        public let hour: Int
        public let minutes: Int
        public var id: Int { weekday * 24 + hour }
    }

    public let cells: [Cell]
    public let rowLabels: [String]
    public let maxMinutes: Int

    public var isEmpty: Bool { maxMinutes == 0 }

    public var peak: Cell? {
        var best: Cell?
        for c in cells where c.minutes > (best?.minutes ?? 0) { best = c }
        return best
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

    public static func weekdayOrder(firstWeekday: Int) -> [Int] {
        let first = ((firstWeekday - 1) % 7 + 7) % 7
        return (0..<7).map { (first + $0) % 7 }
    }
}

public struct CalendarStrip: Equatable, Sendable {
    public struct Cell: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        public let column: Int
        public let row: Int
        public let focusMin: Int
        public let sessions: Int
        public var id: String { key }
    }

    public let cells: [Cell]
    public let weeks: Int
    public let maxMinutes: Int
    public var activeDays: Int { cells.filter { $0.focusMin > 0 }.count }
    public var isEmpty: Bool { maxMinutes == 0 }

    public init(calendar buckets: [FocusBucket], today: String? = nil, now: Date,
                in calendar: Calendar, weeks: Int = 12) {
        let byKey = Dictionary(buckets.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let end = today.flatMap { DayKey.date($0, in: calendar) } ?? calendar.startOfDay(for: now)
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

    static func row(of date: Date, calendar: Calendar) -> Int {
        let wd = calendar.component(.weekday, from: date)
        return ((wd - calendar.firstWeekday) % 7 + 7) % 7
    }
}
