#if DEBUG
import Foundation
import EmberKit

@MainActor
enum DashboardFixtures {
    static let calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "Europe/Amsterdam")!
        c.locale = Locale(identifier: "en_US")
        c.firstWeekday = 2
        return c
    }()

    static let now = date("2026-09-26T10:30:00+02:00")
    static let loadedAt = date("2026-09-26T10:29:30+02:00")

    static var established: DashboardData {
        var d = DashboardData()
        d.snapshot = .loaded(snapshot, at: loadedAt)
        d.pomodoro = .loaded(pomo(phase: "focus", running: true, remaining: 1122, planned: 1500, round: 2), at: loadedAt)
        d.stats = .loaded(stats(days: 120), at: loadedAt)
        d.usage = .loaded(golden("usage"), at: loadedAt)
        d.meetings = .loaded(meetings, at: loadedAt)
        d.screen = .loaded(ledFrame, at: loadedAt)
        d.clockHealth = .loaded(golden("clock_health"), at: loadedAt)
        d.weather = .loaded(weather, at: loadedAt)
        d.activity = .loaded(activity(days: 7), at: loadedAt)
        d.dayState = .loaded(dayState, at: loadedAt)
        d.dayHistory = .loaded(dayHistory(days: 28), at: loadedAt)
        d.dayWeeks = .loaded(dayWeeks(from: dayHistory(days: 28)), at: loadedAt)
        d.heatmap = .loaded(heatmap(days: 84), at: loadedAt)
        d.reminders = [UpcomingItem(id: "r|1", kind: .reminder, title: "Call the bank",
                                    date: now.addingTimeInterval(4 * 3600))]
        d.remindersEnabled = true
        d.pomoConfig = SettingsModels.defaultPomoConfig
        d.meetingsEnabled = true
        d.now = now
        d.calendar = calendar
        return d
    }

    static var newUser: DashboardData {
        var d = established
        d.pomodoro = .loaded(pomo(phase: "idle", running: false, remaining: 0, planned: 0, round: 0), at: loadedAt)
        d.stats = .loaded(stats(days: 2), at: loadedAt)
        d.activity = .loaded(golden("activity_summary"), at: loadedAt)
        d.dayState = .loaded(dayState(phase: "off"), at: loadedAt)
        d.dayHistory = .loaded(dayHistory(days: 2), at: loadedAt)
        d.dayWeeks = .loaded(dayWeeks(from: dayHistory(days: 2)), at: loadedAt)
        d.heatmap = .loaded(heatmap(days: 2), at: loadedAt)
        d.meetings = .loaded(MeetingsState(), at: loadedAt)
        d.reminders = []
        return d
    }

    static var oldServer: DashboardData {
        var d = established
        d.usage = .failed(.featureOff, last: nil, lastAt: nil)
        d.activity = .failed(.featureOff, last: nil, lastAt: nil)
        d.clockHealth = .failed(.featureOff, last: nil, lastAt: nil)
        d.weather = .failed(.featureOff, last: nil, lastAt: nil)
        d.dayState = .failed(.featureOff, last: nil, lastAt: nil)
        var s = stats(days: 120)
        s.weekly = []
        s.completion = CompletionStat()
        s.goal = GoalStatus()
        s.longestStreak = s.streak
        d.stats = .loaded(s, at: loadedAt)
        return d
    }

    static var dayOff: DashboardData {
        var d = established
        d.dayState = .loaded(dayState(phase: "off", enabled: false), at: loadedAt)
        return d
    }

    static var pomodoroOff: DashboardData {
        var d = established
        d.pomodoro = .failed(.featureOff, last: nil, lastAt: nil)
        d.stats = .failed(.featureOff, last: nil, lastAt: nil)
        d.heatmap = .failed(.featureOff, last: nil, lastAt: nil)
        return d
    }

    static var stale: DashboardData {
        var d = established
        d.connection = .offline(since: now.addingTimeInterval(-180))
        let since = now.addingTimeInterval(-180)
        d.snapshot = d.snapshot.afterFailure(.offline)
        d.stats = .failed(.offline, last: d.stats.value, lastAt: since)
        d.clockHealth = .failed(.offline, last: d.clockHealth.value, lastAt: since)
        d.screen = .failed(.offline, last: d.screen.value, lastAt: since)
        return d
    }

    static var offline: DashboardData {
        var d = DashboardData()
        d.connection = .offline(since: now.addingTimeInterval(-600))
        d.snapshot = .failed(.offline, last: nil, lastAt: nil)
        d.pomodoro = .failed(.offline, last: nil, lastAt: nil)
        d.stats = .failed(.offline, last: nil, lastAt: nil)
        d.usage = .failed(.offline, last: nil, lastAt: nil)
        d.meetings = .failed(.offline, last: nil, lastAt: nil)
        d.screen = .failed(.offline, last: nil, lastAt: nil)
        d.clockHealth = .failed(.offline, last: nil, lastAt: nil)
        d.weather = .failed(.offline, last: nil, lastAt: nil)
        d.activity = .failed(.offline, last: nil, lastAt: nil)
        d.dayState = .failed(.offline, last: nil, lastAt: nil)
        d.dayHistory = .failed(.offline, last: nil, lastAt: nil)
        d.dayWeeks = .failed(.offline, last: nil, lastAt: nil)
        d.heatmap = .failed(.offline, last: nil, lastAt: nil)
        d.now = now
        d.calendar = calendar
        return d
    }

    static var loading: DashboardData {
        var d = DashboardData()
        d.now = now
        d.calendar = calendar
        return d
    }

    static func golden<T: Decodable>(_ name: String) -> T {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        do {
            return try decoder.decode(T.self, from: Data(contentsOf: goldenURL(name)))
        } catch {
            fatalError("fixture \(name): \(error)")
        }
    }

    private static func goldenURL(_ name: String) -> URL {
        let rel = "cmd/ember/testdata/dashboard/\(name).json"
        if let root = ProcessInfo.processInfo.environment["EMBER_REPO_ROOT"] {
            return URL(fileURLWithPath: root).appendingPathComponent(rel)
        }
        return URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent(rel)
    }

    static var weather: WeatherState {
        var w: WeatherState = golden("weather_state")
        let temps = [11.5, 12, 12.5, 13.5, 14.5, 15, 15.5, 15, 14, 13, 12, 11.5]
        let f = ISO8601DateFormatter()
        let points = temps.enumerated().map { i, t in
            #"{"time":"\#(f.string(from: date("2026-09-26T10:00:00+02:00").addingTimeInterval(Double(i) * 3600)))","temp_c":\#(t)}"#
        }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        w.current?.hourly = try! decoder.decode([WeatherState.TempPoint].self,
                                                from: Data("[\(points.joined(separator: ","))]".utf8))
        return w
    }

    static func pomo(phase: String, running: Bool, paused: Bool = false, remaining: Int, planned: Int,
                     round: Int) -> PomoState {
        try! JSONDecoder().decode(PomoState.self, from: Data(
            #"{"phase":"\#(phase)","running":\#(running),"paused":\#(paused),"remaining_sec":\#(remaining),"planned_sec":\#(planned),"round":\#(round)}"#.utf8))
    }

    static var meetings: MeetingsState {
        MeetingsState(upcoming: [
            .init(uid: "standup", title: "Standup", start: now.addingTimeInterval(12 * 60)),
            .init(uid: "anna", title: "1:1 with Anna", start: now.addingTimeInterval(5.5 * 3600)),
            .init(uid: "review", title: "Design review", start: now.addingTimeInterval(24 * 3600)),
        ], fetchedAt: loadedAt)
    }

    static var snapshot: Snapshot {
        let rows = [
            ##"{"source":"m4","tool":"claude","session":"a1","state":"running","activity":"Bash: go test ./... -race","context_pct":8,"rate_window_pct":14,"rate_reset_at":1790419200,"source_color":"#00C8C8","updated_at":"2026-09-26T10:29:50+02:00"}"##,
            ##"{"source":"m5","tool":"codex","session":"b2","state":"waiting","activity":"exec: swift test --package-path macos","context_pct":41,"rate_window_pct":3,"rate_reset_at":1790424600,"source_color":"#FF8800","updated_at":"2026-09-26T10:27:40+02:00"}"##,
            ##"{"source":"m4","tool":"claude","session":"c3","state":"done","message":"Refactored the heatmap transform","context_pct":63,"source_color":"#00C8C8","updated_at":"2026-09-26T10:05:00+02:00"}"##,
        ]
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return Snapshot(sessions: rows.map { try! decoder.decode(Session.self, from: Data($0.utf8)) })
    }

    private static func noise(_ seed: Int) -> Double {
        var x = UInt64(truncatingIfNeeded: seed &* 2_654_435_761 &+ 97)
        x ^= x >> 13; x = x &* 0x5bd1e995; x ^= x >> 15
        return Double(x % 10_000) / 10_000
    }

    private static func focusMinutes(back: Int) -> Int {
        guard let d = calendar.date(byAdding: .day, value: -back, to: now) else { return 0 }
        let weekday = calendar.component(.weekday, from: d)
        let weekend = weekday == 1 || weekday == 7
        let n = noise(back)
        if back == 0 { return 75 }
        if n < (weekend ? 0.6 : 0.12) { return 0 }
        let sessions = weekend ? 1 + Int(n * 3) : 2 + Int(n * 6)
        return sessions * 25
    }

    static func stats(days: Int) -> PomoStats {
        let keys = (0..<7).map { DayKey.key(calendar.date(byAdding: .day, value: -$0, to: now)!, in: calendar) }
        let history: [PomoDayStat] = keys.enumerated().map { i, key in
            let m = i < days ? focusMinutes(back: i) : 0
            return try! JSONDecoder().decode(PomoDayStat.self, from: Data(
                #"{"date":"\#(key)","completed_focus":\#(m / 25),"focus_min":\#(m)}"#.utf8))
        }
        var weekly: [String: (Int, Int)] = [:]
        for back in 0..<min(days, 84) {
            let d = calendar.date(byAdding: .day, value: -back, to: now)!
            let m = focusMinutes(back: back)
            let k = DayKey.weekKey(d, in: calendar)
            weekly[k, default: (0, 0)].0 += m
            weekly[k, default: (0, 0)].1 += m / 25
        }
        let buckets = weekly.filter { $0.value.0 > 0 }.keys.sorted().map {
            FocusBucket(key: $0, focusMin: weekly[$0]!.0, sessions: weekly[$0]!.1)
        }
        let completed = min(days, 30) * 3
        return PomoStats(today: history[0], history: history,
                         streak: min(days, 4), longestStreak: days > 30 ? 11 : min(days, 4),
                         completion: CompletionStat(completedFocus: completed, abandonedFocus: completed / 6,
                                                    totalFocus: completed + completed / 6,
                                                    completionRate: Double(completed) / Double(completed + completed / 6),
                                                    focusSec: completed * 1500),
                         goal: GoalStatus(dailySessions: 6, todayCompleted: history[0].completedFocus,
                                          dailyMet: false, weeklyDays: 5, weekActiveDays: 4, weeklyMet: false),
                         weekly: Array(buckets.suffix(12)))
    }

    static func heatmap(days: Int) -> Heatmap {
        var grid = Array(repeating: Array(repeating: 0, count: 24), count: 7)
        var calendarBuckets: [FocusBucket] = []
        for back in (0..<min(days, 84)).reversed() {
            let d = calendar.date(byAdding: .day, value: -back, to: now)!
            let m = focusMinutes(back: back)
            guard m > 0 else { continue }
            calendarBuckets.append(FocusBucket(key: DayKey.key(d, in: calendar), focusMin: m, sessions: m / 25))
            let wd = calendar.component(.weekday, from: d) - 1
            var left = m / 25
            var hour = 9 + Int(noise(back * 7) * 3)
            while left > 0 && hour < 23 {
                if hour != 12 && hour != 13 { grid[wd][hour] += 25; left -= 1 }
                hour += noise(back * 31 + hour) < 0.3 ? 2 : 1
            }
        }
        return Heatmap(grid: grid, calendar: calendarBuckets, days: 84)
    }

    private static func iso(_ date: Date) -> String {
        let f = ISO8601DateFormatter()
        f.timeZone = calendar.timeZone
        return f.string(from: date)
    }

    private static func at(_ back: Int, hours: Double) -> Date {
        calendar.startOfDay(for: calendar.date(byAdding: .day, value: -back, to: now)!).addingTimeInterval(hours * 3600)
    }

    private static func clockString(_ hours: Double) -> String {
        let minutes = Int((hours * 60).rounded())
        return String(format: "%02d:%02d", (minutes / 60) % 24, minutes % 60)
    }

    private static func decodeDay<T: Decodable>(_ json: String) -> T {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try! decoder.decode(T.self, from: Data(json.utf8))
    }

    static var dayState: DayState { dayState(phase: "on") }

    static func dayState(phase: String, enabled: Bool = true) -> DayState {
        let started = phase == "off" ? "null" : "\"\(iso(at(0, hours: 9.67)))\""
        return decodeDay(#"{"enabled":\#(enabled),"state":"\#(phase)","started_at":\#(started),"stop_at":"00:00","on_sec":3000,"replies_off_last_night":5}"#)
    }

    static func dayHistory(days: Int) -> DayHistory {
        let rows: [String] = (0..<days).map { back in
            let d = calendar.date(byAdding: .day, value: -back, to: now)!
            let key = DayKey.key(d, in: calendar)
            let weekday = calendar.component(.weekday, from: d)
            let weekend = weekday == 1 || weekday == 7
            let n = noise(back * 17 + 3)
            let target = 24 + min(1.0, Double(max(0, back - 4)) / 24)
            if back == 0 {
                return #"{"date":"\#(key)","start":"\#(iso(at(0, hours: 9.67)))","stop":null,"stop_target":"\#(clockString(target))","on_sec":3000,"break_sec":0,"breaks":[],"auto":false,"in_range":null,"replies_on":14,"replies_off":0,"first_reply":"\#(iso(at(0, hours: 9.7)))","last_reply":"\#(iso(at(0, hours: 10.4)))"}"#
            }
            if back == 9 || n < (weekend ? 0.5 : 0.06) {
                let replies = back == 9 ? 6 : (n < 0.2 ? 3 : 0)
                let times = (0..<replies).map { "\"\(iso(at(back, hours: 21.2 + Double($0) * 0.3)))\"" }
                return #"{"date":"\#(key)","start":null,"stop":null,"stop_target":null,"on_sec":0,"break_sec":0,"breaks":[],"auto":false,"in_range":null,"replies_on":0,"replies_off":\#(replies),"first_reply":null,"last_reply":null,"replies_off_at":[\#(times.joined(separator: ","))]}"#
            }
            let start = 10 + noise(back * 5 + 1) * 2.5
            let over = noise(back * 7 + 2) * Double(back) / 14 - 0.3
            let stop = target + over
            let auto = back % 6 == 2
            let breaks = [(14 + noise(back) * 1.2, 0.5 + noise(back + 1) * 0.5), (19.5 + noise(back + 2), 0.4)]
                .prefix(back % 3 == 0 ? 1 : 2)
            let breakTotal = breaks.reduce(0.0) { $0 + $1.1 }
            let breaksJSON = breaks.map { #"{"start":"\#(iso(at(back, hours: $0.0)))","end":"\#(iso(at(back, hours: $0.0 + $0.1)))"}"# }
            let off = over > 0.1 ? 2 + Int(noise(back * 3) * 8) : Int(noise(back * 3) * 3)
            let offTimes = (0..<off).map { "\"\(iso(at(back, hours: stop + 0.15 + Double($0) * 0.2)))\"" }
            let inRange = stop <= target + 0.25
            let onSec = Int((stop - start - breakTotal) * 3600)
            return #"{"date":"\#(key)","start":"\#(iso(at(back, hours: start)))","stop":"\#(iso(at(back, hours: stop)))","stop_target":"\#(clockString(target))","on_sec":\#(onSec),"break_sec":\#(Int(breakTotal * 3600)),"breaks":[\#(breaksJSON.joined(separator: ","))],"auto":\#(auto),"in_range":\#(inRange),"replies_on":\#(120 + Int(n * 120)),"replies_off":\#(off),"first_reply":"\#(iso(at(back, hours: start + 0.1)))","last_reply":"\#(iso(at(back, hours: stop + 0.1)))","replies_off_at":[\#(offTimes.joined(separator: ","))]}"#
        }
        return decodeDay(#"{"days":[\#(rows.joined(separator: ","))]}"#)
    }

    static func dayWeeks(from history: DayHistory) -> DayWeeks {
        var weeks: [String: (worked: Int, inRange: Int, on: Int, brk: Int, off: Int)] = [:]
        for day in history.days {
            guard let d = DayKey.date(day.date, in: calendar) else { continue }
            let key = DayKey.weekKey(d, in: calendar)
            var w = weeks[key, default: (0, 0, 0, 0, 0)]
            if day.start != nil {
                w.worked += 1
                if day.inRange == true { w.inRange += 1 }
            }
            w.on += day.onSec
            w.brk += day.breakSec
            w.off += day.repliesOff
            weeks[key] = w
        }
        let rows = weeks.keys.sorted().map { k -> String in
            let w = weeks[k]!
            return #"{"week":"\#(k)","days_worked":\#(w.worked),"days_in_range":\#(w.inRange),"on_sec":\#(w.on),"break_sec":\#(w.brk),"replies_off":\#(w.off)}"#
        }
        return decodeDay(#"{"weeks":[\#(rows.joined(separator: ","))]}"#)
    }

    static func activity(days: Int) -> ActivitySummary {
        var a: ActivitySummary = golden("activity_summary")
        var rows: [ActivitySummary.SourceDay] = []
        for back in (0..<days).reversed() {
            let d = calendar.date(byAdding: .day, value: -back, to: now)!
            let key = DayKey.key(d, in: calendar)
            for (i, src) in [("m4", "#00C8C8"), ("m5", "#FF8800")].enumerated() {
                let secs = back == 0 ? (i == 0 ? 240 : 600) : Int(noise(back * 11 + i) * 9000)
                var r = a.dailyBySource[i]
                r.day = key
                r.date = calendar.startOfDay(for: d)
                r.source = src.0
                r.sourceColor = src.1
                r.activeSec = secs
                rows.append(r)
            }
        }
        a.dailyBySource = rows
        a.days = days
        return a
    }

    static var ledFrame: [Int] {
        let font: [Character: [String]] = [
            "1": ["010", "110", "010", "010", "010", "010", "111"],
            "0": ["111", "101", "101", "101", "101", "101", "111"],
            "3": ["111", "001", "001", "111", "001", "001", "111"],
            ":": ["0", "1", "0", "0", "0", "1", "0"],
        ]
        var px = Array(repeating: 0, count: 256)
        var x = 8
        for ch in "10:30" {
            let glyph = font[ch]!
            for (row, line) in glyph.enumerated() {
                for (col, bit) in line.enumerated() where bit == "1" {
                    px[row * 32 + x + col] = 0x2EE8D0
                }
            }
            x += glyph[0].count + 1
        }
        for day in 0..<7 {
            let color = day == 5 ? 0xFFFFFF : 0x444444
            px[7 * 32 + 5 + day * 3] = color
            px[7 * 32 + 6 + day * 3] = color
        }
        return px
    }

    static func date(_ iso: String) -> Date { try! Date(iso, strategy: .iso8601) }
}
#endif
