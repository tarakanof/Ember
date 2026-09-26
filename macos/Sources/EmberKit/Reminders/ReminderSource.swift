import Foundation

/// One incomplete reminder that has a due *time* (date-only reminders are not
/// due at any moment, so sources leave them out). Also the row type of
/// `ReminderScheduler.upcoming`.
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

/// Where `ReminderScheduler` reads reminders from. The app's adapter wraps
/// EventKit; tests use an in-memory fake. A source never mutates reminders.
public protocol ReminderSource: Sendable {
    /// True while reminders may be read (EventKit full access). Checked before
    /// every poll, so revoking access stops firing without a restart.
    var hasAccess: Bool { get }

    /// Every incomplete reminder with a due time, in any order.
    func dueTimedReminders() async -> [DueReminder]
}
