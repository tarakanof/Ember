import Foundation

public struct DayState: Decodable, Sendable, Equatable {
    public enum Phase: String, Sendable, Equatable {
        case off, on
        case onBreak = "break"
    }

    public var enabled: Bool
    public var phase: Phase
    public var startedAt: Date?
    public var stopAt: String?
    public var onSec: Int
    public var repliesOffLastNight: Int

    enum CodingKeys: String, CodingKey {
        case enabled, state
        case startedAt = "started_at"
        case stopAt = "stop_at"
        case onSec = "on_sec"
        case repliesOffLastNight = "replies_off_last_night"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
        phase = Phase(rawValue: try c.decodeIfPresent(String.self, forKey: .state) ?? "") ?? .off
        startedAt = DayClock.realDate(try c.decodeIfPresent(Date.self, forKey: .startedAt))
        stopAt = try c.decodeIfPresent(String.self, forKey: .stopAt)
        onSec = try c.decodeIfPresent(Int.self, forKey: .onSec) ?? 0
        repliesOffLastNight = try c.decodeIfPresent(Int.self, forKey: .repliesOffLastNight) ?? 0
    }
}

public struct DayHistory: Decodable, Sendable, Equatable {
    public struct Interval: Decodable, Sendable, Equatable {
        public var start: Date
        public var end: Date?
    }

    public struct Day: Decodable, Sendable, Equatable, Identifiable {
        public var date: String
        public var start: Date?
        public var stop: Date?
        public var stopTarget: String?
        public var onSec: Int
        public var breakSec: Int
        public var breaks: [Interval]
        public var auto: Bool
        public var inRange: Bool?
        public var repliesOn: Int
        public var repliesOff: Int
        public var firstReply: Date?
        public var lastReply: Date?
        public var repliesOffAt: [Date]
        public var id: String { date }

        enum CodingKeys: String, CodingKey {
            case date, start, stop, breaks, auto
            case stopTarget = "stop_target"
            case onSec = "on_sec"
            case breakSec = "break_sec"
            case inRange = "in_range"
            case repliesOn = "replies_on"
            case repliesOff = "replies_off"
            case firstReply = "first_reply"
            case lastReply = "last_reply"
            case repliesOffAt = "replies_off_at"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            date = try c.decode(String.self, forKey: .date)
            start = DayClock.realDate(try c.decodeIfPresent(Date.self, forKey: .start))
            stop = DayClock.realDate(try c.decodeIfPresent(Date.self, forKey: .stop))
            stopTarget = try c.decodeIfPresent(String.self, forKey: .stopTarget)
            onSec = try c.decodeIfPresent(Int.self, forKey: .onSec) ?? 0
            breakSec = try c.decodeIfPresent(Int.self, forKey: .breakSec) ?? 0
            breaks = try c.decodeIfPresent([Interval].self, forKey: .breaks) ?? []
            auto = try c.decodeIfPresent(Bool.self, forKey: .auto) ?? false
            inRange = try c.decodeIfPresent(Bool.self, forKey: .inRange)
            repliesOn = try c.decodeIfPresent(Int.self, forKey: .repliesOn) ?? 0
            repliesOff = try c.decodeIfPresent(Int.self, forKey: .repliesOff) ?? 0
            firstReply = DayClock.realDate(try c.decodeIfPresent(Date.self, forKey: .firstReply))
            lastReply = DayClock.realDate(try c.decodeIfPresent(Date.self, forKey: .lastReply))
            repliesOffAt = (try c.decodeIfPresent([Date].self, forKey: .repliesOffAt) ?? []).compactMap(DayClock.realDate)
        }
    }

    public var days: [Day]
}

public struct DayWeeks: Decodable, Sendable, Equatable {
    public struct Week: Decodable, Sendable, Equatable, Identifiable {
        public var key: String
        public var daysWorked: Int
        public var daysInRange: Int
        public var onSec: Int
        public var breakSec: Int
        public var repliesOff: Int
        public var id: String { key }

        enum CodingKeys: String, CodingKey {
            case week, key, start
            case daysWorked = "days_worked"
            case daysInRange = "days_in_range"
            case onSec = "on_sec"
            case breakSec = "break_sec"
            case repliesOff = "replies_off"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            let name = try c.decodeIfPresent(String.self, forKey: .week)
                ?? c.decodeIfPresent(String.self, forKey: .key)
                ?? c.decode(String.self, forKey: .start)
            key = name
            daysWorked = try c.decodeIfPresent(Int.self, forKey: .daysWorked) ?? 0
            daysInRange = try c.decodeIfPresent(Int.self, forKey: .daysInRange) ?? 0
            onSec = try c.decodeIfPresent(Int.self, forKey: .onSec) ?? 0
            breakSec = try c.decodeIfPresent(Int.self, forKey: .breakSec) ?? 0
            repliesOff = try c.decodeIfPresent(Int.self, forKey: .repliesOff) ?? 0
        }
    }

    public var weeks: [Week]
}

public struct DayConfig: Codable, Sendable, Equatable {
    public var enabled: Bool
    public var defaultStop: String
    public var breakHintMinutes: Int
    public var autoStopMinutes: Int
    public var graceMinutes: Int

    public init(enabled: Bool = true, defaultStop: String = "00:00", breakHintMinutes: Int = 90,
                autoStopMinutes: Int = 30, graceMinutes: Int = 15) {
        self.enabled = enabled
        self.defaultStop = defaultStop
        self.breakHintMinutes = breakHintMinutes
        self.autoStopMinutes = autoStopMinutes
        self.graceMinutes = graceMinutes
    }

    enum CodingKeys: String, CodingKey {
        case enabled
        case defaultStop = "default_stop"
        case breakHintMinutes = "break_hint_minutes"
        case autoStopMinutes = "auto_stop_minutes"
        case graceMinutes = "grace_minutes"
    }
}

enum DayClock {
    static func realDate(_ d: Date?) -> Date? {
        guard let d, d.timeIntervalSince1970 > 365 * 86_400 else { return nil }
        return d
    }
}
