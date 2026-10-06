import Foundation

public func reminderShouldFire(now: Date, dueDate: Date, leadMinutes: Int, grace: TimeInterval) -> Bool {
    let fireTime = dueDate.addingTimeInterval(-Double(leadMinutes) * 60)
    let delta = now.timeIntervalSince(fireTime)
    return delta >= 0 && delta <= grace
}

public func reminderDedupeKey(id: String, dueDate: Date) -> String {
    "\(id)|\(Int(dueDate.timeIntervalSince1970))"
}

public struct ReminderFiredLedger: Sendable {
    private var dueByKey: [String: Date] = [:]

    public init() {}

    public var count: Int { dueByKey.count }

    public func contains(_ key: String) -> Bool { dueByKey[key] != nil }

    public mutating func record(_ key: String, due: Date) { dueByKey[key] = due }

    public mutating func prune(now: Date, keep: TimeInterval = 86_400) {
        dueByKey = dueByKey.filter { now.timeIntervalSince($0.value) <= keep }
    }
}

public enum ReminderFireOutcome: Equatable, Sendable {
    case delivered
    case maybeDelivered
    case notDelivered

    public init(error: Error) {
        switch error {
        case is RequestNotSent:
            self = .notDelivered
        case APIError.notConfigured, APIError.rateLimited:
            self = .notDelivered
        case APIError.http(let status, _) where (400..<500).contains(status):
            self = .notDelivered
        default:
            self = .maybeDelivered
        }
    }
}

public struct ReminderFireTracker: Sendable {
    private var fired = ReminderFiredLedger()
    private var inFlight = Set<String>()

    public init() {}

    public mutating func begin(_ key: String) -> Bool {
        if fired.contains(key) || inFlight.contains(key) { return false }
        inFlight.insert(key)
        return true
    }

    public mutating func finish(_ key: String, due: Date, outcome: ReminderFireOutcome) {
        inFlight.remove(key)
        if outcome != .notDelivered { fired.record(key, due: due) }
    }

    public mutating func prune(now: Date) { fired.prune(now: now) }

    public var firedCount: Int { fired.count }
}

public struct ReminderPrefs: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var sound: Bool
    public var leadMinutes: Int
    public var popupDuration: Int
    public var useNativeIcon: Bool
    public var nativeIconId: String
    public var hold: Bool
    public var repeatSound: Bool

    public init(enabled: Bool = false, sound: Bool = true, leadMinutes: Int = 0,
                popupDuration: Int = 8, useNativeIcon: Bool = false, nativeIconId: String = "",
                hold: Bool = true, repeatSound: Bool = false) {
        self.enabled = enabled
        self.sound = sound
        self.leadMinutes = leadMinutes
        self.popupDuration = popupDuration
        self.useNativeIcon = useNativeIcon
        self.nativeIconId = nativeIconId
        self.hold = hold
        self.repeatSound = repeatSound
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        sound = try c.decodeIfPresent(Bool.self, forKey: .sound) ?? true
        leadMinutes = try c.decodeIfPresent(Int.self, forKey: .leadMinutes) ?? 0
        popupDuration = try c.decodeIfPresent(Int.self, forKey: .popupDuration) ?? 8
        useNativeIcon = try c.decodeIfPresent(Bool.self, forKey: .useNativeIcon) ?? false
        nativeIconId = try c.decodeIfPresent(String.self, forKey: .nativeIconId) ?? ""
        hold = try c.decodeIfPresent(Bool.self, forKey: .hold) ?? true
        repeatSound = try c.decodeIfPresent(Bool.self, forKey: .repeatSound) ?? false
    }
}
