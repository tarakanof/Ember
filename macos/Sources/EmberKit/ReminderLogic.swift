import Foundation

/// True when `now` is inside a reminder's fire window: at or after its fire time
/// (dueDate shifted earlier by `leadMinutes`) and no later than `grace` seconds
/// after it. The grace window lets a missed/coalesced poll still fire, while
/// keeping a long-overdue reminder from firing on launch.
public func reminderShouldFire(now: Date, dueDate: Date, leadMinutes: Int, grace: TimeInterval) -> Bool {
    let fireTime = dueDate.addingTimeInterval(-Double(leadMinutes) * 60)
    let delta = now.timeIntervalSince(fireTime)
    return delta >= 0 && delta <= grace
}

/// A stable per-occurrence key so each reminder rings once; a recurring reminder
/// (new due date after completion) yields a new key and rings again.
public func reminderDedupeKey(id: String, dueDate: Date) -> String {
    "\(id)|\(Int(dueDate.timeIntervalSince1970))"
}

/// The reminder occurrences (by `reminderDedupeKey`) already delivered to the
/// clock, so each rings once. Callers record a key only after the fire request
/// succeeds, so a failed fire is retried on the next poll while still inside
/// the grace window. `prune` keeps the set from growing for the app's lifetime.
public struct ReminderFiredLedger: Sendable {
    private var dueByKey: [String: Date] = [:]

    public init() {}

    /// Number of remembered occurrences.
    public var count: Int { dueByKey.count }

    /// True when `key` was recorded and not yet pruned.
    public func contains(_ key: String) -> Bool { dueByKey[key] != nil }

    /// Remembers that the occurrence `key`, due at `due`, reached the clock.
    public mutating func record(_ key: String, due: Date) { dueByKey[key] = due }

    /// Forgets occurrences due more than `keep` before `now`. They are long past
    /// any fire window, so forgetting them can't cause a second ring.
    public mutating func prune(now: Date, keep: TimeInterval = 86_400) {
        dueByKey = dueByKey.filter { now.timeIntervalSince($0.value) <= keep }
    }
}

/// What a fire attempt tells us about whether the popup reached the clock.
public enum ReminderFireOutcome: Equatable, Sendable {
    case delivered
    /// The request may have reached the server (timeout, 5xx such as a 502
    /// after a lost clock ack). Treated as delivered: a second ring is worse
    /// than a lost one, and the server's idempotency key catches most repeats.
    case maybeDelivered
    /// The request provably had no effect; safe to retry.
    case notDelivered

    /// Classifies a `RemindersService.fire` error. Only failures that prove
    /// the server never acted are `.notDelivered`: no connection, not
    /// configured, 429, or another 4xx (rejected before the push).
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

/// Decides which due reminder occurrences to fire. `begin` claims a key before
/// the request is sent, so an overlapping poll (e.g. after a disable/re-enable
/// cycle) can't fire the same occurrence while the first request is in flight.
/// `finish` records the key unless the attempt was `.notDelivered`, which is
/// retried on the next poll while still inside the grace window.
public struct ReminderFireTracker: Sendable {
    private var fired = ReminderFiredLedger()
    private var inFlight = Set<String>()

    public init() {}

    /// Claims `key` for a fire attempt; false if it already fired or is in flight.
    public mutating func begin(_ key: String) -> Bool {
        if fired.contains(key) || inFlight.contains(key) { return false }
        inFlight.insert(key)
        return true
    }

    /// Releases the claim from `begin`, recording the occurrence (due at `due`)
    /// as fired unless `outcome` is `.notDelivered`.
    public mutating func finish(_ key: String, due: Date, outcome: ReminderFireOutcome) {
        inFlight.remove(key)
        if outcome != .notDelivered { fired.record(key, due: due) }
    }

    /// Forgets fired occurrences due more than a day before `now`.
    public mutating func prune(now: Date) { fired.prune(now: now) }
}

/// Apple-Reminders watcher settings, persisted app-side (UserDefaults). The
/// server holds none of this — it's sent per-fire.
public struct ReminderPrefs: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var sound: Bool
    public var leadMinutes: Int
    public var popupDuration: Int
    public var useNativeIcon: Bool
    public var nativeIconId: String
    /// When true the alarm takes over the clock until dismissed (middle button);
    /// when false it auto-dismisses after `popupDuration`.
    public var hold: Bool

    public init(enabled: Bool = false, sound: Bool = true, leadMinutes: Int = 0,
                popupDuration: Int = 8, useNativeIcon: Bool = false, nativeIconId: String = "",
                hold: Bool = true) {
        self.enabled = enabled
        self.sound = sound
        self.leadMinutes = leadMinutes
        self.popupDuration = popupDuration
        self.useNativeIcon = useNativeIcon
        self.nativeIconId = nativeIconId
        self.hold = hold
    }

    // Custom decoder so prefs persisted before `hold` existed still load (defaulting
    // hold to true) instead of failing to decode.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        sound = try c.decodeIfPresent(Bool.self, forKey: .sound) ?? true
        leadMinutes = try c.decodeIfPresent(Int.self, forKey: .leadMinutes) ?? 0
        popupDuration = try c.decodeIfPresent(Int.self, forKey: .popupDuration) ?? 8
        useNativeIcon = try c.decodeIfPresent(Bool.self, forKey: .useNativeIcon) ?? false
        nativeIconId = try c.decodeIfPresent(String.self, forKey: .nativeIconId) ?? ""
        hold = try c.decodeIfPresent(Bool.self, forKey: .hold) ?? true
    }
}
