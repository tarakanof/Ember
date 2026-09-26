import Foundation

/// GET /v1/pomodoro/workhours — sessionized work span per logical day.
public struct WorkHours: Decodable, Sendable, Equatable {
    public struct Day: Decodable, Sendable, Equatable, Identifiable {
        /// Logical day ("2026-09-26").
        public var date: String
        /// nil on a day with no work.
        public var workStart: Date?
        public var workEnd: Date?
        public var spanSec: Int
        public var activeSec: Int
        public var breakSec: Int
        public var sessions: Int
        public var longestSec: Int
        public var id: String { date }

        enum CodingKeys: String, CodingKey {
            case date, sessions
            case workStart = "work_start"
            case workEnd = "work_end"
            case spanSec = "span_sec"
            case activeSec = "active_sec"
            case breakSec = "break_sec"
            case longestSec = "longest_sec"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            date = try c.decode(String.self, forKey: .date)
            workStart = Self.realDate(try c.decodeIfPresent(Date.self, forKey: .workStart))
            workEnd = Self.realDate(try c.decodeIfPresent(Date.self, forKey: .workEnd))
            spanSec = try c.decode(Int.self, forKey: .spanSec)
            activeSec = try c.decode(Int.self, forKey: .activeSec)
            breakSec = try c.decode(Int.self, forKey: .breakSec)
            sessions = try c.decode(Int.self, forKey: .sessions)
            longestSec = try c.decode(Int.self, forKey: .longestSec)
        }

        /// Servers before 0.28 send Go's zero time ("0001-01-01T00:00:00Z") for
        /// an empty day; treat anything before 1971 as "no value".
        static func realDate(_ d: Date?) -> Date? {
            guard let d, d.timeIntervalSince1970 > 365 * 86_400 else { return nil }
            return d
        }
    }

    /// Most recent first.
    public var days: [Day]
    public var gapMin: Int
    /// Agent activity is unioned with focus blocks.
    public var includeActivity: Bool

    enum CodingKeys: String, CodingKey {
        case days
        case gapMin = "gap_min"
        case includeActivity = "include_activity"
    }
}
