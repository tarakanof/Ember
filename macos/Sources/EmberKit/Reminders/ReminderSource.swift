import Foundation

public struct DueReminder: Identifiable, Equatable, Sendable {
    public let id: String
    public let title: String
    public let due: Date
    public let externalID: String

    public init(id: String, title: String, due: Date, externalID: String = "") {
        self.id = id
        self.title = title
        self.due = due
        self.externalID = externalID
    }

    public var dedupeKey: String {
        reminderDedupeKey(id: externalID.isEmpty ? id : externalID, dueDate: due)
    }
}

public protocol ReminderSource: Sendable {
    var hasAccess: Bool { get }

    func dueTimedReminders() async -> [DueReminder]
}
