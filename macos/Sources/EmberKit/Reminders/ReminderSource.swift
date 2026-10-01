import Foundation

/// One incomplete reminder that has a due *time* (date-only reminders are not
/// due at any moment, so sources leave them out).
public struct DueReminder: Identifiable, Equatable, Sendable {
    /// Stable per reminder (EventKit's `calendarItemIdentifier`); a recurring
    /// reminder keeps it across occurrences.
    public let id: String
    public let title: String
    public let due: Date

    public init(id: String, title: String, due: Date) {
        self.id = id
        self.title = title
        self.due = due
    }
}

/// Where `ReminderScheduler` reads reminders from.
public protocol ReminderSource: Sendable {
    /// True while reminders may be read (EventKit full access).
    var hasAccess: Bool { get }

    /// Every incomplete reminder with a due time, in any order.
    func dueTimedReminders() async -> [DueReminder]
}
