import Foundation

/// The server's bucket keys as dates. Days are "2026-09-26" (a logical day)
/// and ISO weeks "2026-W39". Everything goes through `Calendar` arithmetic,
/// never "+ 86 400 s", so a 23- or 25-hour DST day lands on the right key.
public enum DayKey {
    /// Local midnight of a "yyyy-MM-dd" key in `calendar`'s time zone; nil
    /// when the key doesn't parse.
    public static func date(_ key: String, in calendar: Calendar) -> Date? {
        let parts = key.split(separator: "-")
        guard parts.count == 3, let y = Int(parts[0]), let m = Int(parts[1]), let d = Int(parts[2]),
              (1...12).contains(m), (1...31).contains(d)
        else { return nil }
        let comps = DateComponents(year: y, month: m, day: d)
        guard let date = calendar.date(from: comps),
              calendar.component(.day, from: date) == d
        else { return nil }
        return calendar.startOfDay(for: date)
    }

    /// The "yyyy-MM-dd" key of the calendar day containing `date`.
    public static func key(_ date: Date, in calendar: Calendar) -> String {
        let c = calendar.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
    }

    /// The "yyyy-Www" ISO week key of `date` (as Go's `time.ISOWeek`).
    public static func weekKey(_ date: Date, in calendar: Calendar) -> String {
        let iso = isoCalendar(like: calendar)
        let c = iso.dateComponents([.yearForWeekOfYear, .weekOfYear], from: date)
        return String(format: "%04d-W%02d", c.yearForWeekOfYear ?? 0, c.weekOfYear ?? 0)
    }

    /// Local midnight of the Monday that starts an ISO week key ("2026-W39");
    /// nil when the key doesn't parse.
    public static func weekStart(_ key: String, in calendar: Calendar) -> Date? {
        let parts = key.split(separator: "-")
        guard parts.count == 2, let y = Int(parts[0]), parts[1].first == "W",
              let w = Int(parts[1].dropFirst()), (1...53).contains(w)
        else { return nil }
        let iso = isoCalendar(like: calendar)
        let comps = DateComponents(weekday: 2, weekOfYear: w, yearForWeekOfYear: y)
        return iso.date(from: comps).map { iso.startOfDay(for: $0) }
    }

    /// `n` consecutive calendar days ending with the day of `end`, oldest
    /// first, each at local midnight.
    public static func days(endingAt end: Date, count n: Int, in calendar: Calendar) -> [Date] {
        let last = calendar.startOfDay(for: end)
        return (0..<max(0, n)).reversed().compactMap {
            calendar.date(byAdding: .day, value: -$0, to: last)
        }
    }

    static func isoCalendar(like calendar: Calendar) -> Calendar {
        var iso = Calendar(identifier: .iso8601)
        iso.timeZone = calendar.timeZone
        iso.locale = calendar.locale
        return iso
    }
}
