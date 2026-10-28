import Foundation

public struct DayStrip: Equatable, Sendable {
    public struct Span: Equatable, Sendable {
        public let from: Double
        public let to: Double
    }

    public struct Row: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        public let isToday: Bool
        public let isWeekend: Bool
        public let start: Double?
        public let stop: Double?
        public let target: Double?
        public let spans: [Span]
        public let dots: [Double]
        public let auto: Bool
        public let inRange: Bool?
        public let onSec: Int
        public let breakSec: Int
        public let repliesOn: Int
        public let repliesOff: Int
        public var id: String { key }
        public var hasClock: Bool { start != nil }
    }

    public static let axis: ClosedRange<Double> = 6...28
    public static let dayStartHour = 4
    public static let defaultCount = 28

    public let rows: [Row]

    public var isEmpty: Bool { !rows.contains { $0.hasClock || $0.repliesOff > 0 } }

    public init(days: [DayHistory.Day], count: Int = defaultCount, now: Date, calendar: Calendar) {
        let ordered = days.sorted { $0.date < $1.date }.suffix(max(1, count))
        let todayKey = days.map(\.date).max()
        rows = ordered.compactMap { d in
            guard let midnight = DayKey.date(d.date, in: calendar) else { return nil }
            return Self.row(d, midnight: midnight, isToday: d.date == todayKey, now: now, calendar: calendar)
        }
    }

    private static func row(_ d: DayHistory.Day, midnight: Date, isToday: Bool, now: Date,
                            calendar: Calendar) -> Row {
        let wall = { (date: Date) in Self.wallHours(date, since: midnight, calendar: calendar) }
        let start = d.start.map(wall)
        let stop = d.stop.map(wall)
        let end = stop ?? (isToday ? wall(now) : start)
        var spans: [Span] = []
        if let start, let end {
            var cursor = start
            for b in d.breaks.sorted(by: { $0.start < $1.start }) {
                let from = wall(b.start), to = b.end.map(wall) ?? end
                if from > cursor { spans.append(Span(from: cursor, to: min(from, end))) }
                cursor = max(cursor, to)
            }
            if end > cursor { spans.append(Span(from: cursor, to: end)) }
        }
        let dotTimes = d.repliesOffAt.isEmpty
            ? (d.repliesOff > 0 ? [d.firstReply, d.lastReply].compactMap { $0 }.filter { reply in
                guard let from = d.start else { return true }
                return reply < from || reply > (d.stop ?? now)
            } : [])
            : d.repliesOffAt
        let weekday = calendar.component(.weekday, from: midnight)
        return Row(key: d.date, date: midnight, isToday: isToday, isWeekend: weekday == 1 || weekday == 7,
                   start: start, stop: stop,
                   target: d.stopTarget.flatMap { resolveTarget($0, midnight: midnight, calendar: calendar) },
                   spans: spans.compactMap(clamped), dots: dotTimes.map { clamp(wall($0)) },
                   auto: d.auto, inRange: d.inRange, onSec: d.onSec, breakSec: d.breakSec,
                   repliesOn: d.repliesOn, repliesOff: d.repliesOff)
    }

    private static func clamped(_ s: Span) -> Span? {
        let from = clamp(s.from), to = clamp(s.to)
        return to > from ? Span(from: from, to: to) : nil
    }

    private static func clamp(_ h: Double) -> Double {
        min(max(h, axis.lowerBound), axis.upperBound)
    }

    public static func resolveTarget(_ raw: String, midnight: Date, calendar: Calendar) -> Double? {
        if let date = try? Date(raw, strategy: .iso8601) {
            return wallHours(date, since: midnight, calendar: calendar)
        }
        let parts = raw.split(separator: ":").compactMap { Int($0) }
        guard parts.count >= 2, (0...24).contains(parts[0]), (0..<60).contains(parts[1]) else { return nil }
        let h = Double(parts[0]) + Double(parts[1]) / 60
        return h < Double(dayStartHour) ? h + 24 : h
    }

    public static func wallHours(_ date: Date, since midnight: Date, calendar: Calendar) -> Double {
        let c = calendar.dateComponents([.hour, .minute, .second], from: date)
        let dayOffset = calendar.dateComponents([.day], from: calendar.startOfDay(for: midnight),
                                                to: calendar.startOfDay(for: date)).day ?? 0
        let h = Double(c.hour ?? 0) + Double(c.minute ?? 0) / 60 + Double(c.second ?? 0) / 3600
        return Double(dayOffset) * 24 + h
    }
}

public struct StopTrend: Equatable, Sendable {
    public struct Point: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        public let stop: Double
        public let target: Double?
        public let auto: Bool
        public var id: String { key }
    }

    public let points: [Point]
    public let hourDomain: ClosedRange<Double>

    public var isEmpty: Bool { points.isEmpty }

    public var averageMinutesPastTarget: Int? {
        let gaps = points.compactMap { p in p.target.map { (p.stop - $0) * 60 } }
        guard !gaps.isEmpty else { return nil }
        return Int((gaps.reduce(0, +) / Double(gaps.count)).rounded())
    }

    public init(strip: DayStrip) {
        points = strip.rows.compactMap { r in
            r.stop.map { Point(key: r.key, date: r.date, stop: $0, target: r.target, auto: r.auto) }
        }
        let values = points.flatMap { [$0.stop] + ($0.target.map { [$0] } ?? []) }
        guard let lo = values.min(), let hi = values.max() else {
            hourDomain = 20...28
            return
        }
        var low = max(DayStrip.axis.lowerBound, (lo - 1).rounded(.down))
        var high = min(DayStrip.axis.upperBound, (hi + 1).rounded(.up))
        if high - low < 4 {
            high = min(DayStrip.axis.upperBound, low + 4)
            low = max(DayStrip.axis.lowerBound, high - 4)
        }
        hourDomain = low...high
    }
}

public struct DayWeekTable: Equatable, Sendable {
    public struct Row: Equatable, Sendable, Identifiable {
        public let key: String
        public let weekStart: Date
        public let isCurrent: Bool
        public let daysWorked: Int
        public let daysInRange: Int
        public let onSec: Int
        public let breakSec: Int
        public let repliesOff: Int
        public var id: String { key }
    }

    public let rows: [Row]
    public let thisWeek: Row?

    public var isEmpty: Bool { !rows.contains { $0.daysWorked > 0 || $0.repliesOff > 0 } }

    public init(weeks: [DayWeeks.Week], count: Int = 8, now: Date, calendar: Calendar) {
        let currentKey = DayKey.weekKey(now, in: calendar)
        let dated: [Row] = weeks.compactMap { w in
            guard let start = DayKey.weekStart(w.key, in: calendar) ?? DayKey.date(w.key, in: calendar) else { return nil }
            let key = DayKey.weekKey(start, in: calendar)
            return Row(key: key, weekStart: start, isCurrent: key == currentKey, daysWorked: w.daysWorked,
                       daysInRange: w.daysInRange, onSec: w.onSec, breakSec: w.breakSec, repliesOff: w.repliesOff)
        }
        rows = Array(dated.sorted { $0.weekStart > $1.weekStart }.prefix(max(1, count)))
        thisWeek = rows.first { $0.isCurrent }
    }
}
