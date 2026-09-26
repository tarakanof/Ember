import Foundation

/// The "Agent time" chart: active agent minutes per day, stacked by source
/// (the Mac that ran the agent), over a zero-filled window ending today.
public struct AgentTimeChart: Equatable, Sendable {
    public struct Segment: Equatable, Sendable, Identifiable {
        public let key: String
        public let date: Date
        public let source: String
        public let minutes: Double
        public var id: String { "\(key)|\(source)" }
    }

    public struct Source: Equatable, Sendable, Identifiable {
        public let name: String
        /// The producer's "#RRGGBB", else one from `fallbackColors` by rank.
        public let colorHex: String
        public let totalMinutes: Double
        public var id: String { name }
    }

    /// Stable colours for sources that haven't posted their own.
    public static let fallbackColors = ["#0A84FF", "#FF9F0A", "#30D158", "#BF5AF2", "#FF375F", "#64D2FF"]

    /// Days × sources, oldest day first; sources in `sources` order.
    public let segments: [Segment]
    /// Most active first: the stacking and legend order.
    public let sources: [Source]
    /// Local midnight of every day in the window, oldest first.
    public let days: [Date]
    public let todayMinutes: Double
    public let isRecording: Bool

    public var isEmpty: Bool { sources.allSatisfy { $0.totalMinutes == 0 } }
    public var totalMinutes: Double { sources.reduce(0) { $0 + $1.totalMinutes } }

    /// Per-day totals, oldest first, for the accessibility description.
    public var dailyTotals: [(date: Date, minutes: Double)] {
        days.map { d in (d, segments.filter { $0.date == d }.reduce(0) { $0 + $1.minutes }) }
    }

    /// One day's bar, for the hover callout.
    public struct DayDetail: Equatable, Sendable {
        public struct Part: Equatable, Sendable {
            public let source: String
            public let minutes: Double
        }

        public let key: String
        public let date: Date
        public let totalMinutes: Double
        /// Sources that worked that day, in stacking order.
        public let parts: [Part]
    }

    /// The day `key` ("2026-09-26") names, or nil outside the window.
    public func detail(forKey key: String) -> DayDetail? {
        let day = segments.filter { $0.key == key }
        guard let date = day.first?.date else { return nil }
        return DayDetail(key: key, date: date, totalMinutes: day.reduce(0) { $0 + $1.minutes },
                         parts: day.filter { $0.minutes > 0 }.map { DayDetail.Part(source: $0.source, minutes: $0.minutes) })
    }

    /// Day keys, oldest first: the chart's categorical x domain.
    public var dayKeys: [String] {
        var seen = Set<String>()
        return segments.map(\.key).filter { seen.insert($0).inserted }
    }

    /// `windowDays` days ending with the server's today (`summary.today.from`
    /// falls on it), zero-filled where the summary has fewer.
    public init(summary: ActivitySummary, windowDays: Int = 7, calendar: Calendar) {
        let todayKey = summary.dailyBySource.last?.day ?? DayKey.key(summary.today.from, in: calendar)
        let end = DayKey.date(todayKey, in: calendar) ?? calendar.startOfDay(for: summary.generatedAt)
        days = DayKey.days(endingAt: end, count: windowDays, in: calendar)
        let windowKeys = Set(days.map { DayKey.key($0, in: calendar) })
        let rows = summary.dailyBySource.filter { windowKeys.contains($0.day) }

        var totals: [String: Double] = [:]
        var colors: [String: String] = [:]
        var firstSeen: [String] = []
        for r in rows {
            if totals[r.source] == nil { firstSeen.append(r.source) }
            totals[r.source, default: 0] += Double(r.activeSec) / 60
            if let c = r.sourceColor, RGB(hex: c) != nil { colors[r.source] = c }
        }
        let ranked = firstSeen.sorted { (totals[$0] ?? 0, $1) > (totals[$1] ?? 0, $0) }
        sources = ranked.enumerated().map { i, name in
            Source(name: name, colorHex: colors[name] ?? Self.fallbackColors[i % Self.fallbackColors.count],
                   totalMinutes: totals[name] ?? 0)
        }
        let byKey = Dictionary(rows.map { ("\($0.day)|\($0.source)", $0) }, uniquingKeysWith: { a, _ in a })
        segments = days.flatMap { day in
            let key = DayKey.key(day, in: calendar)
            return ranked.map { name in
                Segment(key: key, date: day, source: name,
                        minutes: Double(byKey["\(key)|\(name)"]?.activeSec ?? 0) / 60)
            }
        }
        todayMinutes = Double(summary.today.total.activeSec) / 60
        isRecording = summary.recording
    }
}
