import Foundation

/// GET /v1/activity/summary — agent activity per tool and per source.
public struct ActivitySummary: Decodable, Sendable, Equatable {
    /// One rollup row. `key` is the tool or source name, nil on a window total.
    /// `sourceColor` ("#RRGGBB") only exists on by-source rows, where the server
    /// sends null until the source posts a colour; it is always nil elsewhere.
    public struct Totals: Decodable, Sendable, Equatable, Identifiable {
        public var key: String?
        public var sourceColor: String?
        /// Wall-clock working time (running/error only; waiting never counts).
        /// Concurrent sessions of one group count once.
        public var activeSec: Int
        public var sessions: Int
        /// Waiting episodes: how often an agent stopped to ask for input.
        public var attention: Int
        public var id: String { key ?? "" }

        enum CodingKeys: String, CodingKey {
            case key, sessions, attention
            case sourceColor = "source_color"
            case activeSec = "active_sec"
        }
    }

    public struct Window: Decodable, Sendable, Equatable {
        public var from: Date
        public var to: Date
        public var total: Totals
        /// Most active first.
        public var byTool: [Totals]
        /// Most active first.
        public var bySource: [Totals]

        enum CodingKeys: String, CodingKey {
            case from, to, total
            case byTool = "by_tool"
            case bySource = "by_source"
        }
    }

    /// One (day, tool) bar. Zero-filled and oldest first, so a stacked
    /// `BarMark(x: .value("Day", $0.date, unit: .day), y: …)` works as is.
    public struct ToolDay: Decodable, Sendable, Equatable, Identifiable {
        /// Logical day ("2026-09-26"), honouring the server's day-start hour.
        public var day: String
        /// Local midnight of `day`.
        public var date: Date
        public var tool: String
        public var activeSec: Int
        public var sessions: Int
        public var attention: Int
        public var id: String { "\(day)|\(tool)" }

        enum CodingKeys: String, CodingKey {
            case day, date, tool, sessions, attention
            case activeSec = "active_sec"
        }
    }

    /// One (day, source) bar, coloured by the source. Zero-filled, oldest first.
    public struct SourceDay: Decodable, Sendable, Equatable, Identifiable {
        public var day: String
        public var date: Date
        public var source: String
        /// "#RRGGBB"; nil until the source has posted a colour since server start.
        public var sourceColor: String?
        public var activeSec: Int
        public var sessions: Int
        public var attention: Int
        public var id: String { "\(day)|\(source)" }

        enum CodingKeys: String, CodingKey {
            case day, date, source, sessions, attention
            case sourceColor = "source_color"
            case activeSec = "active_sec"
        }
    }

    public var generatedAt: Date
    /// False while the server's work-hours activity overlay is off: nothing new
    /// is being stored, so recent windows read as zero.
    public var recording: Bool
    public var days: Int
    public var spanGapSec: Int
    public var today: Window
    /// The last `days` logical days, today included.
    public var period: Window
    public var daily: [ToolDay]
    public var dailyBySource: [SourceDay]

    enum CodingKeys: String, CodingKey {
        case recording, days, today, period, daily
        case generatedAt = "generated_at"
        case spanGapSec = "span_gap_sec"
        case dailyBySource = "daily_by_source"
    }
}
