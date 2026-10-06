import Foundation

public struct DueReminder: Identifiable, Equatable, Sendable {
    public let id: String
    public let title: String
    public let due: Date

    public init(id: String, title: String, due: Date) {
        self.id = id
        self.title = title
        self.due = due
    }
}

public protocol ReminderSource: Sendable {
    var hasAccess: Bool { get }

    func dueTimedReminders() async -> [DueReminder]
}
