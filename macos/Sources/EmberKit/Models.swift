import Foundation

public struct PomoState: Codable, Sendable, Equatable {
    public var phase: String
    public var running: Bool
    public var paused: Bool
    public var remainingSec: Int
    public var plannedSec: Int
    public var round: Int

    enum CodingKeys: String, CodingKey {
        case phase, running, paused
        case remainingSec = "remaining_sec"
        case plannedSec = "planned_sec"
        case round
    }
}

public struct PomoDayStat: Codable, Sendable, Equatable {
    public var date: String
    public var completedFocus: Int
    public var focusMin: Int

    enum CodingKeys: String, CodingKey {
        case date
        case completedFocus = "completed_focus"
        case focusMin = "focus_min"
    }
}

public struct PomoStats: Codable, Sendable, Equatable {
    public var today: PomoDayStat
    public var history: [PomoDayStat]
    public var streak: Int
}

public struct PomoConfig: Codable, Sendable, Equatable {
    public var focusMinutes: Int
    public var shortBreakMinutes: Int
    public var longBreakMinutes: Int
    public var roundsBeforeLongBreak: Int
    public var autoStartNext: Bool
    public var sound: Bool
    public var soundMelody: String
    public var focusColor: String
    public var breakColor: String

    public init(focusMinutes: Int, shortBreakMinutes: Int, longBreakMinutes: Int,
                roundsBeforeLongBreak: Int, autoStartNext: Bool, sound: Bool,
                soundMelody: String, focusColor: String, breakColor: String) {
        self.focusMinutes = focusMinutes
        self.shortBreakMinutes = shortBreakMinutes
        self.longBreakMinutes = longBreakMinutes
        self.roundsBeforeLongBreak = roundsBeforeLongBreak
        self.autoStartNext = autoStartNext
        self.sound = sound
        self.soundMelody = soundMelody
        self.focusColor = focusColor
        self.breakColor = breakColor
    }

    enum CodingKeys: String, CodingKey {
        case focusMinutes = "focus_minutes"
        case shortBreakMinutes = "short_break_minutes"
        case longBreakMinutes = "long_break_minutes"
        case roundsBeforeLongBreak = "rounds_before_long_break"
        case autoStartNext = "auto_start_next"
        case sound
        case soundMelody = "sound_melody"
        case focusColor = "focus_color"
        case breakColor = "break_color"
    }
}

/// Mirrors internal/render.Session. context_pct/source_color/rate_window_pct are
/// the only semantically-optional (pointer) fields; the rest default on absence.
public struct Session: Codable, Sendable, Equatable {
    public var source: String
    public var tool: String
    public var session: String
    public var state: String
    public var message: String
    public var tokensToday: Int64
    public var contextPct: Int?
    public var sourceColor: String?
    public var rateWindowPct: Int?
    public var activity: String
    public var contextNumber: Bool
    public var rateBottomBar: Bool
    public var rateResetAt: Int64
    public var rateReset: Bool
    public var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case source, tool, session, state, message
        case tokensToday = "tokens_today"
        case contextPct = "context_pct"
        case sourceColor = "source_color"
        case rateWindowPct = "rate_window_pct"
        case activity
        case contextNumber = "context_number"
        case rateBottomBar = "rate_bottom_bar"
        case rateResetAt = "rate_reset_at"
        case rateReset = "rate_reset"
        case updatedAt = "updated_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        source = try c.decodeIfPresent(String.self, forKey: .source) ?? ""
        tool = try c.decodeIfPresent(String.self, forKey: .tool) ?? ""
        session = try c.decodeIfPresent(String.self, forKey: .session) ?? ""
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? ""
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
        tokensToday = try c.decodeIfPresent(Int64.self, forKey: .tokensToday) ?? 0
        contextPct = try c.decodeIfPresent(Int.self, forKey: .contextPct)
        sourceColor = try c.decodeIfPresent(String.self, forKey: .sourceColor)
        rateWindowPct = try c.decodeIfPresent(Int.self, forKey: .rateWindowPct)
        activity = try c.decodeIfPresent(String.self, forKey: .activity) ?? ""
        contextNumber = try c.decodeIfPresent(Bool.self, forKey: .contextNumber) ?? false
        rateBottomBar = try c.decodeIfPresent(Bool.self, forKey: .rateBottomBar) ?? false
        rateResetAt = try c.decodeIfPresent(Int64.self, forKey: .rateResetAt) ?? 0
        rateReset = try c.decodeIfPresent(Bool.self, forKey: .rateReset) ?? false
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt) ?? .distantPast
    }
}

public struct Snapshot: Codable, Sendable {
    public var sessions: [Session]

    enum CodingKeys: String, CodingKey { case sessions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessions = try c.decodeIfPresent([Session].self, forKey: .sessions) ?? []
    }
}

public struct CardFrame: Codable, Sendable, Equatable {
    public var card: String
    public var pixels: [String]
}

public struct PreviewResponse: Codable, Sendable, Equatable {
    public var width: Int
    public var height: Int
    public var activity: String
    public var frames: [CardFrame]
}
