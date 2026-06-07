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

/// Apple-Reminders watcher settings, persisted app-side (UserDefaults). The
/// server holds none of this — it's sent per-fire.
public struct ReminderPrefs: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var sound: Bool
    public var leadMinutes: Int
    public var popupDuration: Int
    public var useNativeIcon: Bool
    public var nativeIconId: String

    public init(enabled: Bool = false, sound: Bool = true, leadMinutes: Int = 0,
                popupDuration: Int = 8, useNativeIcon: Bool = false, nativeIconId: String = "") {
        self.enabled = enabled
        self.sound = sound
        self.leadMinutes = leadMinutes
        self.popupDuration = popupDuration
        self.useNativeIcon = useNativeIcon
        self.nativeIconId = nativeIconId
    }
}
