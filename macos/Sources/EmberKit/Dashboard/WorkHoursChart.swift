import Foundation

/// The "Work hours" chart: one row per logical day, a bar from the first to
/// the last worked minute on an hour-of-day axis.
public struct WorkHoursChart: Equatable, Sendable {
    public struct Row: Equatable, Sendable, Identifiable {
        public let key: String
        /// Local midnight of the day.
        public let date: Date
        /// Wall-clock hours from the day's midnight (9.5 = 09:30). Past
        /// midnight continues above 24 (01:15 the next day = 25.25). nil on a
        /// day without work.
        public let start: Double?
        public let end: Double?
        public let activeSec: Int
        public let spanSec: Int
        public let sessions: Int
        public let isToday: Bool
        public var id: String { key }
        public var hasWork: Bool { start != nil && end != nil }
    }

    /// Rows shown even when fewer days have work.
    public static let minimumRows = 7

    /// Oldest first, so today is the bottom row.
    public let rows: [Row]
    /// Hour-of-day axis fitted to the worked range (see `domain`).
    public let hourDomain: ClosedRange<Double>
    /// Now on today's row, when it's inside the axis.
    public let nowHour: Double?

    public var isEmpty: Bool { !rows.contains(where: \.hasWork) }

    /// `days` is the server's list (newest first). Older days without work
    /// are dropped down to `minimumRows`.
    public init(days: [WorkHours.Day], now: Date, calendar: Calendar) {
        let todayKey = days.first?.date
        var all: [Row] = days.reversed().compactMap { d in
            guard let midnight = DayKey.date(d.date, in: calendar) else { return nil }
            let start = d.workStart.map { Self.wallHours($0, since: midnight, calendar: calendar) }
            let end = d.workEnd.map { Self.wallHours($0, since: midnight, calendar: calendar) }
            let valid = start != nil && end != nil
            return Row(key: d.date, date: midnight, start: valid ? start : nil,
                       end: valid ? max(end!, start!) : nil,
                       activeSec: d.activeSec, spanSec: d.spanSec, sessions: d.sessions,
                       isToday: d.date == todayKey)
        }
        if let firstWork = all.firstIndex(where: \.hasWork) {
            let keep = max(Self.minimumRows, all.count - firstWork)
            all = Array(all.suffix(keep))
        } else {
            all = Array(all.suffix(Self.minimumRows))
        }
        rows = all
        var nowOnToday: Double?
        if let today = all.last, today.isToday {
            nowOnToday = Self.wallHours(now, since: today.date, calendar: calendar)
        }
        let starts = all.compactMap(\.start), ends = all.compactMap(\.end)
        let domain = Self.domain(low: starts.min(), high: ends.max(), now: nowOnToday)
        hourDomain = domain
        nowHour = nowOnToday.flatMap { domain.contains($0) ? $0 : nil }
    }

    /// The hour axis: the worked range padded by an hour each side, stretched
    /// to show now on today's row, at least `minimumSpan` hours and within
    /// 0...30. Without work it's 08...18.
    public static func domain(low: Double?, high: Double?, now: Double?) -> ClosedRange<Double> {
        guard var lo = low, var hi = high else { return 8...18 }
        if let now, now >= lo - 1, now <= 30 { hi = max(hi, now) }
        lo = max(0, (lo - 1).rounded(.down))
        hi = min(30, (hi + 1).rounded(.up))
        if hi - lo < minimumSpan {
            let grow = minimumSpan - (hi - lo)
            hi = min(30, hi + (grow / 2).rounded(.up))
            lo = max(0, hi - minimumSpan)
            hi = max(hi, lo + minimumSpan)
        }
        return lo...hi
    }

    /// The narrowest the hour axis gets.
    public static let minimumSpan: Double = 8

    /// Wall-clock hours of `date` counted from `midnight`'s calendar day:
    /// 09:00 is 9 even on a 23- or 25-hour DST day, and the next day's 01:00
    /// is 25.
    public static func wallHours(_ date: Date, since midnight: Date, calendar: Calendar) -> Double {
        let c = calendar.dateComponents([.hour, .minute, .second], from: date)
        let dayOffset = calendar.dateComponents([.day], from: calendar.startOfDay(for: midnight),
                                                to: calendar.startOfDay(for: date)).day ?? 0
        let h = Double(c.hour ?? 0) + Double(c.minute ?? 0) / 60 + Double(c.second ?? 0) / 3600
        return Double(dayOffset) * 24 + h
    }
}
