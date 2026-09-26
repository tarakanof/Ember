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
    /// Hour-of-day axis: 6...24, widened to whole hours around any span
    /// outside it (never beyond 0...30).
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
        let starts = all.compactMap(\.start), ends = all.compactMap(\.end)
        let lo = min(6, (starts.min() ?? 6).rounded(.down))
        let hi = max(24, (ends.max() ?? 24).rounded(.up))
        hourDomain = max(0, lo)...min(30, hi)
        if let today = all.last, today.isToday {
            let h = Self.wallHours(now, since: today.date, calendar: calendar)
            nowHour = hourDomain.contains(h) ? h : nil
        } else {
            nowHour = nil
        }
    }

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
