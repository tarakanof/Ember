import Foundation

public enum BotPrint: Equatable, Sendable {
    case none
    case starting
    case printing(percent: Int)
    case attention(percent: Int)
    case done

    public var fill: Double {
        switch self {
        case .none, .starting: 0
        case .printing(let p), .attention(let p): Double(min(max(p, 0), 100)) / 100
        case .done: 1
        }
    }

    public var isAttention: Bool {
        if case .attention = self { return true }
        return false
    }

    public func looksLike(_ other: BotPrint, radius: Double) -> Bool {
        switch (self, other) {
        case (.printing, .printing), (.attention, .attention):
            BotPose.fillPixels(fill, radius: radius) == BotPose.fillPixels(other.fill, radius: radius)
        default:
            self == other
        }
    }
}

public struct BotPrintDoneStore {
    public static let key = "bot.printDonePlayed"

    private let defaults: UserDefaults

    public init(defaults: UserDefaults) {
        self.defaults = defaults
    }

    public func load() -> UInt64? {
        (defaults.object(forKey: Self.key) as? NSNumber)?.uint64Value
    }

    public func save(_ id: UInt64) {
        defaults.set(NSNumber(value: id), forKey: Self.key)
    }
}

public struct BotPrintTracker: Sendable {
    public static let doneLength: TimeInterval = 60
    public static let eventWindow: TimeInterval = 30 * 60
    static let fillPhases: Set<String> = ["heating", "preparing", "printing", "finishing", "paused", "attention"]
    static let startPhases: Set<String> = ["heating", "preparing"]

    public private(set) var playedDoneID: UInt64?
    public private(set) var doneEndsAt: Date?

    public init(playedDoneID: UInt64? = nil) {
        self.playedDoneID = playedDoneID
    }

    public mutating func update(state: PrinterState?, agentState: String, enabled: Bool, now: Date) -> BotPrint {
        guard enabled, agentState != "waiting", agentState != "error" else {
            doneEndsAt = nil
            return .none
        }
        guard let state, state.enabled else {
            if let end = doneEndsAt, now < end { return .done }
            doneEndsAt = nil
            return .none
        }
        if let e = state.event, e.kind == "finished", e.id != playedDoneID,
           now.timeIntervalSince1970 - Double(e.at) < Self.eventWindow {
            playedDoneID = e.id
            doneEndsAt = now.addingTimeInterval(Self.doneLength)
        }
        let active = Self.fillPhases.contains(state.phase)
        if let end = doneEndsAt {
            if now < end && !active { return .done }
            doneEndsAt = nil
        }
        guard active else { return .none }
        let percent = min(max(state.progress ?? 0, 0), 100)
        if state.phase == "attention" { return .attention(percent: percent) }
        return Self.startPhases.contains(state.phase) ? .starting : .printing(percent: percent)
    }
}
