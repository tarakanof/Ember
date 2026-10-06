import Foundation

public struct WorkHoursChart: Equatable, Sendable {
    public struct Row: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        public let start: Double?
        public let end: Double?
        public let activeSec: Int
        public let spanSec: Int
        public let sessions: Int
        public let isToday: Bool
        public var id: String { key }
        public var hasWork: Bool { start != nil && end != nil }
    }

    public static let minimumRows = 7

    public let rows: [Row]
    public let hourDomain: ClosedRange<Double>
    public let nowHour: Double?

    public var isEmpty: Bool { !rows.contains(where: \.hasWork) }

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

    public static let minimumSpan: Double = 8

    public static func wallHours(_ date: Date, since midnight: Date, calendar: Calendar) -> Double {
        let c = calendar.dateComponents([.hour, .minute, .second], from: date)
        let dayOffset = calendar.dateComponents([.day], from: calendar.startOfDay(for: midnight),
                                                to: calendar.startOfDay(for: date)).day ?? 0
        let h = Double(c.hour ?? 0) + Double(c.minute ?? 0) / 60 + Double(c.second ?? 0) / 3600
        return Double(dayOffset) * 24 + h
    }
}
