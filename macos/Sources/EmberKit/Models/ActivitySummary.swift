import Foundation

public struct ActivitySummary: Decodable, Sendable, Equatable {
    public struct Totals: Decodable, Sendable, Equatable, Identifiable {
        public var key: String?
        public var sourceColor: String?
        public var activeSec: Int
        public var sessions: Int
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
        public var byTool: [Totals]
        public var bySource: [Totals]

        enum CodingKeys: String, CodingKey {
            case from, to, total
            case byTool = "by_tool"
            case bySource = "by_source"
        }
    }

    public struct ToolDay: Decodable, Sendable, Equatable, Identifiable {
        public var day: String
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

    public struct SourceDay: Decodable, Sendable, Equatable, Identifiable {
        public var day: String
        public var date: Date
        public var source: String
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
    public var recording: Bool
    public var days: Int
    public var spanGapSec: Int
    public var today: Window
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
