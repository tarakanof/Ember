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

    enum CodingKeys: String, CodingKey {
        case enabled, sound, hold
        case leadMinutes = "lead_minutes"
        case popupDuration = "popup_duration"
        case useNativeIcon = "use_native_icon"
        case nativeIconId = "native_icon_id"
        case repeatSound = "repeat_sound"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = ReminderPrefs()
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? d.enabled
        sound = try c.decodeIfPresent(Bool.self, forKey: .sound) ?? d.sound
        leadMinutes = try c.decodeIfPresent(Int.self, forKey: .leadMinutes) ?? d.leadMinutes
        popupDuration = try c.decodeIfPresent(Int.self, forKey: .popupDuration) ?? d.popupDuration
        useNativeIcon = try c.decodeIfPresent(Bool.self, forKey: .useNativeIcon) ?? d.useNativeIcon
        nativeIconId = try c.decodeIfPresent(String.self, forKey: .nativeIconId) ?? d.nativeIconId
        hold = try c.decodeIfPresent(Bool.self, forKey: .hold) ?? d.hold
        repeatSound = try c.decodeIfPresent(Bool.self, forKey: .repeatSound) ?? d.repeatSound
    }

    public static let leadRange = 0...30
    public static let popupDurationRange = 1...300
    public static let maxNativeIconIdLength = 64

    public var clampedForServer: ReminderPrefs {
        var p = self
        p.leadMinutes = min(max(leadMinutes, Self.leadRange.lowerBound), Self.leadRange.upperBound)
        p.popupDuration = min(max(popupDuration, Self.popupDurationRange.lowerBound), Self.popupDurationRange.upperBound)
        p.nativeIconId = String(nativeIconId.trimmingCharacters(in: .whitespaces).prefix(Self.maxNativeIconIdLength))
        return p
    }

    public init?(legacyData data: Data) {
        guard let l = try? JSONDecoder().decode(Legacy.self, from: data) else { return nil }
        let d = ReminderPrefs()
        self.init(enabled: l.enabled ?? d.enabled, sound: l.sound ?? d.sound,
                  leadMinutes: l.leadMinutes ?? d.leadMinutes, popupDuration: l.popupDuration ?? d.popupDuration,
                  useNativeIcon: l.useNativeIcon ?? d.useNativeIcon, nativeIconId: l.nativeIconId ?? d.nativeIconId,
                  hold: l.hold ?? d.hold, repeatSound: l.repeatSound ?? d.repeatSound)
    }

    private struct Legacy: Decodable {
        var enabled: Bool?
        var sound: Bool?
        var leadMinutes: Int?
        var popupDuration: Int?
        var useNativeIcon: Bool?
        var nativeIconId: String?
        var hold: Bool?
        var repeatSound: Bool?
    }
}
