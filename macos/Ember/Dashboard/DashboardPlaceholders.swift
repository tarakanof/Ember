import Foundation
import EmberKit

@MainActor
enum DashboardPlaceholders {
    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    private static func decode<T: Decodable>(_ json: String) -> T {
        try! decoder.decode(T.self, from: Data(json.utf8))
    }

    private static func iso(_ d: Date) -> String { d.formatted(.iso8601) }

    private static let calendar = Calendar.current
    private static let today = calendar.startOfDay(for: .now)
    private static func day(_ back: Int) -> Date { calendar.date(byAdding: .day, value: -back, to: today)! }
    private static let minutes = [75, 50, 125, 25, 100, 0, 50]

    static let stats: PomoStats = {
        let history: [PomoDayStat] = minutes.enumerated().map { i, m in
            decode(#"{"date":"\#(DayKey.key(day(i), in: calendar))","completed_focus":\#(m / 25),"focus_min":\#(m)}"#)
        }
        let weekly = (0..<8).reversed().map { back -> FocusBucket in
            let d = calendar.date(byAdding: .weekOfYear, value: -back, to: today)!
            return FocusBucket(key: DayKey.weekKey(d, in: calendar), focusMin: [300, 420, 360, 480, 390, 450, 510, 240][7 - back],
                               sessions: 12)
        }
        return PomoStats(today: history[0], history: history, streak: 3, longestStreak: 9,
                         goal: GoalStatus(dailySessions: 6, todayCompleted: 3), weekly: weekly)
    }()

    static let heatmap: Heatmap = {
        let grid = (0..<7).map { wd in
            (0..<24).map { h in (1...5).contains(wd) && [9, 10, 11, 14, 15, 16].contains(h) ? 25 * (1 + (wd + h) % 3) : 0 }
        }
        let cal = (0..<60).compactMap { back -> FocusBucket? in
            back % 3 == 2 ? nil : FocusBucket(key: DayKey.key(day(back), in: calendar), focusMin: 25 * (1 + back % 4), sessions: 1)
        }
        return Heatmap(grid: grid, calendar: cal.reversed(), days: 84)
    }()

    static let dayState: DayState = decode(#"{"enabled":true,"state":"on","started_at":"\#(iso(today.addingTimeInterval(10 * 3600)))","stop_at":"00:00","on_sec":14400,"replies_off_last_night":4}"#)

    static let dayHistory: DayHistory = {
        let days = (0..<28).map { back -> String in
            let d = day(back)
            let start = iso(d.addingTimeInterval(11 * 3600)), stop = iso(d.addingTimeInterval(24.3 * 3600))
            return #"{"date":"\#(DayKey.key(d, in: calendar))","start":"\#(start)","stop":"\#(stop)","stop_target":"00:00","on_sec":43200,"break_sec":3600,"in_range":false,"replies_on":80,"replies_off":2}"#
        }
        return decode(#"{"days":[\#(days.joined(separator: ","))]}"#)
    }()

    static let dayWeeks: DayWeeks = {
        let weeks = (0..<8).map { back -> String in
            let d = calendar.date(byAdding: .weekOfYear, value: -back, to: today)!
            return #"{"week":"\#(DayKey.weekKey(d, in: calendar))","days_worked":5,"days_in_range":3,"on_sec":144000,"break_sec":14400,"replies_off":12}"#
        }
        return decode(#"{"weeks":[\#(weeks.joined(separator: ","))]}"#)
    }()

    static let snapshot: Snapshot = {
        let rows = (0..<3).map { i in
            #"{"source":"mac","tool":"claude","session":"\#(i)","state":"running","activity":"Working on something","context_pct":30,"rate_window_pct":20,"updated_at":"\#(iso(.now))"}"#
        }
        return decode(#"{"sessions":[\#(rows.joined(separator: ","))]}"#)
    }()

    static let activity: ActivitySummary = {
        let rows = (0..<7).reversed().flatMap { back -> [String] in
            let d = day(back)
            return ["a", "b"].map { src in
                #"{"day":"\#(DayKey.key(d, in: calendar))","date":"\#(iso(d))","source":"\#(src)","source_color":null,"active_sec":\#(1800 + back * 600),"sessions":1,"attention":0}"#
            }
        }
        let window = #"{"from":"\#(iso(today))","to":"\#(iso(.now))","total":{"active_sec":3600,"sessions":2,"attention":0},"by_tool":[],"by_source":[]}"#
        return decode(#"{"generated_at":"\#(iso(.now))","recording":true,"days":7,"span_gap_sec":300,"today":\#(window),"period":\#(window),"daily":[],"daily_by_source":[\#(rows.joined(separator: ","))]}"#)
    }()

    static let weather: WeatherState = decode(#"""
    {"generated_at":"\#(iso(.now))","enabled":true,"provider":"open-meteo","units":"metric","location_name":"Location",
     "current":{"fetched_at":"\#(iso(.now))","stale":false,"condition":"clouds","severe":false,"temp_c":15,"hourly":[]},
     "air":null,"sun":null}
    """#)
}
